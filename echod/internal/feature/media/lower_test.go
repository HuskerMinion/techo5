package media

import (
	"math"
	"testing"
)

// An alarm's music held under full (#651) stays there through a turn's duck and its release, and the
// two add up while both hold.
func TestALoweredTrackStaysLowered(t *testing.T) {
	m := &Stream{gain: 1, target: 1}
	db := func() float64 { return 20 * math.Log10(float64(m.target)) }
	m.Lower(-20)
	if got := db(); math.Abs(got+20) > 0.01 {
		t.Fatalf("lowered to %.1f dB, want -20", got)
	}
	m.Duck(0) // a turn ending
	if got := db(); math.Abs(got+20) > 0.01 {
		t.Errorf("a turn's end let it up to %.1f dB", got)
	}
	m.Lower(0)
	if m.target != 1 {
		t.Errorf("let up to %v of full", m.target)
	}
}
