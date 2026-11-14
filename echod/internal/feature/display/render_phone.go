//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"time"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
)

// The call page: over everything while a call rings, is placed or is up. A title, who it is with, how
// long it has lasted, and the buttons: Decline and Answer while ringing, Hang up otherwise.
// The call's answers sit where every other page's do; see actionBand.

// atDoorNote is what a call's page says when the doorbell is pressed during it.
const atDoorNote = "Someone's at the door"

func (r *renderer) callPage(s scene) {
	st := s.call
	title := tr("ui_calling_variant")
	switch {
	case st.Phase == phone.Ringing:
		title = tr("ui_incoming_call")
	case st.Phase == phone.Talking:
		title = tr("ui_on_a_call")
	}
	if st.DropIn {
		// Somebody connected without anyone here answering: the page says so, and whose device it is.
		title = tr("ui_drop_in")
	}
	r.text(r.title, title, (r.w-r.width(r.title, title))/2, r.s(80), amber)

	// The lines are placed to the panel's scale, as their faces are: on a Show 8, a name left where a
	// Show 5 has it ran into the ringing note above it.

	// Something ringing behind the call. It has to be said, because this page is drawn over the
	// ringing one and the noise would otherwise have nothing on screen to explain it — and it has to
	// say where to press, because the buttons below belong to the call.
	if s.ring.any() {
		what := tr("alarm")
		if s.ring.timer != "" && s.ring.alarm == nil {
			what = tr("timer")
		}
		note := fmt.Sprintf(tr("drawer_call_alarm"), what)
		r.text(r.small, note, (r.w-r.width(r.small, note))/2, r.s(130), dim)
	}

	who := st.Peer
	if who == "" {
		who = tr("ui_unknown")
	}
	// As big as fits: a name, or a number with its caller ID name, can be long.
	face := r.body
	for _, f := range []font.Face{r.big, r.title} {
		if r.width(f, who) <= r.w-2*r.margin {
			face = f
			break
		}
	}
	r.text(face, who, (r.w-r.width(face, who))/2, r.s(220), cream)

	// The doorbell, pressed during the call: said here instead of rung (feature/doorbell), in the
	// call time's place, which is free on every panel and in every phase of a call.
	line, lineColor := "", dim
	if s.atDoor {
		line, lineColor = tr("ui_someone_s_at_the_door"), amber
	} else if st.Phase == phone.Talking && !st.Since.IsZero() {
		d := s.now.Sub(st.Since).Round(time.Second)
		line = fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
	}
	// The microphone off, said first on the same line and in the accent: the header's pill is small
	// and in a corner, and somebody talking to a caller who cannot hear them is looking here.
	muted := ""
	if s.muted {
		muted = tr("ui_muted")
		if line != "" {
			muted += "  ·  "
		}
	}
	x := (r.w - r.width(r.body, muted) - r.width(r.body, line)) / 2
	r.text(r.body, muted, x, r.s(290), amber)
	r.text(r.body, line, x+r.width(r.body, muted), r.s(290), lineColor)

	decline, answer := r.actionHalves()
	rad := float64(r.s(actionRadius))
	mid := (decline.Min.Y + decline.Max.Y) / 2
	// A finger shows on the button its tap would be (callTap): the right half answers a call that is
	// ringing, the rest of the band declines or hangs up, and above the band is no answer. Not with
	// something ringing behind the call either, when a tap off the buttons' band stops that.
	white := color.RGBA{0xff, 0xff, 0xff, 0xff}
	held := r.press.on && r.actionDecided(r.press.at.Y)
	answering := held && st.Phase == phone.Ringing && r.press.at.X >= r.w/2
	if st.Phase == phone.Ringing {
		r.roundButton(decline, rad, r.pressTinted(held && !answering, declineRed, white))
		r.roundButton(answer, rad, r.pressTinted(answering, answerGreen, white))
		r.text(r.title, tr("ui_decline"), decline.Min.X+(decline.Dx()-r.width(r.title, tr("ui_decline")))/2, mid+r.s(16), color.White)
		r.text(r.title, tr("ui_answer"), answer.Min.X+(answer.Dx()-r.width(r.title, tr("ui_answer")))/2, mid+r.s(16), color.White)
		return
	}
	hang := image.Rect(decline.Min.X, decline.Min.Y, answer.Max.X, decline.Max.Y)
	r.roundButton(hang, rad, r.pressTinted(held, declineRed, white))
	r.text(r.title, tr("hang_up"), hang.Min.X+(hang.Dx()-r.width(r.title, tr("hang_up")))/2, mid+r.s(16), color.White)
}

// callButtonAt is what a finger at p on the call page is on: Decline or Hang up, Answer, or neither.
// A button takes the taps over its own width and no more: the margins either side are not Hang up,
// and while a call rings the gap between Decline and Answer is neither, since a finger that came down
// between two opposite answers has not given one. Up and down it is the band every page's answers
// share (actionDecided), so the buttons are no smaller to a finger than they were.
func (r *renderer) callButtonAt(p image.Point, ringing bool) (hang, answer bool) {
	if !r.actionDecided(p.Y) {
		return false, false
	}
	decline, accept := r.actionHalves()
	across := func(b image.Rectangle) bool { return p.X >= b.Min.X && p.X < b.Max.X }
	if !ringing {
		return across(decline.Union(accept)), false
	}
	return across(decline), across(accept)
}

// callTap is a finger on the call page: Decline or Hang up on its button, Answer on its own while
// the call rings (callButtonAt).
func (d *Display) callTap(x, y int, st phone.State) {
	if d.r == nil {
		return
	}
	switch hang, answer := d.r.callButtonAt(image.Pt(x, y), st.Phase == phone.Ringing); {
	case answer:
		phone.Get().Answer()
	case hang:
		phone.Get().Hangup()
	}
}
