//go:build spot

package display

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// A slow press stops a ringing alarm rather than opening a menu over it.
//
// The Spot reports Hold after 450 ms and Release when the finger goes. The ringing face refused both
// and returned false, which means "this was not a gesture on the ringing face" — so it fell through
// to the tail of the gesture chain, where Hold opens the ring menu. Pressing slightly too long at a
// ringing alarm opened a menu on top of it, on a face that says "Tap to stop".
func TestASlowPressStopsTheRing(t *testing.T) {
	for _, k := range []touch.Kind{touch.Tap, touch.Hold, touch.Release} {
		stop, snooze := ringMeans(k)
		if !stop {
			t.Errorf("%s does not stop a ring; it falls through to the menu underneath", k)
		}
		if snooze {
			t.Errorf("%s snoozes; a press is a stop", k)
		}
	}
}

// A sideways swipe still snoozes, and nothing else on the face means anything.
func TestTheRingingFaceTakesSwipesAndNothingElse(t *testing.T) {
	for _, k := range []touch.Kind{touch.SwipeLeft, touch.SwipeRight} {
		if stop, snooze := ringMeans(k); stop || !snooze {
			t.Errorf("%s should snooze, got stop=%v snooze=%v", k, stop, snooze)
		}
	}
	// Up and down are the volume, and stay the volume: they are the one pair a finger makes by
	// accident on a round panel, and an alarm must not be lost to one.
	for _, k := range []touch.Kind{touch.SwipeUp, touch.SwipeDown, touch.Drag} {
		if stop, snooze := ringMeans(k); stop || snooze {
			t.Errorf("%s acted on the ringing face, got stop=%v snooze=%v", k, stop, snooze)
		}
	}
}
