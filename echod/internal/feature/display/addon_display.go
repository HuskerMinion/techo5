//go:build !dot

package display

import (
	"image"
	"image/color"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/addonhost"
	"github.com/HuskerMinion/techo5/echod/internal/feature/addons"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	"github.com/HuskerMinion/techo5/echod/internal/lib/keepawake"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// The add-on screen's part the Show and the Spot share (addon_screen.go, addon_screen_spot.go): what is
// kept between frames, what the host is told and when, and the name bar over a page.

// addonUI is the add-on screen between frames, under d.mu.
type addonUI struct {
	page    string    // the add-on whose page is up, "" for none
	name    string    // its name, for its name bar
	touched time.Time // when it was opened or last touched
	scrolls [maxSceneLists]int
	// scrollSet is the scene (serve.Scene.Set) the scrolls are of: a new scene.set starts its lists at
	// their tops, as its lists are others.
	scrollSet uint64
	// shownPage and shownCard are what the add-ons were last told is in view; framePage and frameCard
	// what this frame shows, settled as it ends (addonSettle). A card is the add-on's id and the scene
	// it was set as, so a card cleared and set again between two frames is said to be in view again.
	shownPage, shownCard addonShown
	framePage, frameCard addonShown
	// card is the card over the clock as last taken (addonCardNow), held to a change every
	// addonCardEvery, or addonCardNightEvery over the night clock.
	card addonCardKept
	// changed is what a tap made the add-ons' steppers and toggles, shown at once, ahead of the add-on,
	// until the add-on's scene says otherwise; changedGen goes up with every change to it.
	changed    map[addonChangedKey]addonChanged
	changedGen uint64
}

// addonChangedKey is one block of an add-on's page or card.
type addonChangedKey struct{ addon, on, block string }

// addonChanged is a stepper's number or a toggle's state a tap made it (value), what the add-on's scene
// said when the first such tap came (base), and the scene.set it was in: kept while the scene still says
// base, so the add-on's own patch (to agree, or to set it otherwise) takes over, and a new scene drops it.
type addonChanged struct {
	base, value any
	set         uint64
}

// changeValue is a stepper's number or a toggle's state, as a change event carries it.
func changeValue(b *addonapi.Block) any {
	if b.Type == addonapi.BlockToggle {
		return b.On
	}
	if b.Value != nil {
		return *b.Value
	}
	return nil
}

// setChangeValue gives a stepper a number or a toggle a state.
func setChangeValue(b *addonapi.Block, v any) {
	switch x := v.(type) {
	case bool:
		b.On = x
	case float64:
		b.Value = &x
	}
}

// addonChangedIn puts into sc what taps made the add-on's steppers and toggles on that surface, dropping
// what the scene has moved on from, and answers the generation for the layout. A copy of the scene's root
// is changed, never the server's.
func (d *Display) addonChangedIn(id, on string, sc *hostScene) uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	var mine map[string]any
	for k, c := range d.addon.changed {
		if k.addon != id || k.on != on {
			continue
		}
		b, ok := sc.Root.Find(k.block)
		if !ok || !addonapi.Changes(b.Type) || sc.Set != c.set || changeValue(b) != c.base {
			delete(d.addon.changed, k)
			d.addon.changedGen++
			continue
		}
		if mine == nil {
			mine = map[string]any{}
		}
		mine[k.block] = c.value
	}
	if len(mine) > 0 {
		root := sc.Root.Clone()
		for block, v := range mine {
			if b, ok := root.Find(block); ok {
				setChangeValue(b, v)
			}
		}
		sc.Root = root
	}
	return d.addon.changedGen
}

// addonChange is a tap on one of the add-on's steppers or toggles, found in lay: the number it makes the
// stepper (step −1 or 1) or the state it makes the toggle, shown at once and sent to the add-on, which
// may set it otherwise. A − at the min or a + at the max does nothing.
func (d *Display) addonChange(h screenHost, id, on string, lay *sceneLayout, b *addonapi.Block, step int) {
	var v any
	switch b.Type {
	case addonapi.BlockStepper:
		if step == 0 || b.Value == nil {
			return
		}
		n := b.Stepped(step)
		if n == *b.Value {
			return
		}
		v = n
	case addonapi.BlockToggle:
		v = !b.On
	default:
		return
	}
	if !h.Change(id, on, b.ID, lay.rev, v) {
		return
	}
	sc, ok := h.Scene(id, on)
	if !ok {
		return
	}
	orig, ok := sc.Root.Find(b.ID)
	if !ok {
		return
	}
	k := addonChangedKey{id, on, b.ID}
	d.mu.Lock()
	if d.addon.changed == nil {
		d.addon.changed = map[addonChangedKey]addonChanged{}
	}
	base := changeValue(orig)
	if c, ok := d.addon.changed[k]; ok && c.set == sc.Set {
		base = c.base
	}
	d.addon.changed[k] = addonChanged{base: base, value: v, set: sc.Set}
	d.addon.changedGen++
	d.mu.Unlock()
	d.wake()
}

// addonShown is an add-on's page or card in view, and the scene.set it shows.
type addonShown struct {
	id  string
	set uint64
}

// addonCardKept is the card a frame last drew, when it was taken, how often it may change, and the
// frame asked for when it may change next.
type addonCardKept struct {
	view  *addonCardView
	at    time.Time
	newAt time.Time // when a new scene.set was last drawn at once
	every time.Duration
	timer *time.Timer
}

// addonCardUnpainted leaves the card out of a frame that still makes room for it: for a test to see what
// the clock draws where the card goes (nothing).
var addonCardUnpainted = false

// addonCardEvery and addonCardNightEvery are how often the card over the clock may be drawn anew, at
// most, by day and over the night clock: an add-on patching its card ten times a second (a changing
// picture, say) would keep a core drawing the clock all night. The page, which somebody is looking at,
// keeps the server's ten a second.
const (
	addonCardEvery      = time.Second
	addonCardNightEvery = 5 * time.Second
)

// maxSceneLists is how many lists of a page keep a scroll of their own; past them a list is not
// scrolled.
const maxSceneLists = 8

// addonPageView is the add-on's page as a frame draws it.
type addonPageView struct {
	id, name string
	icon     string // its mark, from its catalog entry; "" for its initials
	sc       hostScene
	has      bool   // false: the add-on has set no page yet, and the page says it is waited for
	local    uint64 // the generation of the steppers' and toggles' values shown ahead of the add-on, in sc
	scrolls  [maxSceneLists]int
	images   sceneImages
	pics     scenePics
}

// addonCardView is the add-on's card over the clock as a frame draws it.
type addonCardView struct {
	id, name string
	icon     string // its mark, from its catalog entry; "" for none
	sc       hostScene
	local    uint64 // as addonPageView's
	images   sceneImages
	pics     scenePics
}

// addonLaid is a layout and what it was made for.
type addonLaid struct {
	key addonLayKey
	lay *sceneLayout
}

type addonLayKey struct {
	id              string
	rev, img, local uint64
	room            image.Rectangle
	font            fontKey
	card            bool
	night           bool
}

// laid is the layout of sc in room, made again only when something it was made for changed.
func (r *paint) laid(kept *addonLaid, id string, sc hostScene, local uint64, room image.Rectangle, card bool, imgs sceneImages) *sceneLayout {
	return r.laidAt(kept, id, sc, local, room, card, false, imgs)
}

// laidAt is laid for a card over the night clock as well (night): laid out without what can be
// pressed (withoutPressable), as nothing on it takes a finger.
//
// local is the generation of the numbers the screen shows for its steppers ahead of the add-on
// (addonUI.steppers), which sc has in it already: a tap's new number is laid out at once.
func (r *paint) laidAt(kept *addonLaid, id string, sc hostScene, local uint64, room image.Rectangle, card, night bool, imgs sceneImages) *sceneLayout {
	k := addonLayKey{id, sc.Rev, sc.Images, local, room, r.faceKey(), card, night}
	if kept.lay == nil || kept.key != k {
		root := sc.Root
		if night {
			root = withoutPressable(root)
		}
		kept.key, kept.lay = k, r.layoutScene(&root, room, card, imgs)
		// A tap found in this layout is a tap on this revision of the scene (serve.Server.Touch).
		kept.lay.rev = sc.Rev
	}
	return kept.lay
}

// withoutPressable is b with every block that can be pressed taken out (buttons, chips, and a list's
// items with an id), and every row, column or card that held only those: what a card over the night
// clock shows. It takes no finger, and a button that does nothing when pressed is one the device's own
// screens never show. The scene is the add-on's, kept by the server: this is a copy.
func withoutPressable(b addonapi.Block) addonapi.Block {
	if len(b.Children) > 0 {
		kids := make([]addonapi.Block, 0, len(b.Children))
		for _, c := range b.Children {
			if addonapi.Pressable(c.Type) {
				continue
			}
			c = withoutPressable(c)
			if emptied(c) {
				continue
			}
			kids = append(kids, c)
		}
		b.Children = kids
	}
	if len(b.Sections) > 0 {
		secs := make([]addonapi.Section, 0, len(b.Sections))
		for _, sec := range b.Sections {
			items := make([]addonapi.Block, 0, len(sec.Items))
			for _, it := range sec.Items {
				if it.ID != "" || addonapi.Pressable(it.Type) {
					continue
				}
				if it = withoutPressable(it); !emptied(it) {
					items = append(items, it)
				}
			}
			sec.Items = items
			secs = append(secs, sec)
		}
		b.Sections = secs
	}
	if addonapi.Pressable(b.Type) {
		return addonapi.Block{Type: addonapi.BlockColumn}
	}
	return b
}

// emptied is a row, a column or a card left with nothing in it once what can be pressed is taken out.
func emptied(b addonapi.Block) bool {
	switch b.Type {
	case addonapi.BlockRow, addonapi.BlockColumn, addonapi.BlockCard:
		for _, c := range b.Children {
			if c.Type != addonapi.BlockSpacer && c.Type != addonapi.BlockDivider {
				return false
			}
		}
		return true
	}
	return false
}

// addonBarH is the name bar's height on the Show, before scaling.
const addonBarH = 76

// addonPageRooms is where a page's parts go: its name bar, its way out, and its content. On the Show the
// bar runs along the top with the way out at its end; on the round face the bar is a band across the
// top of the circle, the way out a round button at its foot, and the content inside the circle.
func (p *paint) addonPageRooms() (bar, close, content image.Rectangle) {
	if p.round {
		c := p.w / 2
		return image.Rect(0, 0, p.w, 96), image.Rect(c-26, p.h-62, c+26, p.h-10), image.Rect(70, 106, p.w-70, p.h-74)
	}
	bar = image.Rect(0, 0, p.w, p.s(addonBarH))
	side := p.s(52)
	close = image.Rect(p.w-p.s(24)-side, (bar.Dy()-side)/2, p.w-p.s(24), (bar.Dy()+side)/2)
	content = image.Rect(p.s(28), bar.Max.Y+p.s(18), p.w-p.s(28), p.h-p.s(18))
	return bar, close, content
}

// addonBar is the page's name bar: the add-on's mark (the icon its catalog entry gives it, or its
// initials) and name, and the way out. It is drawn after the scene, over all of it, and nothing in a
// scene can move, cover or color it.
//
// end, when not 0, is where the name must end before, short of the way out: a pill of the device's own
// (the microphone off) drawn in the bar beside it (renderer.headerAt), which the name keeps clear of, so
// both stay readable.
func (r *paint) addonBar(name, icon string, bar, closeAt image.Rectangle, pal scenePalette, end int) {
	r.roundFill(bar, 0, pal.surface(3), pal.surface(2))
	for x := bar.Min.X; x < bar.Max.X; x++ {
		r.blendAt(x, bar.Max.Y-1, pal.rules, 1)
	}
	m := r.sceneMetrics()
	f := sceneFace(r.faceKey(), m.text[2], true)
	rad := r.s(22)
	cx, cy := bar.Min.X+r.s(28)+rad, bar.Min.Y+bar.Dy()/2
	room := closeAt.Min.X - (cx + rad + r.s(16)) - r.s(16)
	if end > 0 {
		room = min(room, end-(cx+rad+r.s(16))-r.s(16))
	}
	if r.round {
		// On the round face the bar is a band across the top of the circle, its mark and name in the
		// middle of it, where the circle is wide enough; the way out is at the foot (addonPageRoomsSpot).
		rad = 18
		room = bar.Dx() - 2*rad - 12 - 2*100
		name := r.fit(f, name, room)
		w := 2*rad + 12 + r.width(f, name)
		cx, cy = (bar.Min.X+bar.Max.X-w)/2+rad, bar.Max.Y-rad-12
	}
	r.addonMark(name, icon, cx, cy, rad, pal, sceneFace(r.faceKey(), m.text[0], true))
	x := cx + rad + r.s(16)
	if r.round {
		x = cx + rad + 12
	}
	r.text(f, r.fit(f, name, room), x, cy+(f.Metrics().Ascent.Ceil()-f.Metrics().Descent.Ceil())/2, pal.text)
	crad := float64(closeAt.Dy()) / 2
	r.roundFill(closeAt, crad, pal.surface(7), pal.surface(5))
	ccx, ccy := float64(closeAt.Min.X)+float64(closeAt.Dx())/2, float64(closeAt.Min.Y)+float64(closeAt.Dy())/2
	arm, pen := r.sf(8), r.sf(28)/10
	r.aaLine(ccx-arm, ccy-arm, ccx+arm, ccy+arm, pen, pal.text)
	r.aaLine(ccx-arm, ccy+arm, ccx+arm, ccy-arm, pen, pal.text)
}

// timersIn answers the add-ons' timers from the frame's list of timers.
func timersIn(list []timer.Countdown) sceneTimers {
	return func(id string) (time.Duration, time.Duration, bool) {
		for _, t := range list {
			if t.ID == id {
				return t.Left, t.Total, true
			}
		}
		return 0, 0, false
	}
}

// The display's side: what it is told, what a frame shows, and what a finger does.

// addonSink is the display as the add-on host's screen (addonhost.ScreenSink). Each call only notes what
// it was told and wakes the frame, which reads the rest.
type addonSink struct{ d *Display }

// PageChanged draws again when the page is the one up (or waited for); another's page is not drawn.
func (k addonSink) PageChanged(id string) {
	k.d.mu.Lock()
	up := k.d.addon.page == id
	k.d.mu.Unlock()
	if up {
		k.d.wake()
	}
}

// CardChanged draws again when the card may change now, and otherwise when it may (addonCardNow): told
// ten times a second, the clock is drawn once a second for the card, once in five over the night clock.
func (k addonSink) CardChanged(string) {
	d := k.d
	d.mu.Lock()
	c := &d.addon.card
	wait := c.every - time.Since(c.at)
	if wait > 0 {
		d.addonCardLaterLocked(wait)
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()
	d.wake()
}

// Dropped lets go of the add-on's pictures, decoded for its page and card, as everything else of its is
// gone.
func (k addonSink) Dropped(id string) {
	addonPics.drop(id)
	k.d.wake()
}

// addonCardLaterLocked has a frame drawn after wait, when the card may change: one at a time. d.mu is
// held.
func (d *Display) addonCardLaterLocked(wait time.Duration) {
	c := &d.addon.card
	if c.timer != nil {
		return
	}
	c.timer = time.AfterFunc(wait, func() {
		d.mu.Lock()
		d.addon.card.timer = nil
		d.mu.Unlock()
		d.wake()
	})
}

// Open is the add-on bringing its page up, which the server allows only in answer to somebody (a command,
// a tap). It waits behind whatever is more pressing, as the deck does; at night it lifts the night light,
// as a long press does, since somebody asked for it, and a screen the night put out (Screen at night set
// to off) is turned on for it, as a tap on the dark screen turns it on. Either way the night takes the
// screen back by its own rules: it was touched now, and once the room has been quiet as long after this
// as after a touch, with the page closed or left, the night light or the dark comes back.
func (k addonSink) Open(id string) {
	d := k.d
	d.openAddon(id, addonName(id))
	d.mu.Lock()
	glow := d.nightGlow
	dark := d.nightDark && !d.on
	// Somebody asked for it: the night waits as long after this as after a touch.
	d.touchedAt = time.Now()
	d.mu.Unlock()
	switch {
	case dark:
		go d.addonLightNight()
	case glow:
		go d.addonLiftNight()
	}
}

// addonLightNight turns on a screen the night put out, for a page somebody asked for: as a tap on the dark
// screen does (gesture), but without saving the screen as on, which a tap does as somebody choosing it,
// so nothing is written and the night's dark is not forgotten for good. The night is no longer what
// holds it dark, so the night puts it out again by its own rule (night) once the room is quiet.
func (d *Display) addonLightNight() {
	d.mu.Lock()
	if !d.nightDark || d.on {
		d.mu.Unlock()
		return
	}
	d.nightDark = false
	d.mu.Unlock()
	slog.Info("screen: lit at night for an add-on's page somebody asked for")
	d.apply(true, d.ceilingOrDefault(), false)
}

func (k addonSink) Close(id string) {
	d := k.d
	d.mu.Lock()
	up := d.addon.page == id
	if up {
		d.addon.page = ""
	}
	d.mu.Unlock()
	if up {
		d.wake()
	}
}

// closeAddon puts the page away, by the name bar's way out: the add-on may not bring it straight back
// on what an earlier tap allowed.
func (d *Display) closeAddon() {
	d.mu.Lock()
	id := d.addon.page
	d.addon.page = ""
	d.mu.Unlock()
	if h := addonHost(); h != nil && id != "" {
		h.Left(id)
	}
	d.wake()
}

// addonFrameBegin starts a frame's account of what of the add-ons is in view; addonSettle tells each
// add-on what changed once the frame is done, whichever way it ended (a dark panel, first-run, a video
// draw nothing of them).
func (d *Display) addonFrameBegin() {
	d.mu.Lock()
	d.addon.framePage, d.addon.frameCard = addonShown{}, addonShown{}
	d.mu.Unlock()
}

func (d *Display) addonSettle() {
	h := addonHost()
	if h == nil {
		return
	}
	d.mu.Lock()
	page, card := d.addon.framePage, d.addon.frameCard
	wasPage, wasCard := d.addon.shownPage, d.addon.shownCard
	d.addon.shownPage, d.addon.shownCard = page, card
	d.mu.Unlock()
	if wasPage.id != page.id {
		if wasPage.id != "" {
			h.Shown(wasPage.id, addonapi.OnPage, false)
		}
		if page.id != "" {
			h.Shown(page.id, addonapi.OnPage, true)
		}
	}
	if wasCard != card {
		if wasCard.id != "" {
			h.Shown(wasCard.id, addonapi.OnCard, false)
		}
		if card.id != "" {
			h.Shown(card.id, addonapi.OnCard, true)
		}
	}
}

// addonStart gives the add-on host the screen, as the display opens: until then, and on a device where
// add-ons do not run, an add-on's screen ops are refused.
func (d *Display) addonStart() {
	if !addons.Here {
		return
	}
	addonPics.mu.Lock()
	addonPics.changed = d.wake
	addonPics.mu.Unlock()
	addonhost.Get().SetScreen(addonSink{d})
}

// addonCardNow is the card a frame draws over the clock (night: the night clock): the newest
// (addonNewestCard), but changed at most every addonCardEvery (addonCardNightEvery at night). A change
// that comes sooner waits, and a frame is drawn for it when it may; a card that went goes at once.
func (d *Display) addonCardNow(h screenHost, night bool) *addonCardView {
	every := addonCardEvery
	if night {
		every = addonCardNightEvery
	}
	best := addonNewestCard(h)
	var prevGone bool
	d.mu.Lock()
	prev := d.addon.card.view
	d.mu.Unlock()
	if prev != nil {
		// The card held is still there to hold: one that went is not drawn a moment longer.
		_, has := h.Scene(prev.id, addonapi.OnCard)
		prevGone = !has
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	c := &d.addon.card
	c.every = every
	now := time.Now()
	switch {
	case best == nil:
		c.view = nil
		return nil
	case prev != nil && prev.id == best.id && prev.sc.Rev == best.sc.Rev && prev.sc.Images == best.sc.Images:
		return best // nothing changed
	case prev != nil && prev.id == best.id && prev.sc.Set != best.sc.Set && now.Sub(c.newAt) >= every:
		// A new scene.set (after card.clear, say), whose taps the server takes once it is drawn: shown
		// at once, as the card held would take none. At most once an interval, so an add-on setting its
		// card over and over is drawn no more than twice as often as one patching it.
		c.newAt = now
	case prev != nil && !prevGone && now.Sub(c.at) < every:
		d.addonCardLaterLocked(every - now.Sub(c.at))
		return prev
	}
	c.view, c.at = best, now
	return best
}

// addonNewestCard is the newest of the running add-ons' cards: of several, the one set last shows, and
// the others keep their place behind it.
func addonNewestCard(h screenHost) *addonCardView {
	var best *addonCardView
	for _, t := range addonTiles() {
		sc, ok := h.Scene(t.ID, addonapi.OnCard)
		if !ok || (best != nil && !sc.At.After(best.sc.At)) {
			continue
		}
		id := t.ID
		best = &addonCardView{id: id, name: t.Name, icon: t.Icon, sc: sc, pics: addonPics.picture,
			images: func(b *addonapi.Block) ([]byte, string, bool) { return h.Image(id, b) }}
	}
	return best
}

// addonPageNow is the add-on's page as a frame draws it, read from the host.
func (d *Display) addonPageNow(h screenHost, id, name string) *addonPageView {
	sc, has := h.Scene(id, addonapi.OnPage)
	d.mu.Lock()
	if has && sc.Set != d.addon.scrollSet {
		// A new scene: its lists are others, and start at their tops.
		d.addon.scrolls, d.addon.scrollSet = [maxSceneLists]int{}, sc.Set
	}
	scrolls := d.addon.scrolls
	d.addon.framePage = addonShown{id: id}
	d.mu.Unlock()
	local := d.addonChangedIn(id, addonapi.OnPage, &sc)
	return &addonPageView{id: id, name: name, icon: addonIcon(id), sc: sc, has: has, scrolls: scrolls, local: local,
		images: func(b *addonapi.Block) ([]byte, string, bool) { return h.Image(id, b) }, pics: addonPics.picture}
}

// addonPageGone closes the page of an add-on that stopped, or that was left untouched as long as a
// dashboard opened by hand is (unless something holds the screen awake), and says whether it did.
func (d *Display) addonPageGone(id string, touched time.Time) bool {
	after, forgets := config.Get().Dashboard.Return()
	if addonRunning(id) && (!forgets || time.Since(touched) <= after || keepawake.Held()) {
		return false
	}
	d.mu.Lock()
	if d.addon.page == id {
		d.addon.page = ""
	}
	d.mu.Unlock()
	return true
}

// addonDrawn is what the renderer keeps of the add-on screen between frames: the layouts, made again
// only when their scene, their room or the fonts change, and where the page's and the card's parts were
// last drawn, for the touch goroutine, under zmu.
type addonDrawn struct {
	page, card addonLaid

	pageAt struct {
		id      string
		lay     *sceneLayout
		scrolls [maxSceneLists]int
		close   image.Rectangle
	}
	cardAt struct {
		id  string
		lay *sceneLayout
		box image.Rectangle
		off image.Point
	}
}

// addonForget forgets where the page or the card was drawn, for a frame that draws it no more.
func (p *paint) addonForget(drawn *addonDrawn, page, card bool) {
	p.zmu.Lock()
	defer p.zmu.Unlock()
	if page {
		drawn.pageAt.id, drawn.pageAt.lay = "", nil
	}
	if card {
		drawn.cardAt.id, drawn.cardAt.lay = "", nil
	}
}

// addonCardIn draws an add-on's card in box, along its foot: its name, then what it holds, laid out in
// inner (the room under the name in the tallest card box allows), the card no taller than that needs.
// At night it is drawn in the night clock's tone, and takes no finger: nobody aims at a card on a night
// light. So it shows nothing that looks pressable then (withoutPressable), and its name, which says
// whose card it is, in the brightest the night allows, the clock's own red: the theme's dim, toned,
// was too dark to read across a room.
func (p *paint) addonCardIn(v *addonCardView, box, inner image.Rectangle, drawn *addonDrawn, timers sceneTimers, night bool, buf **image.RGBA) {
	lay := p.laidAt(&drawn.card, v.id, v.sc, v.local, inner, true, night, v.images)
	m := p.sceneMetrics()
	label := sceneFace(p.faceKey(), m.text[0], true)
	box.Min.Y = max(box.Min.Y, box.Max.Y-(lay.height+2*m.pad+lineH(label)+m.gap[1]))
	off := image.Pt(0, box.Min.Y+m.pad+lineH(label)+m.gap[1]-inner.Min.Y)
	rad := float64(m.pad)
	paintCard := func() {
		pal := themePalette()
		p.roundShadow(box, rad, rad*0.8, p.s(3), shadowAlpha()*0.7)
		p.roundFill(box, rad, pal.surface(3), pal.surface(2))
		nameInk := pal.dim
		if night {
			nameInk = color.RGBA{255, 255, 255, 255} // toned, the night clock's red itself
		}
		x, base := box.Min.X+m.pad, box.Min.Y+m.pad+label.Metrics().Ascent.Ceil()
		room := box.Dx() - 2*m.pad
		if addonHasIcon(v.icon) {
			// The reviewer's icon before the name, the size of its capitals.
			side := lineH(label)
			p.mdiIconIn(v.icon, image.Rect(x, box.Min.Y+m.pad, x+side, box.Min.Y+m.pad+side), p.unscaled(side), nameInk)
			x += side + m.gap[1]
			room -= side + m.gap[1]
		}
		p.text(label, p.fit(label, upper(v.name), room), x, base, nameInk)
		// What it holds from just under its name: the layout was made for the tallest card, and is
		// moved down to where this one's content starts, and drawn inside the card's box only, so
		// nothing of it (a stack of accents, a line past the layout's room) reaches past the card.
		screen := p.dst
		in := box.Intersect(screen.Rect)
		if in.Empty() {
			return
		}
		p.dst = screen.SubImage(in).(*image.RGBA)
		p.drawScene(lay, nil, off, pal, timers, v.pics)
		p.dst = screen
	}
	if night {
		p.toned(buf, box.Inset(-p.s(20)), paintCard)
	} else {
		paintCard()
	}
	p.zmu.Lock()
	drawn.cardAt.id, drawn.cardAt.lay, drawn.cardAt.box, drawn.cardAt.off = v.id, lay, box, off
	if night {
		drawn.cardAt.id = ""
	}
	p.zmu.Unlock()
}

// toned paints with paint onto a black layer over b and takes what it drew into the night's tone onto
// the screen, as the night clock's own cards are drawn.
func (p *paint) toned(buf **image.RGBA, b image.Rectangle, paint func()) {
	b = b.Intersect(p.dst.Rect)
	if b.Empty() {
		return
	}
	layer := nightLayerFor(buf, b)
	screen := p.dst
	p.dst = layer
	paint()
	p.dst = screen
	nightToneOnto(screen, layer, b)
}

// addonPageShowing is whether the last frame drew an add-on's page: every finger is its then.
func (d *Display) addonPageShowing() bool {
	if d.r == nil {
		return false
	}
	d.r.zmu.Lock()
	defer d.r.zmu.Unlock()
	return d.r.addon.pageAt.id != ""
}

// addonGesture is a finger on the add-on's page: the name bar's way out leaves; a tap on one of its
// buttons, chips or list items goes to the add-on; a swipe up or down scrolls the list under it; a swipe
// in from the left edge is the back gesture, and another left or right swipe the add-on's.
func (d *Display) addonGesture(g touch.Gesture) {
	h := addonHost()
	r := d.r
	if h == nil || r == nil {
		return
	}
	r.zmu.Lock()
	at := r.addon.pageAt
	r.zmu.Unlock()
	if at.id == "" {
		return
	}
	pt := image.Pt(g.X, g.Y)
	d.mu.Lock()
	d.addon.touched = time.Now()
	d.mu.Unlock()
	switch g.Kind {
	case touch.Tap:
		if pt.In(at.close.Inset(-r.s(zoneSlop))) {
			d.closeAddon()
			return
		}
		if at.lay == nil {
			return
		}
		id, step, b := at.lay.tapStepAt(pt, r.s(4), at.scrolls[:], image.Point{})
		switch {
		case b != nil && addonapi.Changes(b.Type):
			d.addonChange(h, at.id, addonapi.OnPage, at.lay, b, step)
		case id != "":
			h.Touch(at.id, addonapi.OnPage, id, at.lay.rev)
		}
	case touch.SwipeUp, touch.SwipeDown:
		if at.lay == nil {
			return
		}
		i := at.lay.listAt(pt, image.Point{})
		if i < 0 || i >= maxSceneLists {
			return
		}
		by := r.s(notchPx)
		if g.Kind == touch.SwipeDown {
			by = -by
		}
		d.mu.Lock()
		d.addon.scrolls[i] = min(max(d.addon.scrolls[i]+by, 0), at.lay.maxScroll(i))
		d.mu.Unlock()
	case touch.SwipeRight:
		if g.X < d.addonBackEdge() {
			h.Back(at.id)
			return
		}
		h.Swipe(at.id, addonapi.SwipeRight)
	case touch.SwipeLeft:
		h.Swipe(at.id, addonapi.SwipeLeft)
	}
}

// addonCardGesture is a finger on the card over the clock, as last drawn: a tap on one of its chips goes
// to the add-on, a tap anywhere else on it opens the add-on's page, and a swipe across it puts it away.
// It says whether the finger was the card's.
func (d *Display) addonCardGesture(g touch.Gesture) bool {
	h := addonHost()
	r := d.r
	if h == nil || r == nil {
		return false
	}
	r.zmu.Lock()
	at := r.addon.cardAt
	r.zmu.Unlock()
	pt := image.Pt(g.X, g.Y)
	if at.id == "" || !pt.In(at.box) {
		return false
	}
	switch g.Kind {
	case touch.Tap:
		if at.lay != nil {
			if id, step, b := at.lay.tapStepAt(pt, r.s(4), nil, at.off); id != "" {
				if b != nil && addonapi.Changes(b.Type) {
					d.addonChange(h, at.id, addonapi.OnCard, at.lay, b, step)
				} else {
					h.Touch(at.id, addonapi.OnCard, id, at.lay.rev)
				}
				d.wake()
				return true
			}
		}
		h.Asked(at.id)
		d.openAddon(at.id, addonName(at.id))
	case touch.SwipeLeft, touch.SwipeRight:
		h.DismissCard(at.id)
		d.wake()
	default:
		return false
	}
	return true
}
