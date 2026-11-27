//go:build !dot

package display

import (
	"image"
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
	// shownPage and shownCard are what the add-ons were last told is in view; framePage and frameCard
	// what this frame shows, settled as it ends (addonSettle).
	shownPage, shownCard string
	framePage, frameCard string
}

// maxSceneLists is how many lists of a page keep a scroll of their own; past them a list is not
// scrolled.
const maxSceneLists = 8

// addonPageView is the add-on's page as a frame draws it.
type addonPageView struct {
	id, name string
	sc       hostScene
	has      bool // false: the add-on has set no page yet, and the page says it is waited for
	scrolls  [maxSceneLists]int
	images   sceneImages
	pics     scenePics
}

// addonCardView is the add-on's card over the clock as a frame draws it.
type addonCardView struct {
	id, name string
	sc       hostScene
	images   sceneImages
	pics     scenePics
}

// addonLaid is a layout and what it was made for.
type addonLaid struct {
	key addonLayKey
	lay *sceneLayout
}

type addonLayKey struct {
	id       string
	rev, img uint64
	room     image.Rectangle
	font     fontKey
	card     bool
}

// laid is the layout of sc in room, made again only when something it was made for changed.
func (r *paint) laid(kept *addonLaid, id string, sc hostScene, room image.Rectangle, card bool, imgs sceneImages) *sceneLayout {
	k := addonLayKey{id, sc.Rev, sc.Images, room, r.faceKey(), card}
	if kept.lay == nil || kept.key != k {
		kept.key, kept.lay = k, r.layoutScene(&sc.Root, room, card, imgs)
	}
	return kept.lay
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

// addonBar is the page's name bar: the add-on's mark and name, and the way out. It is drawn after the
// scene, over all of it, and nothing in a scene can move, cover or color it.
func (r *paint) addonBar(name string, bar, closeAt image.Rectangle, pal scenePalette) {
	r.roundFill(bar, 0, pal.surface(3), pal.surface(2))
	for x := bar.Min.X; x < bar.Max.X; x++ {
		r.blendAt(x, bar.Max.Y-1, pal.rules, 1)
	}
	m := r.sceneMetrics()
	f := sceneFace(r.faceKey(), m.text[2], true)
	rad := r.s(22)
	cx, cy := bar.Min.X+r.s(28)+rad, bar.Min.Y+bar.Dy()/2
	room := closeAt.Min.X - (cx + rad + r.s(16)) - r.s(16)
	if r.round {
		// On the round face the bar is a band across the top of the circle, its mark and name in the
		// middle of it, where the circle is wide enough; the way out is at the foot (addonPageRoomsSpot).
		rad = 18
		room = bar.Dx() - 2*rad - 12 - 2*100
		name := r.fit(f, name, room)
		w := 2*rad + 12 + r.width(f, name)
		cx, cy = (bar.Min.X+bar.Max.X-w)/2+rad, bar.Max.Y-rad-12
	}
	r.addonMark(name, cx, cy, rad, pal, sceneFace(r.faceKey(), m.text[0], true))
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

func (k addonSink) PageChanged(string) { k.d.wake() }
func (k addonSink) CardChanged(string) { k.d.wake() }

// Open is the add-on bringing its page up, which the server allows only in answer to somebody (a command,
// a tap). It waits behind whatever is more pressing, as the deck does; at night it lifts the night light,
// as a long press does, since somebody asked for it.
func (k addonSink) Open(id string) {
	d := k.d
	d.openAddon(id, addonName(id))
	d.mu.Lock()
	glow := d.nightGlow
	// Somebody asked for it: the night waits as long after this as after a touch.
	d.touchedAt = time.Now()
	d.mu.Unlock()
	if glow {
		go d.addonLiftNight()
	}
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

// closeAddon puts the page away, by the name bar's way out.
func (d *Display) closeAddon() {
	d.mu.Lock()
	d.addon.page = ""
	d.mu.Unlock()
	d.wake()
}

// addonFrameBegin starts a frame's account of what of the add-ons is in view; addonSettle tells each
// add-on what changed once the frame is done, whichever way it ended (a dark panel, first-run, a video
// draw nothing of them).
func (d *Display) addonFrameBegin() {
	d.mu.Lock()
	d.addon.framePage, d.addon.frameCard = "", ""
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
	if wasPage != page {
		if wasPage != "" {
			h.Shown(wasPage, addonapi.OnPage, false)
		}
		if page != "" {
			h.Shown(page, addonapi.OnPage, true)
		}
	}
	if wasCard != card {
		if wasCard != "" {
			h.Shown(wasCard, addonapi.OnCard, false)
		}
		if card != "" {
			h.Shown(card, addonapi.OnCard, true)
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
		best = &addonCardView{id: id, name: t.Name, sc: sc, pics: addonPics.picture,
			images: func(b *addonapi.Block) ([]byte, string, bool) { return h.Image(id, b) }}
	}
	return best
}

// addonPageNow is the add-on's page as a frame draws it, read from the host.
func (d *Display) addonPageNow(h screenHost, id, name string) *addonPageView {
	sc, has := h.Scene(id, addonapi.OnPage)
	d.mu.Lock()
	scrolls := d.addon.scrolls
	d.addon.framePage = id
	d.mu.Unlock()
	return &addonPageView{id: id, name: name, sc: sc, has: has, scrolls: scrolls,
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
// light.
func (p *paint) addonCardIn(v *addonCardView, box, inner image.Rectangle, drawn *addonDrawn, timers sceneTimers, night bool, buf **image.RGBA) {
	lay := p.laid(&drawn.card, v.id, v.sc, inner, true, v.images)
	m := p.sceneMetrics()
	label := sceneFace(p.faceKey(), m.text[0], true)
	box.Min.Y = max(box.Min.Y, box.Max.Y-(lay.height+2*m.pad+lineH(label)+m.gap[1]))
	off := image.Pt(0, box.Min.Y+m.pad+lineH(label)+m.gap[1]-inner.Min.Y)
	rad := float64(m.pad)
	paintCard := func() {
		pal := themePalette()
		p.roundShadow(box, rad, rad*0.8, p.s(3), shadowAlpha()*0.7)
		p.roundFill(box, rad, pal.surface(3), pal.surface(2))
		p.text(label, p.fit(label, upper(v.name), box.Dx()-2*m.pad), box.Min.X+m.pad, box.Min.Y+m.pad+label.Metrics().Ascent.Ceil(), pal.dim)
		// What it holds from just under its name: the layout was made for the tallest card, and is
		// moved down to where this one's content starts.
		p.drawScene(lay, nil, off, pal, timers, v.pics)
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

// addonCardHeight is how tall v's card is in box, laid out in inner.
func (p *paint) addonCardHeight(v *addonCardView, box, inner image.Rectangle, drawn *addonDrawn) int {
	lay := p.laid(&drawn.card, v.id, v.sc, inner, true, v.images)
	m := p.sceneMetrics()
	return min(box.Dy(), lay.height+2*m.pad+lineH(sceneFace(p.faceKey(), m.text[0], true))+m.gap[1])
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
		if id := at.lay.tapAt(pt, r.s(4), at.scrolls[:], image.Point{}); id != "" {
			h.Touch(at.id, addonapi.OnPage, id)
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
			if id := at.lay.tapAt(pt, r.s(4), nil, at.off); id != "" {
				h.Touch(at.id, addonapi.OnCard, id)
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
