package interpreter

import (
	"math"
	"testing"
	"time"
)

// techo5: a scale that is not a finite number, from a model made to hang the check a custom model
// goes through, gives a zero multiplier at once instead of a loop that never ends.
func TestQuantizeMultiplierTakesAnyNumber(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, v := range []float64{math.Inf(1), math.Inf(-1), math.NaN(), 0} {
			if q := QuantizeMultiplier(v); q != (QuantMultiplier{0, 0}) {
				t.Errorf("QuantizeMultiplier(%v) = %+v, want zero", v, q)
			}
		}
		if q := QuantizeMultiplier(0.5); q.Multiplier == 0 {
			t.Error("QuantizeMultiplier(0.5) gave zero")
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("QuantizeMultiplier did not return")
	}
}
