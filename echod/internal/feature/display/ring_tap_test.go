//go:build !dot && !spot

package display

import (
	"image"
	"testing"
)

// The whole screen stops a ringing alarm, not just the buttons and a little above them.
//
// The live region used to be the action band alone. On a Show 5 that left the top 65% of the panel
// deciding nothing, and the same sizes scale to 72% on a Show 8 — so most of a ringing alarm was a
// picture of two buttons that did nothing where somebody actually pressed.
func TestTheWholeRingingScreenStops(t *testing.T) {
	for _, panel := range showPanels() {
		img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		r := newRenderer(img)

		y0, _ := r.actionBand()
		above := []int{0, y0 / 4, y0 / 2, y0 - r.s(actionReach) - 1}

		for _, y := range above {
			if r.actionDecided(y) {
				continue // inside the buttons' reach after all; nothing to prove here
			}
			// Above the buttons, on either side, the answer is Stop — never Snooze, and never
			// nothing.
			for _, x := range []int{1, panel.wide / 4, panel.wide/2 + 1, panel.wide - 1} {
				if ringSnoozeAt(x, panel.wide, true, r.actionDecided(y)) {
					t.Errorf("%s: a tap at (%d,%d), above the buttons, snoozes; it should stop",
						panel.name, x, y)
				}
			}
		}
	}
}

// Snooze stays exactly where the Snooze button is drawn, so the deliberate choice is still there.
func TestSnoozeIsOnlyTheSnoozeButton(t *testing.T) {
	for _, panel := range showPanels() {
		img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		r := newRenderer(img)

		y0, y1 := r.actionBand()
		mid := (y0 + y1) / 2
		if !r.actionDecided(mid) {
			t.Fatalf("%s: the middle of the button band is not inside it", panel.name)
		}

		if !ringSnoozeAt(panel.wide-1, panel.wide, true, true) {
			t.Errorf("%s: the right of the button band does not snooze", panel.name)
		}
		if ringSnoozeAt(1, panel.wide, true, true) {
			t.Errorf("%s: the left of the button band snoozes; it is the Stop button", panel.name)
		}
		// A ringing timer has no snooze, so both halves stop.
		if ringSnoozeAt(panel.wide-1, panel.wide, false, true) {
			t.Errorf("%s: a timer was snoozed, and a timer cannot be put off", panel.name)
		}
	}
}
