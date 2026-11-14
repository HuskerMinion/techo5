//go:build spot

package display

import (
	"math"
	"testing"
)

// Done and the star keep inside the ring at the rim, which shows a timer running or the microphone off:
// nothing of either reaches the ring's inner edge, 222 px from the center.
func TestDoneAndTheStarKeepOffTheRing(t *testing.T) {
	const ringInner = 222
	reach := func(x, y, r float64) float64 { return math.Hypot(x-center, y-center) + r }
	for name, far := range map[string]float64{
		"Done's outer end": reach(doneX-18, doneY, doneR),
		"the star":         reach(starX, starY, starR),
	} {
		if far >= ringInner {
			t.Errorf("%s reaches %.0f px from the center, into the ring at %d", name, far, ringInner)
		}
	}
	// And each is still tapped where it is drawn.
	if !onDone(int(doneX), int(doneY)) || !onStar(int(starX), int(starY)) {
		t.Error("Done or the star is not tapped at its own middle")
	}
}
