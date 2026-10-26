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

// answerGreen and declineRed are the call buttons whatever the theme: every phone uses them, and a
// call is the one page where a wrong tap cannot be taken back.
var (
	answerGreen = color.RGBA{0x2e, 0xa0, 0x4f, 0xff}
	declineRed  = color.RGBA{0xc6, 0x3a, 0x32, 0xff}
)

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
	if s.atDoor {
		note := tr("ui_someone_s_at_the_door")
		r.text(r.body, note, (r.w-r.width(r.body, note))/2, r.s(290), amber)
	} else if st.Phase == phone.Talking && !st.Since.IsZero() {
		d := s.now.Sub(st.Since).Round(time.Second)
		line := fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
		r.text(r.body, line, (r.w-r.width(r.body, line))/2, r.s(290), dim)
	}

	decline, answer := r.actionHalves()
	rad := float64(r.s(actionRadius))
	mid := (decline.Min.Y + decline.Max.Y) / 2
	if st.Phase == phone.Ringing {
		r.roundButton(decline, rad, declineRed)
		r.roundButton(answer, rad, answerGreen)
		r.text(r.title, tr("ui_decline"), decline.Min.X+(decline.Dx()-r.width(r.title, tr("ui_decline")))/2, mid+r.s(16), color.White)
		r.text(r.title, tr("ui_answer"), answer.Min.X+(answer.Dx()-r.width(r.title, tr("ui_answer")))/2, mid+r.s(16), color.White)
		return
	}
	hang := image.Rect(decline.Min.X, decline.Min.Y, answer.Max.X, decline.Max.Y)
	r.roundButton(hang, rad, declineRed)
	r.text(r.title, tr("hang_up"), hang.Min.X+(hang.Dx()-r.width(r.title, tr("hang_up")))/2, mid+r.s(16), color.White)
}

// callTap is a finger on the call page: the left half of the buttons declines or hangs up, the right
// half answers a call that is ringing.
func (d *Display) callTap(x, y int, st phone.State) {
	if d.r == nil || !d.r.actionDecided(y) {
		return
	}
	if st.Phase == phone.Ringing && x >= d.r.w/2 {
		phone.Get().Answer()
		return
	}
	phone.Get().Hangup()
}
