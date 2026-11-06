//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"strings"
	"time"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
)

// The call over the page: a call being placed or up, drawn as a red Hang up button where the home
// screen's green Call button is, over whatever page is showing, rather than as a page of its own,
// when Call screen says so (callscreen.go). With details, a pill beside the button says who the call
// is with and how long it has lasted. Everything else is the page as it would be without the call:
// the clock, a dashboard, a notification's card.

// callBarStyle is how the frame draws the call over the page: not at all, the button, or the button
// with its details.
type callBarStyle int

const (
	callBarNone callBarStyle = iota
	callBarButton
	callBarDetails
)

// In the Show 5's pixels, as everything else in this package.
const (
	callBarGap   = 12 // between the button and the details, and between the things in them
	callBarPad   = 20 // inside the details, either end
	callBarReach = 12 // around the button, a tap still hangs up, as around the Call button
)

// callBarOn is how a call is drawn over the page, by the Call screen setting. Only a call being placed
// or up: a call ringing is answered or declined on its page, with both answers a finger can find
// without looking. And never a Drop In: somebody listening in who was not answered is said by a page.
func callBarOn(st phone.State) callBarStyle {
	if (st.Phase != phone.Dialing && st.Phase != phone.Talking) || st.DropIn {
		return callBarNone
	}
	return callBarStyle(callScreen.Load())
}

// callPageUp is whether the call's page has the screen. It has it for a call not drawn over the
// page, and for one that is while the page under it is asking for an answer at its foot, where the
// button would be: a ring, a browser or a video asking to be let in, the settings lock's PIN pad. The
// call's page already deals with each of those, as it always has.
func (s scene) callPageUp() bool {
	if s.call.Phase == phone.Idle {
		return false
	}
	return s.callBar == callBarNone || s.ring.any() || s.setupAsking || s.showVideoAsk || s.pin.open
}

// callOverPage is whether the frame draws the call over the page.
func (s scene) callOverPage() bool { return s.call.Phase != phone.Idle && !s.callPageUp() }

// setCallBarAt keeps where the call over the page and its Hang up were drawn, for a tap there. Empty
// when the frame drew none: what cannot be seen cannot be tapped.
func (r *renderer) setCallBarAt(all, hang image.Rectangle) {
	r.zmu.Lock()
	r.callBarAt, r.hangAt = all, hang
	r.zmu.Unlock()
}

// callBarTapped is what a tap at p does to the call over the page as last drawn: on it at all, and
// on Hang up. A tap on the details does nothing, rather than reaching the page under them.
func (r *renderer) callBarTapped(p image.Point) (on, hang bool) {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	if r.callBarAt.Empty() {
		return false, false
	}
	hang = p.In(r.hangAt.Inset(-r.s(callBarReach)))
	return hang || p.In(r.callBarAt), hang
}

// callBarDrawn is whether the frame last drawn had the call over the page.
func (r *renderer) callBarDrawn() bool {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	return !r.callBarAt.Empty()
}

// hangButtonDraw draws Hang up where the Call button is (callButtonRect), the same size and shape, red
// as the call page's Hang up is, with the handset put down.
func (r *renderer) hangButtonDraw() image.Rectangle {
	b := r.callButtonRect()
	rad := float64(b.Dx()) / 2
	r.roundShadow(b, rad, r.sf(10), r.s(3), shadowAlpha())
	r.roundFill(b, rad, shift(declineRed, 16), shift(declineRed, -12))
	r.roundHighlight(b, rad)
	icon := r.s(32)
	r.mdiIcon("phone-hangup", b.Min.X+(b.Dx()-icon)/2, b.Min.Y+(b.Dy()-icon)/2, 32, cream)
	return b
}

// callBarDraw draws the call over the page when the frame has it. Deferred before the header, so it is
// drawn over whatever the page put in its corner.
func (r *renderer) callBarDraw(s scene) {
	if !s.callOverPage() {
		return
	}
	hang := r.hangButtonDraw()
	all := hang
	if s.callBar == callBarDetails {
		if d := r.callDetailsDraw(s, hang); !d.Empty() {
			all = all.Union(d)
		}
	}
	r.callBarNext, r.hangNext = all, hang
}

// publishCallBar makes the finished frame's call over the page what a tap acts on.
func (r *renderer) publishCallBar() { r.setCallBarAt(r.callBarNext, r.hangNext) }

// keepCallBar is for a frame that keeps the page as it was drawn: the bar it showed is still there.
func (r *renderer) keepCallBar() {
	r.zmu.Lock()
	r.callBarNext, r.hangNext = r.callBarAt, r.hangAt
	r.zmu.Unlock()
}

// callDetailsDraw draws the pill beside Hang up: what the call is doing, who it is with, and how long
// it has lasted or that somebody is at the door, as the call's page says them.
func (r *renderer) callDetailsDraw(s scene, hang image.Rectangle) image.Rectangle {
	st := s.call
	what := tr("ui_calling_variant")
	if st.Phase == phone.Talking {
		what = tr("ui_on_a_call")
	}
	who := st.Peer
	if who == "" {
		who = tr("ui_unknown")
	}
	when, whenColor := "", dim
	switch {
	case s.atDoor:
		when, whenColor = tr("ui_someone_s_at_the_door"), amber
	case st.Phase == phone.Talking && !st.Since.IsZero():
		d := s.now.Sub(st.Since).Round(time.Second)
		when = fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
	}
	face := r.small
	pad, gap := r.s(callBarPad), r.s(callBarGap)
	x0 := hang.Max.X + gap
	room := r.w - r.margin - x0 - 2*pad
	if room < r.width(face, what) {
		return image.Rectangle{}
	}
	label, when := r.callBarLine(face, what, who, when, room, gap)
	inner := r.width(face, label)
	if when != "" {
		inner += gap + r.width(face, when)
	}
	pill := image.Rect(x0, hang.Min.Y, x0+inner+2*pad, hang.Max.Y)
	r.roundButton(pill, float64(pill.Dy())/2, walnut)
	base := pill.Min.Y + pill.Dy()/2 + r.s(12)
	x := pill.Min.X + pad
	r.text(face, label, x, base, cream)
	if when != "" {
		r.text(face, when, x+r.width(face, label)+gap, base, whenColor)
	}
	return pill
}

// callBarLine is what the details say in room: what the call is doing, who it is with, and when, which
// is the time or the doorbell. The name gives way first, then when. A name cut down to a letter or two
// says nothing, so one that keeps fewer than three of its letters goes whole.
func (r *renderer) callBarLine(face font.Face, what, who, when string, room, gap int) (string, string) {
	label := what + "  ·  " + who
	fit := func(room int) string {
		if r.width(face, label) <= room {
			return label
		}
		cut := clipText(r, face, label, room)
		if name, ok := strings.CutPrefix(strings.TrimSuffix(cut, ellipsis), what+"  ·  "); ok && len([]rune(strings.TrimSpace(name))) >= 3 {
			return cut
		}
		if r.width(face, what) <= room {
			return what
		}
		return clipText(r, face, what, room)
	}
	if when != "" {
		if left := room - gap - r.width(face, when); left >= r.width(face, what) {
			return fit(left), when
		}
	}
	return fit(room), ""
}
