package array

import (
	"math"
	"math/cmplx"

	"github.com/HuskerMinion/techo5/echod/internal/lib/fft"
)

// Spectra turns blocks of every microphone into windowed spectra, for cross spectra to be made from.
// It keeps the bins between lo and hi only, since nothing outside them is used.
type Spectra struct {
	n      int
	rate   float64
	lo, hi int
	fft    *fft.FFT
	window []float32
	buf    []complex64
}

// NewSpectra is for blocks of n samples at rate, keeping lo to hi Hz.
func NewSpectra(n int, rate, loHz, hiHz float64) *Spectra {
	s := &Spectra{n: n, rate: rate, fft: fft.New(n), window: make([]float32, n), buf: make([]complex64, n)}
	for i := range s.window {
		s.window[i] = float32(0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n)))
	}
	s.lo = max(1, int(math.Ceil(loHz*float64(n)/rate)))
	s.hi = min(n/2-1, int(hiHz*float64(n)/rate))
	return s
}

// Bins is how many bins a spectrum holds.
func (s *Spectra) Bins() int { return s.hi - s.lo + 1 }

// Omega is bin k's angular frequency, rad/s.
func (s *Spectra) Omega(k int) float64 { return 2 * math.Pi * float64(s.lo+k) * s.rate / float64(s.n) }

// Size is the block length.
func (s *Spectra) Size() int { return s.n }

// Into transforms one block into out, which must hold Bins.
func (s *Spectra) Into(block []float32, out []complex64) {
	for i := range s.buf {
		s.buf[i] = complex(block[i]*s.window[i], 0)
	}
	s.fft.Forward(s.buf)
	copy(out, s.buf[s.lo:s.hi+1])
}

// PHAT adds A's spectrum times B's conjugate to acc, each bin brought to unit magnitude: the phase
// transform, which keeps when each frequency arrives and throws away how loud it is, so the room's
// colour and the source's spectrum do not decide the answer. weight scales the whole frame, for a
// caller that trusts some frames more than others.
func PHAT(acc []complex128, a, b []complex64, weight float64) {
	for k := range acc {
		c := complex128(a[k]) * cmplx.Conj(complex128(b[k]))
		if m := cmplx.Abs(c); m > 1e-20 {
			acc[k] += c * complex(weight/m, 0)
		}
	}
}

// Lag finds the arrival difference (A minus B, seconds) the accumulated cross spectrum points at,
// searching ±limit in steps of step. It evaluates the spectrum's phase directly at each candidate
// rather than reading an inverse transform's samples, so the answer is not held to the sample grid:
// at 16 kHz a 22.5 mm pair spans little more than a sample.
//
// strength is the peak's height as a fraction of a perfect match, 0 to 1.
func (s *Spectra) Lag(acc []complex128, limit, step float64) (lag, strength float64) {
	var total float64
	for _, c := range acc {
		total += cmplx.Abs(c)
	}
	best := math.Inf(-1)
	for t := -limit; t <= limit+step/2; t += step {
		var p float64
		for k, c := range acc {
			// A lagging B by t is A's phase behind by ωt: undo it and the bins add up.
			w := s.Omega(k) * t
			p += real(c)*math.Cos(w) - imag(c)*math.Sin(w)
		}
		if p > best {
			best, lag = p, t
		}
	}
	if total > 0 {
		strength = best / total
	}
	return lag, strength
}
