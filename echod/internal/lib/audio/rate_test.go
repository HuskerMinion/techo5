package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// rms is the root mean square of xs, skipping the first skip samples (the filter filling up).
func rms(xs []float64, skip int) float64 {
	var sum float64
	for _, x := range xs[skip:] {
		sum += x * x
	}
	return math.Sqrt(sum / float64(len(xs)-skip))
}

// decimate runs a sine at hz through a decimator and returns the output.
func decimate(factor, taps, rate int, hz float64, n int) []float64 {
	d := NewDecimator(factor, taps)
	var out []float64
	for i := range n {
		if y, ok := d.Push(float32(0.5 * math.Sin(2*math.Pi*hz*float64(i)/float64(rate)))); ok {
			out = append(out, float64(y))
		}
	}
	return out
}

// The microphones' 96 kHz and the reference's 48 kHz both come down to 16 kHz: speech passes at its
// level, and what would fold back into the speech band is gone.
func TestDecimatorPassesSpeechAndStopsAliases(t *testing.T) {
	for _, c := range []struct {
		factor, taps, rate int
	}{{6, 192, 96000}, {3, 96, 48000}} {
		pass := rms(decimate(c.factor, c.taps, c.rate, 1000, c.rate), 100)
		if want := 0.5 / math.Sqrt2; math.Abs(pass-want) > 0.01*want {
			t.Errorf("%d Hz: 1 kHz came out at %.4f RMS, want %.4f", c.rate, pass, want)
		}
		// 12 kHz would land on 4 kHz.
		stop := rms(decimate(c.factor, c.taps, c.rate, 12000, c.rate), 100)
		if db := 20 * math.Log10(stop/(0.5/math.Sqrt2)); db > -60 {
			t.Errorf("%d Hz: 12 kHz folded back at %.1f dB, want under -60", c.rate, db)
		}
	}
}

// The two rates' filters delay by the same time, so the reference stays aligned with the
// microphones after both are brought down.
func TestDecimatorsAtTwoRatesDelayAlike(t *testing.T) {
	impulse := func(factor, taps int) int {
		d := NewDecimator(factor, taps)
		best, at := float32(0), -1
		for i := range 4 * taps {
			x := float32(0)
			if i == 0 {
				x = 1
			}
			if y, ok := d.Push(x); ok && y > best {
				best, at = y, i
			}
		}
		return at
	}
	// In input samples, then in microseconds.
	mic := float64(impulse(6, 192)) / 96000 * 1e6
	ref := float64(impulse(3, 96)) / 48000 * 1e6
	if math.Abs(mic-ref) > 63 { // one output sample at 16 kHz
		t.Errorf("peak at %.0f us on the microphones and %.0f us on the reference", mic, ref)
	}
}

func TestUpsamplerKeepsTheLevelAndTheTone(t *testing.T) {
	const rate, hz, n = 48000, 1000.0, 48000
	in := make([]byte, 0, 4*n)
	for i := range n {
		s := uint16(int16(16000 * math.Sin(2*math.Pi*hz*float64(i)/rate)))
		in = binary.LittleEndian.AppendUint16(in, s)
		in = binary.LittleEndian.AppendUint16(in, s)
	}
	u := NewUpsampler(2, 48, 2, 1)
	// In two blocks, so the history between calls is used.
	out := u.S16LE(in[:len(in)/2], nil)
	out = u.S16LE(in[len(in)/2:], out)
	if len(out) != 2*len(in) {
		t.Fatalf("%d bytes out of %d in, want twice", len(out), len(in))
	}

	left := make([]float64, 0, 2*n)
	for f := 0; f < len(out); f += 4 {
		left = append(left, float64(int16(binary.LittleEndian.Uint16(out[f:]))))
	}
	if got, want := rms(left, 200), 16000/math.Sqrt2; math.Abs(got-want) > 0.01*want {
		t.Errorf("RMS %.0f, want %.0f", got, want)
	}

	// The tone at 96 kHz: 96 samples a period, so the output a period later matches.
	for i := 1000; i < 1100; i++ {
		if d := math.Abs(left[i] - left[i+96]); d > 2 {
			t.Fatalf("sample %d differs from a period later by %.0f", i, d)
		}
	}
	if u.Clipped() != 0 {
		t.Errorf("clipped %d samples of a half-scale tone", u.Clipped())
	}
}

// What the trim takes off is taken off exactly.
func TestUpsamplerGain(t *testing.T) {
	in := make([]byte, 0, 2*4800)
	for range 4800 {
		in = binary.LittleEndian.AppendUint16(in, uint16(int16(10000)))
	}
	out := NewUpsampler(2, 48, 1, math.Pow(10, -10.0/20)).S16LE(in, nil)
	last := float64(int16(binary.LittleEndian.Uint16(out[len(out)-2:])))
	if want := 10000 * math.Pow(10, -10.0/20); math.Abs(last-want) > 2 {
		t.Errorf("DC of 10000 at -10 dB came out %.0f, want %.0f", last, want)
	}
}

// Silence is skipped, not changed: through sound, a long silence and sound again, both filters give
// exactly what the plain sums do, and silence comes out as zeros.
func TestSilenceSkippedExactly(t *testing.T) {
	sig := func(i int) float32 {
		if i >= 3000 && i < 9000 {
			return 0
		}
		return float32(math.Sin(float64(i)*0.05)*8000 + math.Sin(float64(i)*0.31)*3000)
	}
	d := NewDecimator(6, 96)
	var win []float32
	for i := range 12000 {
		x := sig(i)
		win = append(win, x)
		y, ok := d.Push(x)
		if !ok {
			continue
		}
		var want float32
		for j, c := range d.h {
			if k := len(win) - len(d.h) + j; k >= 0 {
				want += c * win[k]
			}
		}
		if math.Abs(float64(y-want)) > 1e-2*(1+math.Abs(float64(want))) {
			t.Fatalf("decimator at %d: %v, want %v", i, y, want)
		}
		if i >= 3000+len(d.h) && i < 9000 && y != 0 {
			t.Fatalf("decimator at %d: %v in silence", i, y)
		}
	}

	u := NewUpsampler(2, 48, 2, 1)
	ref := NewUpsampler(2, 48, 2, 1)
	var in []byte
	for i := range 6000 {
		v := int16(0)
		if i < 1000 || i >= 4000 {
			v = int16(sig(i) / 2)
		}
		in = binary.LittleEndian.AppendUint16(in, uint16(v))
		in = binary.LittleEndian.AppendUint16(in, uint16(-v))
	}
	got := u.S16LE(in, nil)
	ref.zeros = -1 << 30 // never skips
	want := ref.S16LE(in, nil)
	if !bytes.Equal(got, want) {
		t.Fatal("the upsampler's output changed with silence skipped")
	}
}
