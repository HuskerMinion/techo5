//go:build !dot && !spot

// Package display is the Echo Show's screen: what the device shows on it, what a finger on it does,
// and what Home Assistant gets for it.
//
// The screen follows the conversation. Idle, it is a clock; while a turn runs it says what the
// device is doing and shows the words — what was heard, then the answer — and lets them linger a
// while after the turn ends. Everything is drawn by the daemon itself onto the kernel framebuffer
// (hardware/screen): no compositor, no browser, no Android.
//
// A tap does what the Dot's action button does: starts a turn, or ends the one running; on a dark
// screen it only lights it. A vertical swipe is the volume, a notch per step, with the level shown
// while it moves. The room's light dims the panel when auto-brightness is on; what Home Assistant
// sets is the ceiling.
//
// To Home Assistant the screen is a light with brightness only — on/off and how bright, which is
// what people automate — plus a switch for auto-brightness.
package display

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/btaudio"
	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/ambient"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/screen"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
	"github.com/HuskerMinion/techo5/echod/internal/service"
)

func init() {
	// A panel that cannot be opened is retried rather than given up on: the device answers without
	// it, and a boot where the node was late should still end with a screen.
	component.Register(component.Device, Get(), component.Order(60),
		component.Supervise(service.Restart(time.Second, 30*time.Second)))
}

const (
	// linger is how long the last turn's words stay on the screen after it ends.
	linger = 12 * time.Second

	// volumeShow is how long the level stays up after it last moved.
	volumeShow = 2 * time.Second

	// idleFrame and activeFrame are how often the screen is redrawn: once a second for a clock, and
	// fast enough for the listening indicator to breathe and the volume to follow a finger.
	idleFrame   = time.Second
	activeFrame = 150 * time.Millisecond

	// floor is the dimmest an "on" backlight goes; below it the panel reads as off.
	floor = 8

	// Auto-brightness: the fraction of the ceiling the room's light allows, from darkFraction in the
	// dark rising on a log curve to the full ceiling at brightLux. Applied through a running average
	// so a passing shadow does not flicker the panel.
	darkFraction = 0.12
	brightLux    = 400.0
	autoSmooth   = 0.25
)

type Display struct {
	light *esphome.Light
	auto  *esphome.Switch
	clock *esphome.Select

	mu      sync.Mutex
	on      bool
	ceiling int // percent Home Assistant asked for
	autoOn  bool
	level   float64 // backlight actually applied, 0..BacklightMax, as a running average
	view    voice.State
	viewAt  time.Time
	volume  int
	volAt   time.Time

	poke chan struct{}

	// shots are screenshot requests, answered with a copy of the next frame drawn.
	shots chan chan *image.RGBA
	dev   *screen.Device
	r     *renderer

	// booting is the splash: from the first frame until Home Assistant is listening and at least
	// splashMin has passed.
	booting bool
	started time.Time
	logo    *splash

	// sheet is the settings sheet being shown; restartArm is the first of the two taps Restart wants.
	sheet      bool
	restartArm time.Time

	// cat is the settings screen's open category; picker is the row whose list of choices is open
	// over it, or empty.
	cat    category
	picker string

	// cardScroll and pickScroll are how far the card and an open list are scrolled, in pixels;
	// openedBy is where the swipe that opened the screen started, so its last notches are ignored.
	cardScroll, pickScroll int
	openedBy               image.Point

	// checking is an update check asked for from the screen, still out; colours is the custom
	// colours editor open on the Display card.
	checking bool
	colours  bool

	// folder is the slideshow's folder list, as far as it has been opened.
	folder folderView

	// drawer is Cameras and Radio, in from the right over the clock: drawerTab is which,
	// drawerScroll how far its list is scrolled, and drawerPick a list of choices open over it.
	drawer       bool
	drawerTab    int
	drawerScroll int
	drawerPick   string

	// touchedAt is the last finger on the panel; nightDark is the screen having been put out by the
	// night schedule rather than by anyone.
	touchedAt time.Time
	nightDark bool

	// slideshowIdleSince is when the screen last became the plain idle page (nothing else showing);
	// zero while it is not. Screensaver mode waits for this to run long enough before taking over.
	slideshowIdleSince time.Time

	// wifi is the Wi-Fi pages' state; wifiOpen shows them. wifiAt is when the status was last read.
	wifi     wifiState
	wifiOpen bool
	wifiAt   time.Time

	// quiet is a turn that was a screen command ("go home", "show the deck"): its words and reply
	// are not shown, so the screen moves at once. radioCue is when Home Assistant last named a
	// station as playing, which comes seconds before the stream does.
	quiet    bool
	radioCue time.Time

	// weatherArmed is a weather question in progress; weatherUntil is how long the forecast page
	// stays once the turn is over.
	weatherArmed bool
	weatherUntil time.Time
	// radar is the rain map in place of the forecast, while the weather page is up.
	radar bool

	// draft is the alarm open in the Alarms card's editor; ringPreview shows the ringing page silently
	// until then.
	draft       *alarmDraft
	ringPreview time.Time

	// demoUntil puts placeholders where the sheet shows the owner's details (name, network,
	// address, SSH key names), for screenshots that are going to be published.
	demoUntil time.Time
}

var (
	once   sync.Once
	shared *Display
)

func Get() *Display {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Display {
	d := &Display{
		light: &esphome.Light{
			Base:                esphome.Base{ObjectID: "screen", Name: "Screen", Icon: "mdi:monitor"},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeBrightness},
		},
		auto: &esphome.Switch{
			Base: esphome.Base{
				ObjectID: "screen_auto_brightness",
				Name:     "Screen auto-brightness",
				Icon:     "mdi:brightness-auto",
				Category: esphome.CategoryConfig,
			},
		},
		poke:  make(chan struct{}, 1),
		shots: make(chan chan *image.RGBA, 4),
		view:  voice.State{Phase: "idle"},
	}
	d.light.OnCommand = d.command
	d.auto.OnCommand = func(on bool) { d.setAuto(on, true) }
	d.clock = clockSelect(d.wake)
	voice.Changed.Listen(d.changed)
	media.Get().OnVolume.Listen(d.volumeMoved)
	ambient.Get().Lux.Listen(d.lux)
	touch.Get().Gestures.Listen(d.gesture)
	// A device with no address a while after boot gets the Wi-Fi page without being asked: a
	// fresh unit, or one carried to another house.
	go func() {
		time.Sleep(90 * time.Second)
		if wifi.Available() && wifi.Current(context.Background()).Address == "" {
			d.mu.Lock()
			open := d.wifiOpen
			d.mu.Unlock()
			if !open {
				slog.Info("wifi: no address after boot, opening setup")
				d.openWifi()
			}
		}
	}()
	hastate.Get().Changed.Listen(func(u hastate.Update) {
		// The first value is the station that played last, sent when Home Assistant connects after
		// a start; only a change means a station is starting.
		if u.First || u.Attribute != "" || u.Entity == "" || u.Entity != config.Get().Home.Radio.Now {
			return
		}
		if u.Value == "" || u.Value == "unknown" || u.Value == "unavailable" {
			return
		}
		d.mu.Lock()
		d.radioCue = time.Now()
		d.mu.Unlock()
		d.wake()
	})
	btaudio.Get().Changed.Listen(func(btaudio.State) { d.wake() })
	phone.Get().Changed.Listen(func(phone.State) { d.wake() })
	security.Get().Changed.Listen(func(struct{}) { d.wake() })
	alarm.Get().Changed.Listen(func(struct{}) { d.wake() })
	timer.Get().Changed.Listen(func(struct{}) { d.wake() })
	home.Get().Changed.Listen(func(struct{}) { d.wake() })
	return d
}

func (d *Display) Name() string { return "screen" }

func (d *Display) Entities() []esphome.Entity { return []esphome.Entity{d.light, d.auto, d.clock} }

// Restore lights the panel the way it was left. Before the framebuffer is opened: the backlight is
// its own device.
func (d *Display) Restore(c config.Config) {
	setClock24(d.clock, c.Screen.Clock24)
	d.setAuto(c.Screen.Auto, false)
	d.apply(c.Screen.On, c.Screen.Brightness, false)
}

// command is Home Assistant changing the light. A bare "on" carries no brightness; the last one stays.
func (d *Display) command(s esphome.LightState) {
	pct := int(math.Round(float64(s.Brightness) * 100))
	if s.On && s.Brightness == 0 {
		pct = int(math.Round(float64(d.light.Get().Brightness) * 100))
		if pct == 0 {
			pct = config.DefaultScreenBrightness
		}
	}
	d.apply(s.On, pct, true)
}

// apply sets the light's state: the ceiling, and whether the panel is lit at all.
func (d *Display) apply(on bool, pct int, save bool) {
	pct = min(max(pct, 0), 100)
	d.mu.Lock()
	d.on, d.ceiling = on, pct
	d.mu.Unlock()
	d.relight(true)

	d.light.Set(esphome.LightState{On: on, Brightness: float32(pct) / 100, ColorMode: esphome.ColorModeBrightness})
	d.wake()

	if save {
		if err := config.Set().Screen().On(on); err != nil {
			slog.Error("saving the screen state failed", "err", err)
		}
		if err := config.Set().Screen().Brightness(pct); err != nil {
			slog.Error("saving the screen brightness failed", "err", err)
		}
	}
	slog.Info("screen", "on", on, "brightness", pct)
}

func (d *Display) setAuto(on bool, save bool) {
	d.mu.Lock()
	d.autoOn = on
	d.mu.Unlock()
	d.auto.Set(on)
	d.relight(true)
	if save {
		if err := config.Set().Screen().Auto(on); err != nil {
			slog.Error("saving the auto-brightness setting failed", "err", err)
		}
		slog.Info("screen auto-brightness", "on", on)
	}
}

// relight works out the backlight from the ceiling, the room and whether the panel is on, and
// applies it. jump skips the smoothing, for a change the user just asked for.
func (d *Display) relight(jump bool) {
	d.mu.Lock()
	target := 0.0
	if d.on {
		target = float64(d.ceiling) * screen.BacklightMax / 100
		if d.autoOn {
			if lux, _, ok := ambient.Get().Current(); ok {
				target *= allowed(lux)
			}
		}
		target = math.Max(target, floor)
	}
	if jump || d.level == 0 {
		d.level = target
	} else {
		d.level += (target - d.level) * autoSmooth
	}
	level := int(math.Round(d.level))
	d.mu.Unlock()

	if err := screen.SetBacklight(level); err != nil {
		slog.Warn("setting the backlight failed", "err", err)
	}
}

// allowed is the fraction of the ceiling a room this bright gets.
func allowed(lux float64) float64 {
	f := darkFraction + (1-darkFraction)*math.Log10(1+math.Max(lux, 0))/math.Log10(1+brightLux)
	return math.Min(math.Max(f, darkFraction), 1)
}

// lux is a reading from the room. On the sensor's goroutine, twice a second.
func (d *Display) lux(float64) {
	d.mu.Lock()
	auto, on := d.autoOn, d.on
	d.mu.Unlock()
	if auto && on {
		d.relight(false)
	}
}

// changed is the conversation moving on. It runs on the conversation's goroutine, so it only
// records and wakes the loop.
func (d *Display) changed(s voice.State) {
	d.mu.Lock()
	newHeard := s.Heard != "" && s.Heard != d.view.Heard
	if s.Phase == "listening" && d.view.Phase != "listening" {
		d.quiet = false
	}
	d.view = s
	d.viewAt = time.Now()
	// A question about the weather brings the forecast page up once the answer is done, for a
	// while, and then the screen goes back to whatever it was showing.
	if newHeard && aboutWeather(s.Heard) {
		d.weatherArmed = true
		d.radar = aboutRadar(s.Heard)
	}
	// "Show the front door": the camera goes up at once, while the assistant answers. Once per
	// sentence: the state repeats the transcript on every phase change.
	if newHeard {
		if entity := home.Get().MatchCamera(s.Heard); entity != "" {
			d.quiet = true
			go home.Get().ShowCamera(entity, cameraVoiceShow)
		}
		// "Go home": whatever page is up comes down, back to the clock, and music stops rather than
		// holding the now-playing page.
		if h := strings.ToLower(s.Heard); strings.Contains(h, "go home") || strings.Contains(h, "home screen") || strings.Contains(h, "main screen") {
			d.weatherArmed, d.weatherUntil = false, time.Time{}
			d.sheet, d.quiet = false, true
			go home.Get().HideCamera()
			go func() {
				if playing, paused := media.Get().Playing(); playing || paused {
					home.Get().Stop()
					media.Get().Stop()
				}
			}()
			slog.Info("screen: home by voice")
		}
	}
	if s.Phase == "idle" && d.weatherArmed {
		d.weatherArmed = false
		d.weatherUntil = time.Now().Add(weatherShow)
	}
	d.mu.Unlock()
	d.wake()
}

// aboutRadar is whether what was heard asked for the rain map rather than the forecast.
func aboutRadar(heard string) bool {
	h := strings.ToLower(heard)
	return strings.Contains(h, "radar") || strings.Contains(h, "rain map") || strings.Contains(h, "weather map")
}

// aboutWeather is whether what was heard asked about the weather.
func aboutWeather(heard string) bool {
	h := strings.ToLower(heard)
	for _, w := range []string{"weather", "forecast", "temperature", "rain", "snow", "how hot", "how cold", "radar", "storm"} {
		if strings.Contains(h, w) {
			return true
		}
	}
	return false
}

// volumeMoved is the level changing on purpose; the screen shows it for a moment.
func (d *Display) volumeMoved(step int) {
	d.mu.Lock()
	d.volume, d.volAt = step, time.Now()
	d.mu.Unlock()
	d.wake()
}

// gesture is a finger on the panel. A dark screen only lights up; otherwise a tap is the action
// button and a vertical swipe the volume.
func (d *Display) gesture(g touch.Gesture) {
	d.mu.Lock()
	on := d.on
	d.mu.Unlock()
	slog.Info("touch", "gesture", g.String())
	d.mu.Lock()
	d.touchedAt = time.Now()
	d.mu.Unlock()

	if !on {
		if g.Kind == touch.Tap {
			d.apply(true, d.ceilingOrDefault(), true)
		}
		return
	}
	// A call: its page takes every tap.
	if st := phone.Get().State(); st.Phase != phone.Idle {
		if g.Kind == touch.Tap {
			d.callTap(g.X, g.Y, st)
		}
		d.wake()
		return
	}

	// A timer or an alarm ringing: its page takes every tap.
	if st := d.ringing(time.Now()); st.any() {
		if g.Kind == touch.Tap {
			d.ringTap(g.X, g.Y, st)
		}
		d.wake()
		return
	}

	// The first-run card: any tap puts it away for good.
	if !config.Get().Screen.Welcomed {
		if g.Kind == touch.Tap {
			if err := config.Set().Screen().Welcomed(true); err != nil {
				slog.Warn("saving the welcome failed", "err", err)
			}
			slog.Info("first-run card put away")
			d.wake()
		}
		return
	}

	// The pairing page: a tap on a row pairs or connects it, the bar at the bottom ends the page.
	// A swipe from the right opens it from the clock.
	bt := btaudio.Get()
	if bt.Pairing() {
		switch g.Kind {
		case touch.Tap:
			if d.r == nil {
				return
			}
			st := bt.State()
			switch row := d.r.btRowAt(g.Y); {
			case row == btRows:
				bt.SetPairing(false)
			case row >= 0 && row < len(st.Devices) && !st.Devices[row].Busy:
				bt.Choose(st.Devices[row].Address)
			}
		case touch.SwipeUp:
			media.Get().Adjust(+1)
		case touch.SwipeDown:
			media.Get().Adjust(-1)
		case touch.SwipeRight:
			bt.SetPairing(false)
		}
		d.wake()
		return
	}

	// A live camera: a tap takes it down.
	if _, up := home.Get().Camera(); up {
		if g.Kind == touch.Tap {
			home.Get().HideCamera()
		}
		d.wake()
		return
	}

	// The Wi-Fi pages take every tap while they are up.
	d.mu.Lock()
	wifiOpen := d.wifiOpen
	d.mu.Unlock()
	if wifiOpen {
		if g.Kind == touch.Tap && d.r != nil {
			d.wifiTap(g.X, g.Y)
		}
		d.wake()
		return
	}

	// The drawer, Cameras and Radio: while it is in, every finger is its.
	d.mu.Lock()
	drawerIn := d.drawer
	d.mu.Unlock()
	if drawerIn {
		d.drawerGesture(g)
		d.wake()
		return
	}

	// The settings screen: taps land on what it drew, vertical swipes scroll it.
	d.mu.Lock()
	sheet := d.sheet
	d.mu.Unlock()
	if sheet {
		// Vertical swipes do nothing here: the swipe that opened the sheet keeps reporting notches
		// until the finger lifts, and those must not turn into volume steps. The Volume row has
		// buttons instead.
		switch {
		case d.r == nil:
		case g.Kind == touch.Tap:
			d.mu.Lock()
			d.openedBy = image.Point{-1, -1}
			d.mu.Unlock()
			d.nextTap(g.X, g.Y)
		case g.Kind == touch.SwipeUp || g.Kind == touch.SwipeDown:
			d.sheetSwipe(g)
		}
		d.wake()
		return
	}

	switch g.Kind {
	case touch.Tap:
		// A short swipe from the top edge that never made a notch arrives as a tap; it must not
		// start a turn. The top band is the sheet's, taps there do nothing.
		if g.Y < topEdge {
			return
		}
		d.mu.Lock()
		weatherUp := time.Now().Before(d.weatherUntil)
		idle := d.view.Phase == "idle"
		d.mu.Unlock()
		if weatherUp {
			// The weather page: its button turns between the forecast and the rain map, and keeps the
			// page up a while longer; a tap anywhere else puts it away.
			d.mu.Lock()
			if d.r != nil && g.X >= 0 && image.Pt(g.X, g.Y).In(d.r.weatherButton()) {
				d.radar = !d.radar
				d.weatherUntil = time.Now().Add(weatherShow)
			} else {
				d.weatherUntil, d.radar = time.Time{}, false
			}
			d.mu.Unlock()
			d.wake()
			return
		}
		if idle && d.nowPlaying() {
			// The now-playing screen: a tap is play/pause.
			if playing, _ := media.Get().Playing(); playing {
				media.Get().Pause()
			} else {
				media.Get().Resume()
			}
			return
		}
		voice.Get().Action()
	case touch.SwipeLeft:
		// From the right edge it brings the drawer in, on the tab it was last on.
		if d.r != nil && g.X >= d.r.w-drawerEdge {
			d.mu.Lock()
			tab := d.drawerTab
			d.mu.Unlock()
			d.openDrawer(tab)
		}
	case touch.SwipeUp:
		media.Get().Adjust(+1)
	case touch.SwipeDown:
		// From the top edge it is the sheet; anywhere else it is the volume.
		if g.Y < topEdge {
			d.mu.Lock()
			d.openedBy = image.Pt(g.X, g.Y)
			d.mu.Unlock()
			d.showSheet(true)
			return
		}
		media.Get().Adjust(-1)
	}
}

// nowPlaying is whether the idle screen should be the radio's: something playing or paused, and
// the radio wired up so the page has a name to show.
func (d *Display) nowPlaying() bool {
	if !home.Get().Radio().Configured {
		return false
	}
	playing, paused := media.Get().Playing()
	if playing || paused {
		return true
	}
	// Home Assistant named a station a moment ago: the stream is on its way, show the page now.
	d.mu.Lock()
	cued := time.Since(d.radioCue) < radioCueFor
	d.mu.Unlock()
	return cued
}

// radioCueFor is how long the now-playing page is shown on Home Assistant's word alone, before
// the stream itself has to be playing to keep it.
const radioCueFor = 20 * time.Second

func (d *Display) showSheet(on bool) {
	d.mu.Lock()
	d.sheet = on
	d.restartArm, d.picker, d.cardScroll, d.pickScroll, d.colours = time.Time{}, "", 0, 0, false
	d.mu.Unlock()
	slog.Info("settings sheet", "open", on)
	d.wake()
}

// ShowWeather puts the weather page up, the forecast or the rain map, as a question would.
func (d *Display) ShowWeather(radar bool) {
	d.mu.Lock()
	d.weatherUntil, d.radar, d.sheet = time.Now().Add(weatherShow), radar, false
	d.mu.Unlock()
	d.wake()
}

func (d *Display) ceilingOrDefault() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ceiling == 0 {
		return config.DefaultScreenBrightness
	}
	return d.ceiling
}

// night puts the screen out inside the night window once nothing has happened for a while, and
// brings it back when the window ends. It reports true when it changed the screen, so the frame
// is redrawn from the new state.
func (d *Display) night(now time.Time, on bool, view voice.State) bool {
	in := inNight(config.Get().Screen.Night, now)
	d.mu.Lock()
	dark, touched := d.nightDark, d.touchedAt
	d.mu.Unlock()
	switch {
	case in && on:
		busy := view.Phase != "idle" || now.Sub(touched) < nightIdle || now.Sub(d.viewAt) < nightIdle || d.ringing(now).any() || phone.Get().Busy()
		if playing, _ := media.Get().Playing(); playing || busy {
			return false
		}
		slog.Info("screen: night, going dark")
		d.mu.Lock()
		d.nightDark = true
		d.mu.Unlock()
		d.apply(false, d.ceilingOrDefault(), false)
		return true
	case !in && dark && !on:
		slog.Info("screen: night over, back on")
		d.mu.Lock()
		d.nightDark = false
		d.mu.Unlock()
		d.apply(true, d.ceilingOrDefault(), false)
		return true
	case !in && dark:
		d.mu.Lock()
		d.nightDark = false
		d.mu.Unlock()
	}
	return false
}

// nightIdle is how long the panel stays lit after a finger or a turn during the night.
const nightIdle = 90 * time.Second

// nightPresets are the Screen off at night list's choices.
var nightPresets = []string{"", "22-6", "23-6", "0-7", "21-7", "23-8"}

func nightHours(v string) (from, to int, ok bool) {
	if _, err := fmt.Sscanf(v, "%d-%d", &from, &to); err != nil || from < 0 || from > 23 || to < 0 || to > 23 || from == to {
		return 0, 0, false
	}
	return from, to, true
}

// inNight is whether now falls in the window, which may cross midnight.
func inNight(v string, now time.Time) bool {
	from, to, ok := nightHours(v)
	if !ok {
		return false
	}
	h := now.Hour()
	if from < to {
		return h >= from && h < to
	}
	return h >= from || h < to
}

// ---- Wi-Fi pages ----

// OpenWifi shows the Wi-Fi pages; demo puts the keyboard up for a made-up network, for a look at
// the layout from afar. CloseWifi takes them down.
func (d *Display) OpenWifi(demo bool) {
	d.openWifi()
	if demo {
		d.mu.Lock()
		d.wifi.pick = &wifi.Network{SSID: "Example Network", Secured: true}
		d.wifi.text = "correct horse"
		d.mu.Unlock()
		d.wake()
	}
}

func (d *Display) CloseWifi() { d.closeWifi() }

// openWifi shows the network list and starts a scan.
func (d *Display) openWifi() {
	d.mu.Lock()
	d.wifiOpen = true
	d.wifi = wifiState{scanning: true}
	d.mu.Unlock()
	wifi.SettingUp(true)
	slog.Info("wifi page", "open", true)
	go d.refreshWifi()
	go d.wifiScan()
	d.wake()
}

func (d *Display) closeWifi() {
	d.mu.Lock()
	d.wifiOpen = false
	d.wifi = wifiState{}
	d.mu.Unlock()
	wifi.SettingUp(false)
	slog.Info("wifi page", "open", false)
	d.wake()
}

func (d *Display) refreshWifi() {
	st := wifi.Current(context.Background())
	d.mu.Lock()
	d.wifi.status = st
	d.mu.Unlock()
	d.wake()
}

func (d *Display) wifiScan() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	nets, err := wifi.Scan(ctx)
	d.mu.Lock()
	d.wifi.scanning = false
	if err != nil {
		d.wifi.err = "scan failed: " + err.Error()
	} else {
		d.wifi.nets, d.wifi.err = nets, ""
	}
	d.mu.Unlock()
	d.wake()
}

// wifiTap is a finger on the Wi-Fi pages.
func (d *Display) wifiTap(x, y int) {
	d.mu.Lock()
	w := d.wifi
	d.mu.Unlock()
	if w.pick != nil {
		d.wifiKey(d.r.keyAt(x, y, w.symbols))
		return
	}
	h := d.r.wifiListHit(x, y)
	switch {
	case h.done:
		d.closeWifi()
	case h.rescan:
		d.mu.Lock()
		d.wifi.scanning, d.wifi.err = true, ""
		d.mu.Unlock()
		go d.wifiScan()
	case h.row >= 0:
		start, end, more := pageWith(len(w.nets), w.page, wifiRows)
		if more && h.row == wifiRows-1 {
			d.mu.Lock()
			d.wifi.page++
			d.mu.Unlock()
			return
		}
		if start+h.row >= end {
			return
		}
		n := w.nets[start+h.row]
		if !n.Secured {
			d.wifiJoin(n, "")
			return
		}
		d.mu.Lock()
		d.wifi.pick, d.wifi.text, d.wifi.shift, d.wifi.symbols, d.wifi.err = &n, "", false, false, ""
		d.mu.Unlock()
	}
}

// wifiKey is a key on the passphrase keyboard.
func (d *Display) wifiKey(k string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.wifi.busy != "" {
		return
	}
	switch k {
	case "":
	case "shift":
		d.wifi.shift = !d.wifi.shift
	case "symbols":
		d.wifi.symbols = !d.wifi.symbols
	case "space":
		d.wifi.text += " "
	case "backspace":
		if n := len(d.wifi.text); n > 0 {
			d.wifi.text = d.wifi.text[:n-1]
		}
	case "cancel":
		d.wifi.pick, d.wifi.text = nil, ""
	case "join":
		n := *d.wifi.pick
		text := d.wifi.text
		d.mu.Unlock()
		d.wifiJoin(n, text)
		d.mu.Lock()
	default:
		if d.wifi.shift && !d.wifi.symbols {
			k = strings.ToUpper(k)
			d.wifi.shift = false
		}
		d.wifi.text += k
	}
}

// wifiJoin joins a network in the background and shows how it went.
func (d *Display) wifiJoin(n wifi.Network, passphrase string) {
	d.mu.Lock()
	d.wifi.busy, d.wifi.err = "Connecting to "+n.SSID+"…", ""
	d.mu.Unlock()
	d.wake()
	go func() {
		err := wifi.Join(context.Background(), n.SSID, passphrase)
		d.mu.Lock()
		d.wifi.busy = ""
		if err != nil {
			d.wifi.err = err.Error()
			slog.Warn("wifi: join failed", "ssid", n.SSID, "err", err)
		} else {
			d.wifi.pick, d.wifi.text = nil, ""
			slog.Info("wifi: joined", "ssid", n.SSID)
		}
		d.mu.Unlock()
		d.refreshWifi()
	}()
}

// answerShots hands a copy of the canvas to whoever asked for a screenshot.
func (d *Display) answerShots() {
	for {
		select {
		case ch := <-d.shots:
			var img *image.RGBA
			if d.r != nil {
				img = image.NewRGBA(d.r.dst.Rect)
				copy(img.Pix, d.r.dst.Pix)
			}
			ch <- img
		default:
			return
		}
	}
}

// Screenshot is the next frame drawn, for a look at the panel from afar.
func (d *Display) Screenshot(ctx context.Context) (*image.RGBA, error) {
	ch := make(chan *image.RGBA, 1)
	select {
	case d.shots <- ch:
	default:
		return nil, errors.New("too many screenshot requests")
	}
	d.wake()
	select {
	case img := <-ch:
		if img == nil {
			return nil, errors.New("the screen is not up")
		}
		return img, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// SetTheme switches to a preset by name and saves it.
func (d *Display) SetTheme(name string) {
	if err := config.Set().Screen().Theme(themes[themeIndex(name)].name); err != nil {
		slog.Warn("saving the theme failed", "err", err)
	}
	d.wake()
}

// Demo puts placeholders in for the owner's details for a while, for published screenshots.
func (d *Display) Demo(for_ time.Duration) {
	d.mu.Lock()
	d.demoUntil = time.Now().Add(for_)
	d.mu.Unlock()
	d.wake()
}

// OpenSheet puts the settings sheet up on the named tab, or takes it down for "off". It reports whether
// the name meant anything.
func (d *Display) OpenSheet(name string) bool {
	if strings.EqualFold(name, "off") {
		d.showSheet(false)
		return true
	}
	switch strings.ToLower(name) {
	case "cameras":
		d.showSheet(false)
		d.openDrawer(drawerCameras)
		return true
	case "radio":
		d.showSheet(false)
		d.openDrawer(drawerRadio)
		return true
	}
	cat, ok := catByName(name)
	if !ok {
		return false
	}
	d.closeDrawer()
	d.mu.Lock()
	d.sheet, d.cat, d.picker, d.restartArm = true, cat, "", time.Time{}
	d.draft, d.cardScroll, d.pickScroll = nil, 0, 0
	d.mu.Unlock()
	d.wake()
	return true
}

func (d *Display) wake() {
	select {
	case d.poke <- struct{}{}:
	default:
	}
}

// Start opens the framebuffer.
func (d *Display) Start(context.Context) error {
	dev, err := screen.Open()
	if err != nil {
		return err
	}
	d.dev = dev
	d.r = newRenderer(dev.Canvas())
	w, h := dev.Size()
	d.logo = newSplash(w, h)
	d.mu.Lock()
	d.booting, d.started = true, time.Now()
	d.mu.Unlock()
	slog.Info("screen open", "fb", dev.String())
	return nil
}

func (d *Display) Close() error {
	if d.dev == nil {
		return nil
	}
	err := d.dev.Close()
	d.dev = nil
	return err
}

// Run redraws the screen until ctx is cancelled: on the second while idle, faster while a turn is
// on or the volume is showing, and at once when something changes.
func (d *Display) Run(ctx context.Context) error {
	for {
		wait := d.frame()
		select {
		case <-ctx.Done():
			return nil
		case <-d.poke:
		case <-time.After(wait):
		}
	}
}

// frame draws what the moment calls for and says how long until the next one is due.
func (d *Display) frame() time.Duration {
	d.mu.Lock()
	on, view, at := d.on, d.view, d.viewAt
	volume, volAt := d.volume, d.volAt
	d.mu.Unlock()

	now := time.Now()
	ring := d.ringing(now)
	call := phone.Get().State()
	if (ring.any() || call.Phase != phone.Idle) && !on {
		// A ring or a call lights a dark panel, night or not: its page is how it is answered or stopped.
		d.apply(true, d.ceilingOrDefault(), false)
		on = true
	}
	if d.night(now, on, view) {
		return time.Minute
	}
	if !config.Get().Screen.Welcomed {
		d.r.welcome(scene{now: now})
		if err := d.dev.Present(); err != nil {
			slog.Warn("presenting the frame failed", "err", err)
		}
		d.answerShots()
		return time.Second
	}
	if !on {
		// Dark panel: nothing to draw, and nothing to redraw until told, or until the night ends.
		d.answerShots()
		if config.Get().Screen.Night != "" {
			return time.Minute
		}
		return time.Hour
	}
	applyTheme(current())

	d.mu.Lock()
	booting, started := d.booting, d.started
	if booting && now.Sub(started) >= splashMin && voice.Get().Ready() {
		d.booting, booting = false, false
		slog.Info("splash done", "after", now.Sub(started).Round(time.Millisecond))
	}
	d.mu.Unlock()
	if booting {
		d.r.drawSplash(d.logo, now.Sub(started))
		if err := d.dev.Present(); err != nil {
			slog.Warn("presenting the frame failed", "err", err)
		}
		return 80 * time.Millisecond
	}

	s := scene{now: now, phase: view.Phase, heard: view.Heard, reply: view.Reply, since: at, ring: ring, call: call}
	s.snooze = config.Get().Alarms.Snooze()
	s.alarms = alarm.Get().View(now)
	s.timers = timer.Get().List(now)
	if view.Phase == "idle" && (view.Heard != "" || view.Reply != "") && now.Sub(at) < linger {
		s.phase = "lingering"
	}
	d.mu.Lock()
	quiet := d.quiet
	d.mu.Unlock()
	if quiet && (s.phase == "thinking" || s.phase == "replying" || s.phase == "lingering") {
		// A screen command: the screen it asked for is the answer, not the words.
		s.phase, s.heard, s.reply = "idle", "", ""
	}
	s.playing, s.paused = media.Get().Playing()
	s.muted, _ = mute.Get().Muted()
	if !volAt.IsZero() && now.Sub(volAt) < volumeShow {
		s.volume, s.showVolume = volume, true
	}
	s.bt = btaudio.Get().State()
	d.mu.Lock()
	s.showSheet = d.sheet
	s.showWifi, s.wifi = d.wifiOpen, d.wifi
	restartArm := d.restartArm
	wifiAt := d.wifiAt
	d.mu.Unlock()
	if (s.showSheet || s.showWifi) && now.Sub(wifiAt) > 5*time.Second && wifi.Available() {
		d.mu.Lock()
		d.wifiAt = now
		d.mu.Unlock()
		go d.refreshWifi()
	}
	if s.showSheet {
		s.sheet = d.gather(s, restartArm)
		if d.cat == catSecurity {
			s.security = security.Get().State()
		}
		d.mu.Lock()
		demo := now.Before(d.demoUntil)
		d.mu.Unlock()
		if demo {
			s.sheet.name, s.sheet.wifi, s.sheet.address = "Kitchen", "HomeWiFi  ·  192.168.1.50", "192.168.1.50"
			s.sheet.wifiName, s.sheet.weather, s.sheet.demo = "HomeWiFi", "Home", true
			for i := range s.security.Keys {
				s.security.Keys[i] = "laptop"
			}
		}
		d.mu.Lock()
		if d.draft != nil && d.cat == catAlarms {
			c := *d.draft
			s.draft = &c
		}
		d.mu.Unlock()
	}
	s.camera, s.showCamera = home.Get().Camera()
	s.nowPlaying = (s.phase == "idle") && d.nowPlaying()
	d.mu.Lock()
	s.showDrawer, s.drawerTab, s.drawerScroll, s.drawerPick, s.pickScroll = d.drawer && !s.showSheet && !s.showCamera, d.drawerTab, d.drawerScroll, d.drawerPick, d.pickScroll
	d.mu.Unlock()
	d.mu.Lock()
	s.demo = now.Before(d.demoUntil)
	d.mu.Unlock()
	if s.showDrawer && s.drawerTab == drawerCameras {
		s.cameras = home.Get().Cameras()
	}
	if (s.showDrawer && s.drawerTab == drawerRadio) || s.nowPlaying {
		s.radio = home.Get().Radio()
	}
	s.weather = home.Get().Weather()
	d.mu.Lock()
	s.showWeather = (s.phase == "idle" || s.phase == "lingering") && now.Before(d.weatherUntil)
	s.showRadar = s.showWeather && d.radar
	d.mu.Unlock()
	if s.showWeather {
		s.forecast = home.Get().Forecast()
	}
	if s.showRadar {
		s.radar = home.Get().Radar()
	}

	// boring is the plain idle page — the same set of pages draw() checks before falling through to
	// bigClock/nowPlaying. Background mode rides along with it; Screensaver only takes over once it
	// has held for the configured wait, tracked by how long it has run continuously.
	boring := s.phase == "idle" && call.Phase == phone.Idle && !ring.any() && !s.bt.Pairing &&
		!s.showWifi && !s.showSheet && !s.showCamera && !s.showRadar && !s.showWeather && !s.nowPlaying
	if boring {
		s.slideshow = home.Get().SlideshowBackground()
	}
	d.mu.Lock()
	if !boring {
		d.slideshowIdleSince = time.Time{}
	} else if d.slideshowIdleSince.IsZero() {
		d.slideshowIdleSince = now
	}
	idleSince := d.slideshowIdleSince
	d.mu.Unlock()
	if boring && !idleSince.IsZero() && now.Sub(idleSince) >= home.Get().SlideshowIdleTimeout() {
		s.slideshowScreensaver = home.Get().SlideshowScreensaverPhoto()
		s.slideshowOverlay = home.Get().SlideshowOverlay()
	}

	d.r.draw(s)
	if err := d.dev.Present(); err != nil {
		slog.Warn("presenting the frame failed", "err", err)
	}
	d.answerShots()

	if s.showCamera {
		return 250 * time.Millisecond // frames arrive as they are fetched; this keeps up
	}
	if ring.any() || call.Phase != phone.Idle {
		return 500 * time.Millisecond
	}
	if s.bt.Pairing || s.showSheet || s.showWifi || s.showDrawer {
		return 500 * time.Millisecond
	}
	if s.showRadar && len(s.radar.Frames) > 1 {
		return radarStep
	}
	if (s.slideshow != nil || s.slideshowScreensaver != nil) && home.Get().SlideshowTransitioning() {
		return home.SlideshowFrame
	}
	if s.showWeather || s.nowPlaying {
		return time.Until(now.Truncate(idleFrame).Add(idleFrame))
	}
	if (s.phase == "idle" || s.phase == "lingering") && !s.showVolume {
		// On the next whole second, so the clock changes when the second does.
		return time.Until(now.Truncate(idleFrame).Add(idleFrame))
	}
	return activeFrame
}
