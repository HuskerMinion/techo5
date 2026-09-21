package asp

import (
	"math"
	"testing"
)

// A tone control that does nothing where it says it does nothing matters as much as the part that
// does something: the shelves sit in front of a compressor that was set up against the mids, and a
// bass control that moved them would move what the whole tuning is anchored to.
func TestShelvesMoveOnlyTheirOwnEnd(t *testing.T) {
	level := func(s *tone, hz float64) float64 {
		const n = 1 << 14
		x := make([]float32, n)
		for i := range x {
			x[i] = float32(math.Sin(2 * math.Pi * hz * float64(i) / Rate))
		}
		s.process(x)
		// The tail only: the filter's own start is not what it settles at.
		var peak float64
		for _, v := range x[n/2:] {
			peak = math.Max(peak, math.Abs(float64(v)))
		}
		return 20 * math.Log10(peak)
	}

	for _, c := range []struct {
		what       string
		tone       Tone
		hz         float64
		want, slop float64
	}{
		{"bass down at 60 Hz", Tone{Bass: -4}, 60, -4, 0.7},
		{"bass down leaves 1 kHz", Tone{Bass: -4}, 1000, 0, 0.5},
		{"bass up at 60 Hz", Tone{Bass: 5}, 60, 5, 0.7},
		{"treble down at 12 kHz", Tone{Treble: -4}, 12000, -4, 0.7},
		{"treble down leaves 500 Hz", Tone{Treble: -4}, 500, 0, 0.5},
		{"flat leaves 60 Hz", Tone{}, 60, 0, 0.01},
		{"flat leaves 12 kHz", Tone{}, 12000, 0, 0.01},
	} {
		got := level(newTone(c.tone, Rate), c.hz)
		if math.Abs(got-c.want) > c.slop {
			t.Errorf("%s: %+.2f dB, expected %+.0f", c.what, got, c.want)
		}
	}
}

// Nothing a configuration can say may take the driver somewhere the tuning was not designed for.
func TestToneIsBounded(t *testing.T) {
	got := Tone{Bass: 40, Treble: -40}.clamped()
	if got.Bass != ToneRange || got.Treble != -ToneRange {
		t.Errorf("a tone of 40 and -40 clamped to %+v, expected ±%v", got, ToneRange)
	}
}
