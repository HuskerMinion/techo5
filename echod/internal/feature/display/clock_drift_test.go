//go:build !dot && !spot

package display

import (
	"image"
	"testing"
	"time"
)

// The clock wanders a pixel a minute at most, never past its reach, through every place in between, and
// on through midnight without a jump (#560).
func TestClockDrift(t *testing.T) {
	for _, reach := range []int{driftAcross, driftDown} {
		seen := map[int]bool{}
		prev := sway(0, reach)
		if prev != 0 {
			t.Errorf("reach %d: starts at %d, not its place", reach, prev)
		}
		for m := 1; m <= 24*60; m++ {
			v := sway(m%(24*60), reach)
			if d := v - prev; d > 1 || d < -1 {
				t.Fatalf("reach %d: jumps %d at minute %d", reach, d, m)
			}
			if v > reach || v < -reach {
				t.Fatalf("reach %d: %d at minute %d", reach, v, m)
			}
			seen[v] = true
			prev = v
		}
		if len(seen) != 2*reach+1 {
			t.Errorf("reach %d: visits %d places, want %d", reach, len(seen), 2*reach+1)
		}
	}
	if sway(7, 0) != 0 {
		t.Error("no reach still moves")
	}

	// The same time of day is the same place, whatever the day: previews stay the same frame.
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, 960, 480)))
	a := r.clockDrift(time.Date(2026, 3, 1, 10, 42, 0, 0, time.UTC))
	b := r.clockDrift(time.Date(2026, 11, 9, 10, 42, 59, 0, time.UTC))
	if a != b {
		t.Errorf("10:42 on two days: %v and %v", a, b)
	}
	// Down only, so the digits never stand higher than the room they were sized for.
	day := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	for m := range 24 * 60 {
		d := r.clockDrift(day.Add(time.Duration(m) * time.Minute))
		if d.Y < 0 || d.Y > r.s(driftDown) || d.X < -r.s(driftAcross) || d.X > r.s(driftAcross) {
			t.Fatalf("minute %d: %v", m, d)
		}
	}
}
