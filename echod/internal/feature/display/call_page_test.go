//go:build !dot && !spot

package display

import (
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
)

// The call page's buttons take the taps over their own width. The margins either side of Hang up are
// not Hang up, the gap between Decline and Answer is neither of them, and above the band nothing is
// decided at all; inside a button, every point is that button, on every panel.
func TestACallsButtonsTakeOnlyTheirOwnTaps(t *testing.T) {
	for _, panel := range showPanels() {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
		decline, answer := r.actionHalves()
		mid := (decline.Min.Y + decline.Max.Y) / 2
		for _, c := range []struct {
			name         string
			at           image.Point
			ringing      bool
			hang, answer bool
		}{
			{"Hang up, its left end", image.Pt(decline.Min.X, mid), false, true, false},
			{"Hang up, its middle", image.Pt(panel.wide/2, mid), false, true, false},
			{"Hang up, its right end", image.Pt(answer.Max.X-1, mid), false, true, false},
			{"Hang up, its top edge", image.Pt(panel.wide/2, decline.Min.Y), false, true, false},
			{"the left margin", image.Pt(decline.Min.X-1, mid), false, false, false},
			{"the left edge of the screen", image.Pt(0, mid), false, false, false},
			{"the right margin", image.Pt(answer.Max.X, mid), false, false, false},
			{"the name, above the band", image.Pt(panel.wide/2, r.s(220)), false, false, false},
			{"Decline", image.Pt(decline.Min.X+decline.Dx()/2, mid), true, true, false},
			{"Decline, its right end", image.Pt(decline.Max.X-1, mid), true, true, false},
			{"Answer", image.Pt(answer.Min.X+answer.Dx()/2, mid), true, false, true},
			{"Answer, its left end", image.Pt(answer.Min.X, mid), true, false, true},
			{"between Decline and Answer", image.Pt(panel.wide/2, mid), true, false, false},
			{"the gap's left edge", image.Pt(decline.Max.X, mid), true, false, false},
			{"the gap's right edge", image.Pt(answer.Min.X-1, mid), true, false, false},
			{"the margin beside Answer", image.Pt(answer.Max.X, mid), true, false, false},
			{"the margin beside Decline", image.Pt(decline.Min.X-1, mid), true, false, false},
			{"the name while it rings", image.Pt(answer.Min.X+10, r.s(220)), true, false, false},
		} {
			hang, ans := r.callButtonAt(c.at, c.ringing)
			if hang != c.hang || ans != c.answer {
				t.Errorf("%s: %s at %v: hang up %v, answer %v; want %v, %v",
					panel.name, c.name, c.at, hang, ans, c.hang, c.answer)
			}
		}
	}
}

// A call with the microphone off says so on its own page, in the line the time is on, and not only in
// the header's pill: the line changes, and nothing under it does.
func TestACallSaysTheMicrophoneIsOff(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	for _, panel := range showPanels() {
		for name, call := range map[string]phone.State{
			"talking": {Phase: phone.Talking, Peer: "104", Since: at.Add(-90 * time.Second)},
			"calling": {Phase: phone.Dialing, Peer: "104"},
			"ringing": {Phase: phone.Ringing, Peer: "104"},
		} {
			heard := scene{now: at, phase: "idle", call: call}
			muted := heard
			muted.muted = true
			// The line's band in the Show 5's pixels: its baseline is at 290, under the name at 220.
			const top, bottom = 250, 300
			if same(band(heard, panel.wide, panel.high, top, bottom), band(muted, panel.wide, panel.high, top, bottom)) {
				t.Errorf("%s/%s: the microphone is off and the call's page does not say so", panel.name, name)
			}
			high := panel.high * showWide / panel.wide
			if !same(band(heard, panel.wide, panel.high, bottom, high), band(muted, panel.wide, panel.high, bottom, high)) {
				t.Errorf("%s/%s: the note that the microphone is off moved the buttons", panel.name, name)
			}
		}
	}
}
