//go:build !dot

package camera

import (
	"testing"
	"time"
)

func TestAEHold(t *testing.T) {
	SetGestureExposure(true)
	defer SetGestureExposure(false)
	var h aeHold
	t0 := time.Unix(1000, 0)
	at := func(i int) time.Time { return t0.Add(time.Duration(i) * 125 * time.Millisecond) }

	// A jump during warm-up is the sensor finding its exposure, not a hand.
	if h.heldAt(100, at(0)) || h.heldAt(20, at(1)) {
		t.Fatal("held during warm-up")
	}
	i := 2
	for ; i < 40; i++ {
		if h.heldAt(100, at(i)) {
			t.Fatalf("held on a steady picture at frame %d", i)
		}
	}
	// A hand over the lens: held, and for no longer than aeHoldFor.
	if !h.heldAt(10, at(i)) {
		t.Fatal("not held on a jump")
	}
	start := at(i)
	for i++; at(i).Sub(start) < aeHoldFor; i++ {
		if !h.heldAt(10, at(i)) {
			t.Fatal("hold ended early")
		}
	}
	// It ended with the room still dark (a light switched off): the exposure follows from here, and
	// does not hold again against the old brightness.
	for j := 0; j < 30; j++ {
		if h.heldAt(10, at(i)) {
			t.Fatalf("held again %d frames after the hold ended", j)
		}
		i++
	}

	// A picture being looked at gets the exposure at once.
	liveUsers.Add(1)
	if h.heldAt(100, at(i)) || h.heldAt(10, at(i+1)) {
		t.Fatal("held with a live viewer")
	}
	liveUsers.Add(-1)

	// After a gap (the sensor stopped and started again), warm-up again.
	i += 100
	if h.heldAt(100, at(i)) || h.heldAt(10, at(i+1)) {
		t.Fatal("held right after a restart")
	}
}
