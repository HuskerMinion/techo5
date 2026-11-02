//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"log/slog"
	"slices"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// Custom screens: pages of their own over the whole screen, beyond the screen's own (the clock, the
// dashboard, the deck), each a file pair that registers itself (registerScreen) with what the screen asks
// of it: whether this device offers it, what a frame of it shows, how it is drawn, what a finger on it
// does, how often it moves. A work feed fed by the owner's own accounts, a map of the aircraft overhead,
// a page an add-on fills: anything that wants the whole screen and the display's own drawing. Nothing
// else in the package knows them by name. What they share is here and in render_custom.go: a tab for
// each along the top, the ×, the edge swipes, "go home", the voice assistant's pages by name, and the
// idle screen.
//
// A custom screen is on the screen one of two ways:
//
//   - Opened by hand: a swipe in from the left edge of the clock brings up the one shown last (the idle
//     screen, or the first, the first time) when there is no Home Assistant dashboard there to bring
//     up, and so does a swipe up from the bottom edge when there is no deck. The tabs turn between
//     them, the × puts them away, and so does "go home" or the Dashboard returns time without a touch.
//   - As the idle screen (Idle screen in Home Assistant, the config's screen.idle_screen): it stands in
//     for the clock whenever the clock would be the page, as the Dashboard's Show when idle does, and it
//     is there again after a restart. Music playing, a dashboard opened or asked for, and everything else
//     that takes the screen come first; it is back once they are done. The × or the left edge's swipe
//     put it away for a while (dashAway), the clock showing meanwhile, and one turned to from it by a
//     tab goes back to it after the Dashboard returns time.
//
// While up a custom screen takes every finger, as the deck does; what it does with them is its own.

// customScreen is one custom screen, as it registered itself.
type customScreen struct {
	id    string // what the config and the voice assistant's pages call it
	label string // its tab, and its choice in Idle screen
	rank  int    // its place among the tabs, the lowest first

	// offered is whether this device has it (nil for every device): one that needs the owner's own
	// settings, or a screen of a certain size.
	offered func() bool
	// listen has the screen's feature call changed whenever what the screen shows has changed.
	listen func(changed func())
	// fill puts what a frame of the screen shows in v.data, on the frame's goroutine: asking the
	// feature for it is also how the feature knows somebody is looking.
	fill func(d *Display, v *customView)
	// draw draws the screen over the whole screen and returns where a tap means something; customBar
	// is the common top bar, with the tabs and the ×.
	draw func(r *renderer, s scene) []customZone
	// gesture is a finger on the screen, past the tabs and the × (customGesture).
	gesture func(d *Display, g touch.Gesture, at image.Point)
	// key is what a frame of it shows, two frames with the same key looking the same, so the second is
	// not drawn (customFrameKey); nil for a screen that moves, drawn every frame.
	key func(s scene) string
	// next is how long until its next frame; nil for the clock's pace.
	next func(s scene) time.Duration
	// follow is a screen that wants every finger as it moves (touch's follow mode): a map to drag.
	follow bool
}

// customScreens are the screens registered, in their ranks' order.
var customScreens []customScreen

// registerScreen adds a custom screen, from its own file's init.
func registerScreen(c customScreen) {
	customScreens = append(customScreens, c)
	slices.SortStableFunc(customScreens, func(x, y customScreen) int { return x.rank - y.rank })
}

// offeredScreens are the custom screens this device has, in their ranks' order.
func offeredScreens() []customScreen {
	var out []customScreen
	for _, c := range customScreens {
		if c.offered == nil || c.offered() {
			out = append(out, c)
		}
	}
	return out
}

// customByID is the custom screen this device has under id.
func customByID(id string) (customScreen, bool) {
	if id == "" {
		return customScreen{}, false
	}
	for _, c := range offeredScreens() {
		if c.id == id {
			return c, true
		}
	}
	return customScreen{}, false
}

// customAfter is the custom screen step tabs on from id, around the end; "" when there is none.
func customAfter(id string, step int) string {
	all := offeredScreens()
	n := len(all)
	if n == 0 {
		return ""
	}
	i := slices.IndexFunc(all, func(c customScreen) bool { return c.id == id })
	return all[((i+step)%n+n)%n].id
}

// customState is the custom screens between frames: which is up, and since when.
type customState struct {
	up    string // the screen opened by hand, or ""
	last  string // the one shown last: what the edge swipes open
	shown string // the screen drawn in the last frame, rather than under something else; "" for none
	// awayUntil is the idle screen put away by hand: the clock is the page in its place until then.
	awayUntil time.Time
	touched   time.Time
	follow    bool // touch's follow mode asked for, by a screen that wants it
}

// customView is what a frame of a custom screen shows (customScene fills it in).
type customView struct {
	name string // "" when none is up
	data any    // the screen's own: what its fill put there, for its draw
}

// customZone is a place on a custom screen a tap means something: a tab and the × (kind "tab" and
// "close", customBar's), and whatever the screen's own draw names.
type customZone struct {
	r    image.Rectangle
	kind string
	id   string // the tab's screen, or what the screen's own zone is about
}

// customHits are the zones of the screen last drawn, read by the touch goroutine.
var customHits struct {
	sync.Mutex
	zones []customZone
}

func setCustomZones(z []customZone) {
	customHits.Lock()
	customHits.zones = z
	customHits.Unlock()
}

// customZoneAt is the zone under p, the last drawn (topmost) first.
func customZoneAt(p image.Point) (customZone, bool) {
	customHits.Lock()
	defer customHits.Unlock()
	for i := len(customHits.zones) - 1; i >= 0; i-- {
		if p.In(customHits.zones[i].r) {
			return customHits.zones[i], true
		}
	}
	return customZone{}, false
}

// customListening is the screens' data waking the screen while one of them is on it, once.
var customListening sync.Once

// idleScreen is the custom screen that is the idle screen, or "" for the clock: one this device does
// not have, from a build or a device with others, is the clock.
func idleScreen() string {
	id := config.Get().Screen.IdleScreen
	if _, ok := customByID(id); !ok {
		return ""
	}
	return id
}

// openCustom puts a custom screen up: the one named, or for "" the one shown last (the idle screen,
// or the first, the first time). It reports false on a device that has none.
func (d *Display) openCustom(name string) bool {
	all := offeredScreens()
	if len(all) == 0 {
		return false
	}
	idle := idleScreen()
	d.mu.Lock()
	if name == "" {
		name = d.customs.last
	}
	if name == "" {
		name = idle
	}
	if _, ok := customByID(name); !ok {
		name = all[0].id
	}
	d.customs.up, d.customs.last, d.customs.touched, d.customs.awayUntil = name, name, time.Now(), time.Time{}
	d.closeAlert()
	d.calUntil, d.weatherUntil = time.Time{}, time.Time{}
	d.sheet, d.dash, d.drawer, d.deckUp = false, false, false, false
	d.mu.Unlock()
	slog.Info("custom screen up", "screen", name)
	d.wake()
	return true
}

// closeCustom puts the custom screen away: back to the clock, and when one is the idle screen, the
// clock for a while (dashAway) before it comes back.
func (d *Display) closeCustom() {
	idle := idleScreen()
	d.mu.Lock()
	d.customs.up = ""
	if idle != "" {
		d.customs.awayUntil = time.Now().Add(dashAway)
	}
	d.mu.Unlock()
	slog.Info("custom screen closed")
	d.wake()
}

// customShowing is whether a custom screen is on the screen now: drawn in the last frame, and the
// device idle.
func (d *Display) customShowing() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.customs.shown != "" && pageIdle(d.view.Phase)
}

// customScene fills in the custom screen, when one is up or is the idle screen and nothing that comes
// before it has the screen.
func (d *Display) customScene(s *scene, now time.Time) {
	over := s.showSheet || s.showDrawer || s.showCamera || s.showWifi || s.showAlert || s.showCalendar ||
		s.showWeather || s.showRadar || s.bt.Pairing || s.showDeck || s.redClock
	idle := idleScreen()
	d.mu.Lock()
	if d.customs.up != "" && dashForgotten(d.customs.touched) {
		d.customs.up = ""
		slog.Info("custom screen closed: not touched for a while")
	}
	name := d.customs.up
	// The idle screen stands in for the clock: so not over the music, which the clock gives way to,
	// nor over a dashboard opened by hand or asked for by Home Assistant.
	asIdle := name == "" && idle != "" && !d.dash && !s.nowPlaying && !now.Before(d.customs.awayUntil)
	if asIdle {
		name = idle
	}
	c, ok := customByID(name)
	if !ok || s.phase != "idle" || over {
		name = ""
	}
	if asIdle && name != "" && name != d.customs.shown {
		slog.Info("custom screen up: the idle screen", "screen", name)
	}
	d.customs.shown = name
	if name != "" {
		d.customs.last = name
	}
	wantFollow := name != "" && c.follow
	was := d.customs.follow
	d.customs.follow = wantFollow
	d.mu.Unlock()

	// A screen that wants every finger as it moves asks every frame while it is up: the dashboard,
	// going away under it in the frame it came up, turns follow mode off once as it goes.
	if wantFollow || was {
		touch.Get().SetFollow(wantFollow)
	}
	v := customView{name: name}
	if name != "" {
		customListening.Do(func() {
			redraw := func() {
				if d.customShowing() {
					d.wake()
				}
			}
			for _, k := range customScreens {
				if k.listen != nil {
					k.listen(redraw)
				}
			}
		})
		if c.fill != nil {
			c.fill(d, &v)
		}
	}
	s.custom = v
}

// customPage draws the custom screen up.
func (r *renderer) customPage(s scene) {
	c, ok := customByID(s.custom.name)
	if !ok || c.draw == nil {
		setCustomZones(nil)
		return
	}
	setCustomZones(c.draw(r, s))
}

// customFrameKey is what a frame of a custom screen shows, or "" when the frame is not just that
// screen, or the screen moves: two frames with the same key look the same, and the second is not drawn
// (frame's deckDrawn).
func customFrameKey(s scene, ringing, calling bool) string {
	c, ok := customByID(s.custom.name)
	if !ok || c.key == nil || s.showVideo || s.showVideoAsk || s.showVolume || ringing || calling || s.setupAsking ||
		s.showAnnouncement || s.showReminder || s.showNotification || s.popup != nil || s.pin.open || s.announceRecording ||
		s.updateActive || s.cue != "" {
		return ""
	}
	// The time in the top bar changes by the minute.
	return fmt.Sprintf("custom|%s|%s|%s|%v", c.id, c.key(s), s.now.Format("15:04"),
		[5]any{walnut, amber, cream, dim, drawnFont()})
}

// customNext is how long until a custom screen's next frame: its own pace, or the clock's. A screen's
// data changing wakes the screen itself (customScreen.listen).
func customNext(s scene) time.Duration {
	if c, ok := customByID(s.custom.name); ok && c.next != nil {
		return c.next(s)
	}
	return time.Until(s.now.Truncate(idleFrame).Add(idleFrame))
}

// customGesture is a finger on a custom screen: the tabs and the × are every screen's, the rest the
// screen's own.
func (d *Display) customGesture(g touch.Gesture) {
	if d.r == nil {
		return
	}
	d.mu.Lock()
	d.customs.touched = time.Now()
	name := d.customs.shown
	d.mu.Unlock()
	at := image.Pt(g.X, g.Y)

	if g.Kind == touch.Tap {
		if z, ok := customZoneAt(at); ok {
			switch z.kind {
			case "tab":
				if z.id != name {
					d.openCustom(z.id)
				}
				return
			case "close":
				d.closeCustom()
				return
			}
		}
	}
	if c, ok := customByID(name); ok && c.gesture != nil {
		c.gesture(d, g, at)
	}
}

// idleScreenOptions are Idle screen's choices: the clock, then the custom screens this device has.
func idleScreenOptions() []string {
	out := []string{"Clock"}
	for _, c := range offeredScreens() {
		out = append(out, c.label)
	}
	return out
}

// idleScreenIndex is the idle screen's place among idleScreenOptions.
func idleScreenIndex() int {
	id := idleScreen()
	return slices.IndexFunc(offeredScreens(), func(c customScreen) bool { return c.id == id }) + 1
}

// setIdleScreen saves the idle screen, i of idleScreenOptions, and shows it at once.
func (d *Display) setIdleScreen(i int) {
	all := offeredScreens()
	if i < 0 || i > len(all) {
		return
	}
	id := ""
	if i > 0 {
		id = all[i-1].id
	}
	if err := config.Set().Screen().IdleScreen(id); err != nil {
		slog.Error("saving the idle screen failed", "err", err)
		return
	}
	d.mu.Lock()
	d.customs.awayUntil = time.Time{}
	if id != "" && d.customs.up == id {
		d.customs.up = "" // opened by hand, it is there for good now
	}
	d.mu.Unlock()
	if d.idleScreenSel != nil {
		d.idleScreenSel.Set(idleScreenOptions()[i])
	}
	slog.Info("setting changed", "setting", "screen_idle_screen", "using", idleScreenOptions()[i])
	d.wake()
}

// idleScreenSelect is Idle screen in Home Assistant: the clock, or a custom screen in its place while
// nothing else is on the screen. Offered only on a device that has a custom screen (Entities).
func idleScreenSelect(d *Display) *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_idle_screen",
			Name:     "Idle screen",
			Icon:     "mdi:monitor-dashboard",
			Category: esphome.CategoryConfig,
		},
		Options: idleScreenOptions(),
	}
	s.OnCommand = func(v string) {
		if i := slices.Index(idleScreenOptions(), v); i >= 0 {
			d.setIdleScreen(i)
		}
	}
	return s
}
