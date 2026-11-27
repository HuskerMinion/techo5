package mic

import (
	"math"
	"math/rand"
	"testing"
)

// The cascade cancels a room's echo, passes SetAdapting to both its filters, and is what newFilter
// builds only where freqCancel says.
func TestCascadeCancels(t *testing.T) {
	c, err := newCascade(1024)
	if err != nil {
		t.Fatal(err)
	}
	if c.Taps() != 1024 {
		t.Errorf("taps %d", c.Taps())
	}

	rng := rand.New(rand.NewSource(4))
	const frame, n = 320, 16000 * 6
	ref := make([]int16, n)
	var lp float64
	for i := range ref {
		lp = 0.9*lp + rng.NormFloat64()
		ref[i] = int16(max(-32767, min(32767, lp*1500)))
	}
	mic := make([]int16, n)
	for i := range mic {
		var acc float64
		for k, h := range []float64{0, 0, 0, 0, 0, 0.4, -0.2, 0.1} {
			if i-k >= 0 {
				acc += h * float64(ref[i-k])
			}
		}
		mic[i] = int16(acc + rng.NormFloat64()*3)
	}
	for at := 0; at+frame <= n; at += frame {
		if _, err := c.Process(mic[at:at+frame], ref[at:at+frame]); err != nil {
			t.Fatal(err)
		}
	}
	if e := c.ERLE(); math.IsNaN(e) || e < 15 {
		t.Errorf("ERLE %.1f dB after six seconds of echo", e)
	}

	c.SetAdapting(false)
	if c.Adapting() || c.freq.Adapting() || c.tail.Adapting() {
		t.Error("SetAdapting(false) left a filter learning")
	}
	c.SetAdapting(true)
	if !c.freq.Adapting() || !c.tail.Adapting() {
		t.Error("SetAdapting(true) left a filter held")
	}

	if _, err := c.Process(make([]int16, 100), make([]int16, 100)); err == nil {
		t.Error("a frame of 100 samples, not whole blocks, was taken")
	}

	f, err := newFilter(1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, isCascade := f.(*cascade); isCascade != freqCancel {
		t.Errorf("newFilter built a %T with freqCancel %v", f, freqCancel)
	}
}
