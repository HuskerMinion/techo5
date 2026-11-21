//go:build !dot

package display

import (
	"testing"
	"time"
)

// The countdown is drawn as often as it moves about two pixels, between four a second and once a
// second (#656): a 30-second photo across 960 pixels was a 32-pixel jump each second.
func TestCountdownStepFollowsTheLine(t *testing.T) {
	for _, c := range []struct {
		whole time.Duration
		span  int
		want  time.Duration
	}{
		{30 * time.Second, 960, countdownFastest},       // 62 ms a step, held to four a second
		{2 * time.Minute, 960, 250 * time.Millisecond},  // two pixels every quarter second, the fastest
		{10 * time.Minute, 960, time.Second},            // slower than a pixel a second: the clock's pace
		{0, 960, time.Second},                           // no photo time known
		{time.Minute, 0, time.Second},                   // nothing to fill
		{5 * time.Minute, 1483, 405 * time.Millisecond}, // the Spot's ring (countdownRing)
	} {
		if got := countdownStep(c.whole, c.span); got.Round(time.Millisecond) != c.want {
			t.Errorf("countdownStep(%v, %d) = %v, want %v", c.whole, c.span, got, c.want)
		}
	}
}
