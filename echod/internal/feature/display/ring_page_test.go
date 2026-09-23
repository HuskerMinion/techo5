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

// A silenced ring says so, in the one band of the ringing page that is clear.
//
// A ring a button quieted looks exactly like one that stopped, and it is not stopped — it comes back
// unless it is answered. The note goes between the title's descenders and the clock's digits, which
// is about thirty scaled pixels: a small-face line does not fit there and overprints the clock, which
// is what the first attempt did. This pins the band as empty when there is nothing to say and used
// when there is.
func TestASilencedRingSaysSoWithoutHittingTheClock(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)

	// The note's band, and everything below it down to the buttons: the clock lives there and must
	// be drawn identically either way.
	const noteTop, noteBottom = 96, 120
	const clockTop, clockBottom = 120, 300

	for _, panel := range showPanels() {
		ringing := scene{now: at, phase: "idle", snooze: 9,
			ring: ringState{alarm: &alarm.Ring{Label: "Wake up"}, snoozable: true}}
		silenced := ringing
		silenced.ring.silenced = true

		if same(band(ringing, panel.wide, panel.high, noteTop, noteBottom),
			band(silenced, panel.wide, panel.high, noteTop, noteBottom)) {
			t.Errorf("%s: the ring was silenced and the page does not say so", panel.name)
		}

		// The first attempt at this note was a small-face line at a baseline that put it straight
		// through the clock's digits. Nothing below the note's band may move.
		if !same(band(ringing, panel.wide, panel.high, clockTop, clockBottom),
			band(silenced, panel.wide, panel.high, clockTop, clockBottom)) {
			t.Errorf("%s: the silenced note overprints the clock", panel.name)
		}
	}
}

// A silenced timer says it is silenced but not that pressing again snoozes, because a timer cannot
// be snoozed — so the two must not draw the same thing.
func TestASilencedTimerDoesNotOfferASnooze(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	const top, bottom = 96, 120

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
