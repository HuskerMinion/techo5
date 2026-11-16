//go:build !dot && !spot

package display

import (
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
)

// band returns the pixels between two scaled y values, to compare one drawing against another.
func band(s scene, wide, high, top, bottom int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, wide, high))
	r := newRenderer(img)
	r.draw(s)

	out := make([]byte, 0, wide*(r.s(bottom)-r.s(top))*4)
	for y := r.s(top); y < r.s(bottom); y++ {
		for x := range wide {
			p := img.RGBAAt(x, y)
			out = append(out, p.R, p.G, p.B, p.A)
		}
	}
	return out
}

// The band a silenced ring's note is drawn in, in the Show 5's pixels: under the clock's digits,
// whose baseline is ringClockBase, and above the buttons, which begin at 330.
const ringNoteTop, ringNoteBottom = ringClockBase + 6, 330

// A silenced ring says so, in the one band of the ringing page that is clear.
//
// A ring a button quieted looks exactly like one that stopped, and it is not stopped — it waits to
// be answered. The note goes between the clock's digits and the buttons. It used to sit between the
// title and the clock, where it touched the tops of the digits on a Show 5; before that it was a
// small-face line that went straight through them. This pins the band as empty when there is nothing
// to say and used when there is, and everything above and below it as drawn the same either way.
func TestASilencedRingSaysSoWithoutHittingTheClock(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)

	for _, panel := range showPanels() {
		// On a panel taller than the Show 5's shape the words sit lower, by half of what it has over
		// (tallerBy), and the buttons lower by all of it, here in the Show 5's pixels as the bands are.
		taller := max(panel.high*showWide/panel.wide-showHigh, 0)
		down := taller / 2
		noteTop, noteBottom := ringNoteTop+down, ringNoteBottom+taller
		// The title and the clock above the note, and the buttons below it.
		clockTop, clockBottom := 0, noteTop
		buttonsTop, buttonsBottom := noteBottom, showHigh+taller
		ringing := scene{now: at, phase: "idle", snooze: 9,
			ring: ringState{alarm: &alarm.Ring{Label: "Wake up"}, snoozable: true}}
		silenced := ringing
		silenced.ring.silenced = true

		if same(band(ringing, panel.wide, panel.high, noteTop, noteBottom),
			band(silenced, panel.wide, panel.high, noteTop, noteBottom)) {
			t.Errorf("%s: the ring was silenced and the page does not say so", panel.name)
		}

		// Nothing outside the note's band may move.
		if !same(band(ringing, panel.wide, panel.high, clockTop, clockBottom),
			band(silenced, panel.wide, panel.high, clockTop, clockBottom)) {
			t.Errorf("%s: the silenced note overprints the title or the clock", panel.name)
		}
		if !same(band(ringing, panel.wide, panel.high, buttonsTop, buttonsBottom),
			band(silenced, panel.wide, panel.high, buttonsTop, buttonsBottom)) {
			t.Errorf("%s: the silenced note overprints the buttons", panel.name)
		}
	}
}

// A silenced timer says it is silenced but not that pressing again snoozes, because a timer cannot
// be snoozed — so the two must not draw the same thing.
func TestASilencedTimerDoesNotOfferASnooze(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	const top, bottom = ringNoteTop, ringNoteBottom

	timer := scene{now: at, phase: "idle", snooze: 9, ring: ringState{timer: "Pasta", silenced: true}}
	alarmed := scene{now: at, phase: "idle", snooze: 9,
		ring: ringState{alarm: &alarm.Ring{Label: "Wake up"}, snoozable: true, silenced: true}}

	plain := scene{now: at, phase: "idle", snooze: 9, ring: ringState{timer: "Pasta"}}

	if same(band(plain, showWide, showHigh, top, bottom),
		band(timer, showWide, showHigh, top, bottom)) {
		t.Fatal("a silenced timer says nothing")
	}
	if same(band(timer, showWide, showHigh, top, bottom),
		band(alarmed, showWide, showHigh, top, bottom)) {
		t.Error("a silenced timer and a silenced alarm say the same thing; a timer cannot be snoozed")
	}
}
