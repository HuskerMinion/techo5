package speaker

import (
	"math"
	"testing"
)

// The lift leaves every step up to 16 as it is, rises evenly above it to the full lift at the top, and
// never goes past the vendor's own full volume; the lifted curve still lands exactly on its steps.
func TestLiftTop(t *testing.T) {
	for name, db := range topLifts {
		curve, ok := firstCurves[name]
		if !ok {
			continue
		}
		lifted := liftTop(curve, db)
		for s := 0; s <= VolumeSteps; s++ {
			want := curve[s]
			if s > liftedFrom {
				want = min(0, curve[s]+db*float64(s-liftedFrom)/float64(VolumeSteps-liftedFrom))
			}
			if math.Abs(lifted[s]-want) > 1e-9 {
				t.Errorf("%s step %d: %v dB, want %v", name, s, lifted[s], want)
			}
			if lifted[s] > 0 {
				t.Errorf("%s step %d: %v dB, past the vendor's own full volume", name, s, lifted[s])
			}
			got := float64(gainForStep(OutputSpeaker, s) * firstRatio(&lifted, OutputSpeaker, s))
			if w := math.Pow(10, lifted[s]/20); s > 0 && math.Abs(got-w) > w*1e-4 {
				t.Errorf("%s step %d: %v, want %v", name, s, got, w)
			}
		}
		if math.Abs(lifted[VolumeSteps]-curve[VolumeSteps]-db) > 1e-9 {
			t.Errorf("%s: the top is %v dB hotter, want %v", name, lifted[VolumeSteps]-curve[VolumeSteps], db)
		}
	}
}

// The switch picks the curve, and only where the tuning has a lift.
func TestLouderTopPicksTheCurve(t *testing.T) {
	first := [VolumeSteps + 1]float64{-90}
	lifted := liftTop(first, 8)
	p := &Player{first: &first, lifted: &lifted}
	if p.inFront() != &first {
		t.Error("off, the curve is not the usual one")
	}
	p.SetLouderTop(true)
	if p.inFront() != &lifted {
		t.Error("on, the curve is not the lifted one")
	}
	p.lifted = nil
	if p.inFront() != &first {
		t.Error("on a tuning with no lift, the switch changed the curve")
	}
}
