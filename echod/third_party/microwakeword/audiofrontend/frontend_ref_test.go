package audiofrontend

import (
	"math"
	"math/rand"
	"testing"
)

// The original ProcessFrame and FFT, as published, for checking the optimized ones against.

// ProcessFrame extracts features from one window of samples.
// samples must have at least windowSize elements.
func (f *Frontend) refProcessFrame(samples []int16) []uint16 {
	// 1. Apply Hann window and convert to float64
	windowed := make([]float64, f.fftSize)
	for i := 0; i < f.windowSize && i < len(samples); i++ {
		windowed[i] = float64(samples[i]) * f.hannWindow[i]
	}

	// 2. FFT → power spectrum
	spectrum := refRealFFT(windowed, f.fftSize)
	numBins := f.fftSize/2 + 1
	energy := make([]float64, numBins)
	for i := 0; i < numBins; i++ {
		re := spectrum[2*i]
		im := spectrum[2*i+1]
		energy[i] = re*re + im*im
	}

	// 3. Mel filterbank
	melEnergy := make([]float64, f.cfg.NumChannels)
	for ch := 0; ch < f.cfg.NumChannels; ch++ {
		sum := 0.0
		for bin := 0; bin < numBins; bin++ {
			sum += energy[bin] * f.melFilters[ch][bin]
		}
		melEnergy[ch] = math.Sqrt(math.Max(sum, 0))
	}

	// 4. Noise reduction (spectral subtraction)
	f.noiseReduce(melEnergy)

	// 5. PCAN gain control
	f.pcanGain(melEnergy)

	// 6. Log scaling
	output := make([]uint16, f.cfg.NumChannels)
	for i := 0; i < f.cfg.NumChannels; i++ {
		v := melEnergy[i]
		if v < 1 {
			v = 1
		}
		logV := math.Log(v) * (1 << 6) // scale_shift = 6
		if logV < 0 {
			logV = 0
		}
		if logV > 65535 {
			logV = 65535
		}
		output[i] = uint16(logV)
	}
	return output
}

// realFFT computes the FFT of a real signal of length n (must be power of 2).
// Returns interleaved [re0, im0, re1, im1, ..., re_{n/2}, im_{n/2}].
func refRealFFT(x []float64, n int) []float64 {
	// Implement Cooley-Tukey radix-2 DIT FFT on complex input,
	// then extract the real FFT result.
	re := make([]float64, n)
	im := make([]float64, n)
	copy(re, x[:n])

	// Bit-reversal permutation
	j := 0
	for i := 0; i < n; i++ {
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
		m := n >> 1
		for m >= 1 && j >= m {
			j -= m
			m >>= 1
		}
		j += m
	}

	// FFT butterfly
	for size := 2; size <= n; size <<= 1 {
		half := size >> 1
		angle := -2 * math.Pi / float64(size)
		wRe := math.Cos(angle)
		wIm := math.Sin(angle)
		for start := 0; start < n; start += size {
			uRe, uIm := 1.0, 0.0
			for k := 0; k < half; k++ {
				tRe := uRe*re[start+k+half] - uIm*im[start+k+half]
				tIm := uRe*im[start+k+half] + uIm*re[start+k+half]
				re[start+k+half] = re[start+k] - tRe
				im[start+k+half] = im[start+k] - tIm
				re[start+k] += tRe
				im[start+k] += tIm
				newURe := uRe*wRe - uIm*wIm
				uIm = uRe*wIm + uIm*wRe
				uRe = newURe
			}
		}
	}

	// Pack into interleaved output: n/2+1 complex bins
	numBins := n/2 + 1
	out := make([]float64, numBins*2)
	for i := 0; i < numBins; i++ {
		out[2*i] = re[i]
		out[2*i+1] = im[i]
	}
	return out
}

// The optimized ProcessFrame gives the same features as the original, frame after frame (the noise
// estimate carries over, so a difference would grow).
func TestProcessFrameMatchesReference(t *testing.T) {
	cfg := DefaultConfig(10)
	fast, slow := New(cfg), New(cfg)
	r := rand.New(rand.NewSource(3))
	samples := make([]int16, fast.windowSize)
	for frame := 0; frame < 2000; frame++ {
		amp := 1 + r.Intn(30000)
		for i := range samples {
			samples[i] = int16(r.Intn(2*amp+1) - amp)
		}
		a, b := fast.ProcessFrame(samples), slow.refProcessFrame(samples)
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("frame %d channel %d: %d, want %d", frame, i, a[i], b[i])
			}
		}
	}
}

func benchFrames(b *testing.B, fn func(*Frontend, []int16) []uint16) {
	f := New(DefaultConfig(10))
	r := rand.New(rand.NewSource(3))
	samples := make([]int16, f.windowSize)
	for i := range samples {
		samples[i] = int16(r.Intn(20001) - 10000)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fn(f, samples)
	}
}

func BenchmarkProcessFrame(b *testing.B) {
	benchFrames(b, (*Frontend).ProcessFrame)
}

func BenchmarkProcessFrameOriginal(b *testing.B) {
	benchFrames(b, (*Frontend).refProcessFrame)
}
