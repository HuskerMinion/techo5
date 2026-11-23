package aec

import (
	"fmt"
	"math"
	"math/cmplx"
	"sync/atomic"
)

// Freq is a frequency-domain echo canceller: a partitioned-block adaptive filter (overlap-save, each
// bin normalized by its own reference power) whose step size follows how much echo is left, after
// Valin's "On adjusting the learning rate in frequency domain echo cancellation with double-talk"
// (IEEE TASLP 2007), the scheme of SpeexDSP's MDF canceller. Two copies of the filter run: the
// background one learns and the foreground one cancels, and the foreground takes the background's
// coefficients only once they do better, so a voice the filter mistakes for echo, or a passage it
// briefly fits wrong, never reaches the output.
//
// Why: the time-domain filter (Canceller) normalizes by the power of the whole reference, so on music,
// whose power sits in the bass, its high bands learn hundreds of times slower than its low ones. On an
// Echo Dot 5th gen playing music (2026-11-23) it took 4 to 8 dB off 2 to 4 kHz in 30 seconds, where
// the same filter reaches 14 to 20 dB after three passes over the take: the band a wake word's
// consonants are in is the one left with the echo. Normalizing per bin takes that away.
//
// Frames go through in blocks of freqBlock samples, so a frame has to be a whole number of blocks; it
// adds no delay.
type Freq struct {
	b, n, parts int

	bg, fg [][]complex128 // per partition, n bins
	x      [][]complex128 // reference spectra, x[(head+p)%parts] is p blocks old
	head   int
	xtime  []float64 // the last n reference samples, pre-emphasized
	powSum []float64 // Σ over partitions of |X|², per bin

	memX, memD, memE float64

	// leak estimation (Valin), per-bin smoothed power spectra of the error and the echo estimate
	eh, yh   []float64
	pey, pyy float64
	leak     float64

	sumAdapt float64
	adapted  bool

	davg1, davg2, dvar1, dvar2 float64

	block    int
	adapting atomic.Bool

	sumD, sumE float64
	out        []int16

	// scratch
	tw                     []complex128
	yf, yb, ef, eb, xin, d []float64
	Y, E, T                []complex128
	p1                     []float64
}

const (
	freqBlock  = 64
	freqPreemp = 0.9
	// freqReg is what the reference power per bin is never taken below: about -90 dBFS of white noise
	// in a bin, so a bin the music leaves empty learns nothing rather than everything.
	freqReg = 1e-9
)

// NewFreq builds a frequency-domain canceller reaching taps samples (rounded up to whole blocks).
func NewFreq(taps int) (*Freq, error) {
	if taps <= 0 {
		return nil, fmt.Errorf("aec: taps must be positive, got %d", taps)
	}
	f := &Freq{}
	f.init(taps)
	f.adapting.Store(true)
	return f, nil
}

// init sets every field but adapting to a filter that knows nothing.
func (f *Freq) init(taps int) {
	b := freqBlock
	n := 2 * b
	parts := (taps + b - 1) / b
	adapting := f.adapting.Load()
	*f = Freq{b: b, n: n, parts: parts} //nolint:govet // adapting is stored again below
	f.adapting.Store(adapting)
	mk := func() [][]complex128 {
		m := make([][]complex128, parts)
		for i := range m {
			m[i] = make([]complex128, n)
		}
		return m
	}
	f.bg, f.fg, f.x = mk(), mk(), mk()
	f.xtime = make([]float64, n)
	f.powSum = make([]float64, n)
	f.eh, f.yh = make([]float64, n/2+1), make([]float64, n/2+1)
	f.yf, f.yb, f.ef, f.eb = make([]float64, b), make([]float64, b), make([]float64, b), make([]float64, b)
	f.xin, f.d = make([]float64, b), make([]float64, b)
	f.Y, f.E, f.T = make([]complex128, n), make([]complex128, n), make([]complex128, n)
	f.p1 = make([]float64, n/2+1)
	f.tw = make([]complex128, n/2)
	for i := range f.tw {
		f.tw[i] = cmplx.Exp(complex(0, -2*math.Pi*float64(i)/float64(n)))
	}
}

// Taps is how far the filter reaches, in samples.
func (f *Freq) Taps() int { return f.parts * f.b }

// SetAdapting stops or resumes learning while it goes on canceling with what it has.
func (f *Freq) SetAdapting(on bool) { f.adapting.Store(on) }

// Adapting reports whether it is learning.
func (f *Freq) Adapting() bool { return f.adapting.Load() }

// ERLE is how much quieter the output is than the microphone was, over the last second or so.
func (f *Freq) ERLE() float64 {
	if f.sumE <= 0 || f.sumD <= 0 {
		return 0
	}
	return 10 * math.Log10(f.sumD/f.sumE)
}

// Reset forgets the room.
func (f *Freq) Reset() { f.init(f.Taps()) }

// Process cancels one frame, a whole number of blocks long. The returned slice is reused.
func (f *Freq) Process(mic, ref []int16) ([]int16, error) {
	if len(mic) != len(ref) || len(mic)%f.b != 0 {
		return nil, ErrLength
	}
	if cap(f.out) < len(mic) {
		f.out = make([]int16, len(mic))
	}
	f.out = f.out[:len(mic)]
	for at := 0; at < len(mic); at += f.b {
		for i := 0; i < f.b; i++ {
			x := float64(ref[at+i]) / full
			d := float64(mic[at+i]) / full
			f.xin[i] = x - freqPreemp*f.memX
			f.memX = x
			f.d[i] = d - freqPreemp*f.memD
			f.memD = d
			f.sumD = f.sumD*(1-1.0/erleTau) + d*d
		}
		f.step()
		for i := 0; i < f.b; i++ {
			v := f.ef[i] + freqPreemp*f.memE
			f.memE = v
			f.sumE = f.sumE*(1-1.0/erleTau) + v*v
			s := math.Round(v * full)
			f.out[at+i] = int16(max(min(s, math.MaxInt16), math.MinInt16))
		}
	}
	return f.out, nil
}

// fft is an in-place radix-2 transform of length n; inverse conjugates around it and scales.
func (f *Freq) fft(a []complex128, inverse bool) {
	n := len(a)
	if inverse {
		for i := range a {
			a[i] = cmplx.Conj(a[i])
		}
	}
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for l := 2; l <= n; l <<= 1 {
		stride := n / l
		for i := 0; i < n; i += l {
			for k := 0; k < l/2; k++ {
				u, v := a[i+k], a[i+k+l/2]*f.tw[k*stride]
				a[i+k], a[i+k+l/2] = u+v, u-v
			}
		}
	}
	if inverse {
		s := 1 / float64(n)
		for i := range a {
			a[i] = complex(real(a[i])*s, -imag(a[i])*s)
		}
	}
}

// filterOut is the last block of the time-domain output of filter w over the reference history.
func (f *Freq) filterOut(w [][]complex128, out []float64) {
	h := f.n / 2
	for k := range f.T {
		f.T[k] = 0
	}
	for p := 0; p < f.parts; p++ {
		X, W := f.x[(f.head+p)%f.parts], w[p]
		for k := 0; k <= h; k++ {
			f.T[k] += W[k] * X[k]
		}
	}
	for k := 1; k < h; k++ {
		f.T[f.n-k] = cmplx.Conj(f.T[k])
	}
	f.fft(f.T, true)
	for i := 0; i < f.b; i++ {
		out[i] = real(f.T[f.b+i])
	}
}

func (f *Freq) step() {
	n, b, h := f.n, f.b, f.n/2

	// The newest reference spectrum, over the last two blocks.
	copy(f.xtime, f.xtime[b:])
	copy(f.xtime[b:], f.xin)
	f.head = (f.head + f.parts - 1) % f.parts
	X0 := f.x[f.head]
	for k := 0; k <= h; k++ {
		f.powSum[k] -= real(X0[k] * cmplx.Conj(X0[k]))
	}
	for i := range X0 {
		X0[i] = complex(f.xtime[i], 0)
	}
	f.fft(X0, false)
	for k := 0; k <= h; k++ {
		f.powSum[k] = max(f.powSum[k]+real(X0[k]*cmplx.Conj(X0[k])), 0)
	}

	f.filterOut(f.fg, f.yf)
	f.filterOut(f.bg, f.yb)
	var sff, see, sxx, syy, sey, dbf float64
	for i := 0; i < b; i++ {
		f.ef[i] = f.d[i] - f.yf[i]
		f.eb[i] = f.d[i] - f.yb[i]
		sff += f.ef[i] * f.ef[i]
		see += f.eb[i] * f.eb[i]
		sxx += f.xin[i] * f.xin[i]
		syy += f.yb[i] * f.yb[i]
		sey += f.eb[i] * f.yb[i]
		dy := f.yf[i] - f.yb[i]
		dbf += dy * dy
	}
	const tiny = 1e-12
	see += tiny
	sff += tiny
	dbf += tiny

	if !f.adapting.Load() {
		return
	}

	// Foreground or background: whichever has been doing better keeps the output (Valin's two-path
	// scheme, with SpeexDSP's thresholds).
	diff := sff - see
	f.davg1 = .6*f.davg1 + .4*diff
	f.davg2 = .85*f.davg2 + .15*diff
	f.dvar1 = .36*f.dvar1 + .16*sff*dbf
	f.dvar2 = .7225*f.dvar2 + .0225*sff*dbf
	switch {
	case diff*math.Abs(diff) > sff*dbf, f.davg1*math.Abs(f.davg1) > .5*f.dvar1, f.davg2*math.Abs(f.davg2) > .25*f.dvar2:
		f.davg1, f.davg2, f.dvar1, f.dvar2 = 0, 0, 0, 0
		for p := range f.fg {
			copy(f.fg[p], f.bg[p])
		}
		copy(f.ef, f.eb)
	case -diff*math.Abs(diff) > 4*sff*dbf, -f.davg1*math.Abs(f.davg1) > 4*f.dvar1, -f.davg2*math.Abs(f.davg2) > 4*f.dvar2:
		// The background went wrong: start it again from the foreground.
		for p := range f.bg {
			copy(f.bg[p], f.fg[p])
		}
		copy(f.eb, f.ef)
		copy(f.yb, f.yf)
		see, syy, sey = sff, 0, 0
		for i := 0; i < b; i++ {
			syy += f.yb[i] * f.yb[i]
			sey += f.eb[i] * f.yb[i]
		}
		f.davg1, f.davg2, f.dvar1, f.dvar2 = 0, 0, 0, 0
	}

	// Spectra of the background's error and echo estimate.
	for i := range f.E {
		f.E[i], f.Y[i] = 0, 0
	}
	for i := 0; i < b; i++ {
		f.E[b+i] = complex(f.eb[i], 0)
		f.Y[b+i] = complex(f.yb[i], 0)
	}
	f.fft(f.E, false)
	f.fft(f.Y, false)

	// How much of the echo is left in the error: the leak, from how the error's spectrum moves with
	// the echo estimate's.
	beta0, betaMax := 2*float64(b)/16000, .5*float64(b)/16000
	beta := min(beta0*syy/see, betaMax)
	const specAvg = 0.05
	var pey, pyy float64
	for k := 0; k <= h; k++ {
		e := real(f.E[k] * cmplx.Conj(f.E[k]))
		y := real(f.Y[k] * cmplx.Conj(f.Y[k]))
		f.eh[k] = (1-specAvg)*f.eh[k] + specAvg*e
		f.yh[k] = (1-specAvg)*f.yh[k] + specAvg*y
		de, dy := e-f.eh[k], y-f.yh[k]
		pey += de * dy
		pyy += dy * dy
	}
	f.pey = (1-beta)*f.pey + beta*pey
	f.pyy = (1-beta)*f.pyy + beta*pyy
	if f.pyy < tiny {
		f.pyy = tiny
	}
	f.pey = max(f.pey, .005*f.pyy)
	f.leak = min(f.pey/f.pyy, 1)

	rer := (.0001*sxx + 3*f.leak*syy) / see
	if lo := sey * sey / (1 + see*syy); rer < lo {
		rer = lo
	}
	rer = min(rer, .5)

	if !f.adapted && f.sumAdapt > float64(f.parts) && f.leak > .03 {
		f.adapted = true
	}
	if f.adapted {
		for k := 0; k <= h; k++ {
			e := real(f.E[k]*cmplx.Conj(f.E[k])) + tiny
			r := f.leak * real(f.Y[k]*cmplx.Conj(f.Y[k]))
			r = min(r, .5*e)
			r = .7*r + .3*rer*e
			f.p1[k] = r / (e * (f.powSum[k] + freqReg*float64(n)))
		}
	} else {
		rate := 0.0
		if sxx > float64(b)*1e-6 { // the reference above about -60 dBFS
			rate = min(.25*sxx, .25*see) / see
		}
		for k := 0; k <= h; k++ {
			f.p1[k] = rate / (f.powSum[k] + freqReg*float64(n))
		}
		f.sumAdapt += rate
	}

	// The update, every partition; one of them a block has its gradient constrained to the filter's
	// length (SpeexDSP's alternating constraint), the rest unconstrained.
	constrain := f.block % f.parts
	f.block++
	for p := 0; p < f.parts; p++ {
		X, W := f.x[(f.head+p)%f.parts], f.bg[p]
		if p != constrain {
			for k := 0; k <= h; k++ {
				W[k] += complex(f.p1[k], 0) * cmplx.Conj(X[k]) * f.E[k]
			}
			for k := 1; k < h; k++ {
				W[n-k] = cmplx.Conj(W[k])
			}
			continue
		}
		for k := 0; k <= h; k++ {
			f.T[k] = W[k] + complex(f.p1[k], 0)*cmplx.Conj(X[k])*f.E[k]
		}
		for k := 1; k < h; k++ {
			f.T[n-k] = cmplx.Conj(f.T[k])
		}
		f.fft(f.T, true)
		for i := b; i < n; i++ {
			f.T[i] = 0
		}
		for i := 0; i < b; i++ {
			f.T[i] = complex(real(f.T[i]), 0)
		}
		f.fft(f.T, false)
		copy(W, f.T)
	}
}
