package asp

import (
	"math"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/fft"
)

// What the chain does to a pure tone, measured rather than listened to.
//
// A tuning that is working sounds like the speaker it was made for; a tuning that is overdriving
// something sounds like grit on loud passages, and the two are hard to tell apart by ear on a desk.
// This runs a sine through the real coefficients at the levels the volume dial actually produces and
// reports the harmonics, so "it distorts when turned up" becomes a number.
//
//	ECHOLOCAL_VENDOR_DIR=/tmp/coefs go test ./internal/lib/asp/ -run VendorTone -v
func TestVendorToneStaysClean(t *testing.T) {
	v := vendorTuning(t)

	const (
		rate  = Rate
		n     = 1 << 15
		block = 1024
	)

	for _, tone := range []float64{60, 200, 1000} {
		for _, level := range []float64{-20, -12, -6, -3} {
			c, err := v.Chain(block)
			if err != nil {
				t.Fatalf("Chain: %v", err)
			}
			c.Volume(1)

			amp := math.Pow(10, level/20)
			x := make([]float32, n)
			for i := range x {
				x[i] = float32(amp * math.Sin(2*math.Pi*tone*float64(i)/rate))
			}
			for i := 0; i+block <= len(x); i += block {
				c.Process(x[i : i+block])
			}

			// The first half is the compressor settling; the tone is measured once it has.
			tail := x[n/2:]
			spec := make([]complex64, len(tail))
			for i, s := range tail {
				// A window, so the tone does not smear across bins and drown its own harmonics.
				w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(len(tail)-1))
				spec[i] = complex(float32(float64(s)*w), 0)
			}
			fft.New(len(spec)).Forward(spec)

			bin := func(f float64) float64 {
				k := int(math.Round(f * float64(len(spec)) / rate))
				var most float64
				for _, j := range []int{k - 1, k, k + 1} {
					if j > 0 && j < len(spec)/2 {
						most = math.Max(most, math.Hypot(float64(real(spec[j])), float64(imag(spec[j]))))
					}
				}
				return most
			}

			fund := bin(tone)
			var harm float64
			for h := 2; h <= 8; h++ {
				m := bin(tone * float64(h))
				harm += m * m
			}
			thd := 100 * math.Sqrt(harm) / fund

			var peak float64
			for _, s := range tail {
				peak = math.Max(peak, math.Abs(float64(s)))
			}
			t.Logf("%.0f Hz in at %+.0f dBFS: out peak %+.2f dBFS, THD %.2f%%",
				tone, level, 20*math.Log10(peak), thd)

			if thd > 10 {
				t.Errorf("%.0f Hz at %+.0f dBFS gives %.1f%% harmonic distortion, which is audible as grit",
					tone, level, thd)
			}
		}
	}
}
