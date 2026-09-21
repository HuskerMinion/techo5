package alarm

import (
	"math"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
)

// The ring goes from the deep red of the first minutes through orange to a warm white, and gets
// brighter all the way: it is a sunrise, not a colour wheel.
func TestTheRingWarmsAsItRises(t *testing.T) {
	start, middle, end := sunriseColor(0.02), sunriseColor(0.5), sunriseColor(1)

	if start.G > 20 || start.B > 10 {
		t.Errorf("it starts at %+v, want the deep red of the first minutes", start)
	}
	if !(middle.G > start.G && end.G > middle.G) {
		t.Errorf("it does not warm: %+v then %+v then %+v", start, middle, end)
	}
	if end.B < 100 {
		t.Errorf("it ends at %+v, want something near white", end)
	}
	bright := func(c led.Color) int { return int(c.R) + int(c.G) + int(c.B) }
	if !(bright(start) < bright(middle) && bright(middle) < bright(end)) {
		t.Errorf("it does not get brighter: %d then %d then %d", bright(start), bright(middle), bright(end))
	}
}

// The curve is the same one the screen uses, so a Dot beside a Show does not come up at a different
// rate from it.
func TestTheCurveIsSharedWithTheScreen(t *testing.T) {
	if got := SunriseLevel(0); got != 0 {
		t.Errorf("SunriseLevel(0) = %v, want nothing before it starts", got)
	}
	if got := SunriseLevel(0.01); got > 0.05 {
		t.Errorf("it starts at %.0f%%, want a glow", got*100)
	}
	if got := SunriseLevel(1); math.Abs(got-1) > 0.001 {
		t.Errorf("SunriseLevel(1) = %v, want all of it", got)
	}
}
