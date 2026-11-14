//go:build spot

package display

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// A tap on nothing puts the ring menu away: between the middle and the items, or outside the ring. It
// used to do nothing, and a menu opened by mistake could only be waited out.
func TestATapOnNothingClosesTheRingMenu(t *testing.T) {
	for _, r := range []int{dialHub + 16, dialR + dialHit + 12} {
		d := &Display{menuOpen: true}
		// Straight down from the center is between two items of the ring at rest only if none sits
		// there; whichever it is, the tap must be on no item for this to prove anything.
		x, y := center, center+r
		if item, middle := dialHitAt(x, y, d.menuRot, len(itemsFor(d.menuMode))); item >= 0 || middle {
			x, y = center+r, center
			if item, middle := dialHitAt(x, y, d.menuRot, len(itemsFor(d.menuMode))); item >= 0 || middle {
				t.Fatalf("radius %d: no empty place found to tap", r)
			}
		}
		d.menuGesture(touch.Gesture{Kind: touch.Tap, X: x, Y: y})
		if d.menuOpen {
			t.Errorf("radius %d: a tap on nothing left the menu open", r)
		}
	}
}
