//go:build !dot && !spot

package display

import (
	"math"
	"testing"
)

// The ramp starts low enough not to wake anybody by itself and ends at the screen's own brightness,
// with most of the change late rather than the moment it starts.
func TestTheLightComesUpSlowlyThenAllAtOnce(t *testing.T) {
	if got := sunriseLevel(0); got != 0 {
		t.Errorf("sunriseLevel(0) = %v, want nothing before it starts", got)
	}
	start, half, end := sunriseLevel(0.01), sunriseLevel(0.5), sunriseLevel(1)
	if start > 0.05 {
		t.Errorf("it starts at %.0f%% of the screen, want a glow", start*100)
	}
	if math.Abs(end-1) > 0.001 {
		t.Errorf("it ends at %.2f, want the screen's own brightness", end)
	}
	if half > 0.35 {
		t.Errorf("halfway through it is already at %.0f%%, want most of the light late", half*100)
	}
	if !(start < half && half < end) {
		t.Errorf("the light does not rise: %v then %v then %v", start, half, end)
	}
}
