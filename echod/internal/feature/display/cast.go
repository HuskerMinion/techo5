//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"net/url"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/cast"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// The cast page: what a phone is sending, over the whole screen. It takes the screen when a cast
// starts and gives it back when the phone stops or a swipe in from the left edge ends it. It waits
// behind whatever is more pressing (a ring, a call, a turn, the settings), as the dashboard does; the
// cast carries on underneath and is there when the screen comes back to it. The question that comes
// first, a phone waiting to be accepted, does not wait behind the settings: it takes them down, since
// nobody can answer a page they cannot see, and the phone's wait is 20 seconds.

// castAsked takes the settings down when a phone is waiting to be accepted, before the scene is
// decided: a sheet over the question would hide it and keep its buttons from being pressed.
func (d *Display) castAsked(s *scene) {
	if s.showSheet && cast.Get().Asking() != "" {
		d.closeSheet()
		s.showSheet = false
	}
}

// castLights lights a dark panel for a phone waiting to be accepted, as a video starting does: somebody
// at the device has to see the question, and the phone gives up after 20 seconds. Once per question, so
// a screen turned off while it is up stays off. At night it is at the night light's level and not the
// room lit up. Not under a closed cover (the caller has already left it).
func (d *Display) castLights(on, night bool) bool {
	if cast.Get().Asking() == "" {
		d.castAskLit = false
		return on
	}
	if d.castAskLit {
		return on
	}
	d.castAskLit = true
	if !on {
		if night && config.Get().Screen.NightLight {
			d.mu.Lock()
			d.nightGlow, d.nightDark = true, false
			d.mu.Unlock()
		}
		d.apply(true, d.ceilingOrDefault(), false)
		on = true
	}
	return on
}

// castLatch tells the tap's latch which question is on the screen, once the frame's scene is whole: only
// when it is really on top. What draw() puts over it (the setup page's or a DLNA video's question, the
// PIN pad, a QR scan, the camera test, an alert) leaves it undrawn and so unanswerable, and its settle
// must not start behind them, or it could be answered the moment the other page goes.
func (d *Display) castLatch(s *scene, id uint64, now time.Time) {
	drawn := uint64(0)
	if s.showCastAsk && !s.callPageUp() && !s.ring.any() && !s.pin.open && !s.setupAsking && !s.showVideoAsk &&
		!s.bt.Pairing && !s.showWifi && s.qrscan == nil && s.camtest == nil && !s.showAlert {
		drawn = id
	}
	d.cask.drawn(drawn, now)
}

// castScene decides whether the cast is the page.
func (d *Display) castScene(s *scene, sheetOrDrawer bool) {
	f := cast.Get()
	if d.r != nil {
		f.SetScreen(d.r.w, d.r.h)
	}
	want := (f.Active() || f.Asking() != "" || f.PairingShown()) && s.phase == "idle" && !sheetOrDrawer &&
		!s.showCamera && !s.showWeather && !s.showRadar && !s.showCalendar && !s.showWifi && !s.bt.Pairing
	s.showCast = want
	// The cast page, the question and the cast after it alike, is over the deck, a custom screen and a
	// video: they leave themselves out of their scenes while it is up, so a tap on Accept, or on the
	// cast, is not a press on a deck button under it, and a swipe from the left edge ends the cast.
	s.showCastAsk = want && f.Asking() != ""
	d.mu.Lock()
	d.castShowing = want
	d.mu.Unlock()
}

// castGesture is a finger on the cast page. It has nothing to press: a swipe in from the left edge
// ends the cast, and the rest is ignored rather than passed on to a page under it.
func (d *Display) castGesture(g touch.Gesture) {
	// The pairing code is up: a touch anywhere puts it away.
	if f := cast.Get(); f.PairingShown() {
		f.ShowPairing(false)
		return
	}
	// A phone waiting to be accepted: the left half of the buttons sends it away, the right half lets
	// it in. Nothing else on this page answers.
	// Only the question drawn, once it has been up askSettle (askLatch): a finger on its way to a
	// settings row, or to what was there before, must not answer a question that has just come up.
	if cast.Get().Asking() != "" {
		id, ok := d.cask.answerable(time.Now())
		if ok && id == cast.Get().AskID() && g.Kind == touch.Tap && d.r != nil && d.r.actionDecided(g.Y) {
			cast.Get().Decide(g.X >= d.r.w/2)
		}
		return
	}
	if g.Kind == touch.SwipeRight && d.r != nil && g.X < d.r.drawerEdge() {
		cast.Get().Stop()
	}
}

// castPage draws the newest frame, or says the cast is starting.
func (r *renderer) castPage(s scene) {
	if who := cast.Get().Asking(); who != "" {
		r.castAsk(who)
		return
	}
	if cast.Get().Draw(r.dst) {
		r.castTitle(cast.Get().Title())
		return
	}
	if cast.Get().PairingShown() {
		r.castPairPage(cast.Get().PairingLink())
		return
	}
	r.fillRect(r.dst.Rect, color.RGBA{A: 255})
	msg := tr("cast_casting")
	r.text(r.small, msg, (r.w-r.width(r.small, msg))/2, r.h/2, dim)
}

// castAsk is the question a phone's cast waits behind: who, and Decline or Accept where every page's
// answers are. The call's colors, for the same reason: a wrong tap here shows the room a stranger's
// screen.
func (r *renderer) castAsk(who string) {
	r.fillRect(r.dst.Rect, color.RGBA{A: 255})
	title := tr("cast_to_this_screen")
	r.text(r.title, title, (r.w-r.width(r.title, title))/2, r.s(80), amber)
	face := r.body
	for _, f := range []font.Face{r.big, r.title} {
		if r.width(f, who) <= r.w-2*r.margin {
			face = f
			break
		}
	}
	r.text(face, who, (r.w-r.width(face, who))/2, r.s(220), cream)
	note := tr("cast_wants_to_show")
	r.text(r.small, note, (r.w-r.width(r.small, note))/2, r.s(270), dim)

	decline, accept := r.actionHalves()
	rad := float64(r.s(actionRadius))
	mid := (decline.Min.Y + decline.Max.Y) / 2
	r.roundButton(decline, rad, declineRed)
	r.roundButton(accept, rad, answerGreen)
	no, yes := tr("ui_decline"), tr("ui_accept")
	r.text(r.title, no, decline.Min.X+(decline.Dx()-r.width(r.title, no))/2, mid+r.s(16), color.White)
	r.text(r.title, yes, accept.Min.X+(accept.Dx()-r.width(r.title, yes))/2, mid+r.s(16), color.White)
}

// castTitle says what is playing, on a dark band over the top of the picture, for the first moments.
func (r *renderer) castTitle(title string) {
	if title == "" {
		return
	}
	line := clipText(r, r.small, fmt.Sprintf(tr("cast_casting_s"), title), r.w-2*r.margin-r.s(32))
	w := r.width(r.small, line) + r.s(32)
	box := image.Rect((r.w-w)/2, r.s(14), (r.w+w)/2, r.s(14)+r.s(52))
	r.roundButton(box, float64(r.s(26)), color.RGBA{A: 185})
	r.text(r.small, line, box.Min.X+r.s(16), box.Min.Y+r.s(36), cream)
}

// pairBitmap is the code last made: the page redraws while it is up, and the same link gives the same
// modules, so it is made once.
var pairBitmap struct {
	sync.Mutex
	link string
	bits [][]bool
}

func pairBits(link string) ([][]bool, error) {
	pairBitmap.Lock()
	defer pairBitmap.Unlock()
	if pairBitmap.link == link && pairBitmap.bits != nil {
		return pairBitmap.bits, nil
	}
	q, err := qrcode.New(link, qrcode.Medium)
	if err != nil {
		return nil, err
	}
	pairBitmap.link, pairBitmap.bits = link, q.Bitmap()
	return pairBitmap.bits, nil
}

// castPairPage is the code a phone scans to pair, with the words for the phone's side and the key in
// plain letters in case the camera will not read it.
func (r *renderer) castPairPage(link string) {
	r.fillRect(r.dst.Rect, color.RGBA{A: 255})
	if link == "" {
		msg := tr("cast_no_address")
		r.text(r.body, msg, (r.w-r.width(r.body, msg))/2, r.h/2, dim)
		return
	}
	bits, err := pairBits(link)
	if err != nil {
		msg := tr("cast_no_code")
		r.text(r.body, msg, (r.w-r.width(r.body, msg))/2, r.h/2, dim)
		return
	}
	// The bitmap carries its own quiet zone, which is what a scanner needs around the modules.
	side := min(r.h-2*r.s(16), r.w/2)
	module := max(side/len(bits), 1)
	size := module * len(bits)
	origin := image.Pt(r.margin, (r.h-size)/2)
	r.fillRect(image.Rect(origin.X, origin.Y, origin.X+size, origin.Y+size), color.White)
	for y, row := range bits {
		for x, on := range row {
			if on {
				px := image.Pt(origin.X+x*module, origin.Y+y*module)
				r.fillRect(image.Rect(px.X, px.Y, px.X+module, px.Y+module), color.Black)
			}
		}
	}

	x := origin.X + size + r.s(28)
	room := r.w - x - r.margin
	y := r.s(90)
	r.text(r.title, tr("cast_pair_a_phone"), x, y, amber)
	y += r.s(58)
	for _, line := range r.wrap(r.small, tr("cast_pair_steps"), room) {
		r.text(r.small, line, x, y, cream)
		y += r.s(36)
	}
	if u, err := url.Parse(link); err == nil {
		y += r.s(16)
		r.text(r.small, tr("cast_or_type_it_in"), x, y, dim)
		y += r.s(36)
		r.text(r.small, u.Query().Get("host"), x, y, cream)
		y += r.s(34)
		r.text(r.small, u.Query().Get("key"), x, y, cream)
	}
	note := tr("cast_touch_to_close")
	r.text(r.tiny, note, x, r.h-r.s(24), dim)
}
