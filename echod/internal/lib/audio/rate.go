package audio

import (
	"encoding/binary"
	"math"

	"github.com/HuskerMinion/techo5/echod/internal/lib/vec"
)

// Whole-number rate changes, for a board whose hardware runs at a multiple of the rate the daemon
// works at: the 2nd gen Echo Show 8 captures at 96 and 48 kHz and plays at 96 kHz, where every other
// board hands over 16 kHz microphones and takes 48 kHz playback as they are.
//
// Both filters are windowed sincs (Blackman: about 74 dB down in the stopband) with the cutoff at the
// lower rate's Nyquist frequency. The transition band is 5.5 times the higher rate over the tap count
// wide, centered on the cutoff.

// lowpass is a Blackman-windowed sinc of taps coefficients, cutoff as a fraction of the sample rate,
// with its DC gain set to gain.
func lowpass(taps int, cutoff, gain float64) []float32 {
	h := make([]float64, taps)
	mid := float64(taps-1) / 2
	var sum float64
	for i := range h {
		x := float64(i) - mid
		s := 2 * cutoff
		if x != 0 {
			s = math.Sin(2*math.Pi*cutoff*x) / (math.Pi * x)
		}
		a := 2 * math.Pi * float64(i) / float64(taps-1)
		h[i] = s * (0.42 - 0.5*math.Cos(a) + 0.08*math.Cos(2*a))
		sum += h[i]
	}
	out := make([]float32, taps)
	for i := range h {
		out[i] = float32(h[i] / sum * gain)
	}
	return out
}

// Decimator takes one channel down by a whole factor: low-pass, then every factor-th sample. It keeps
// its history between calls, so a stream read in blocks comes out as one signal. The filter is only
// evaluated for the samples kept.
type Decimator struct {
	factor int
	h      []float32
	// hist holds the last len(h) inputs twice over, so the newest window is always one contiguous
	// slice: hist[at : at+len(h)], oldest first.
	hist  []float32
	at    int
	phase int
	// zeros counts the inputs in a row that were exactly zero, up to len(h): with the whole window
	// zero the output is zero, and silence (a speaker's reference with nothing playing) costs nothing.
	zeros int
}

// NewDecimator makes a decimator by factor with taps coefficients. Its delay is (taps-1)/2 input
// samples, so two channels at different rates line up when their tap counts are in the same ratio
// as their rates.
func NewDecimator(factor, taps int) *Decimator {
	return &Decimator{
		factor: factor,
		h:      lowpass(taps, 0.5/float64(factor), 1),
		hist:   make([]float32, 2*taps),
		zeros:  taps, // the history starts silent
	}
}

// Push takes one input sample and reports an output sample on every factor-th call.
func (d *Decimator) Push(x float32) (float32, bool) {
	n := len(d.h)
	if x != 0 {
		d.zeros = 0
	} else if d.zeros < n {
		d.zeros++
	}
	d.hist[d.at] = x
	d.hist[d.at+n] = x
	d.at++
	if d.at == n {
		d.at = 0
	}

	d.phase++
	if d.phase < d.factor {
		return 0, false
	}
	d.phase = 0

	if d.zeros == n {
		return 0, true
	}
	// h is symmetric, so the window's oldest-first order needs no reversing, and the product is
	// vec's (NEON on the boards that run this: the filters were most of an idle Show 8's CPU).
	return vec.Dot(d.h, d.hist[d.at:d.at+n]), true
}

// Reset forgets the history, for a stream that starts over.
func (d *Decimator) Reset() {
	clear(d.hist)
	d.at, d.phase, d.zeros = 0, 0, len(d.h)
}

// Upsampler takes interleaved 16-bit audio up by a whole factor: each input sample becomes factor
// outputs, the images between them filtered out. It keeps its history between calls.
type Upsampler struct {
	factor   int
	channels int
	// phases[p] is the polyphase branch for output p of each input: every factor-th coefficient, last
	// tap first, so it lines up with the window's oldest-first order for vec.Dot.
	phases [][]float32
	// hist is, per channel, the last len(phases[0]) inputs twice over, newest last (as in Decimator).
	hist [][]float32
	at   int
	// zeros counts the frames in a row that were silent on every channel, up to a branch's length:
	// with every window zero the output is silence, written without the products (the speaker's
	// stream stays open, and idle, between sounds).
	zeros   int
	silence []byte // one input frame's output, silent

	clipped uint64
}

// NewUpsampler makes an upsampler by factor for channels interleaved channels, with tapsPerPhase
// coefficients per output phase, scaled by gain (1 keeps the level).
func NewUpsampler(factor, tapsPerPhase, channels int, gain float64) *Upsampler {
	h := lowpass(factor*tapsPerPhase, 0.5/float64(factor), float64(factor)*gain)
	u := &Upsampler{factor: factor, channels: channels, zeros: tapsPerPhase,
		silence: make([]byte, 2*channels*factor)}
	for p := range factor {
		branch := make([]float32, tapsPerPhase)
		for k := range branch {
			branch[tapsPerPhase-1-k] = h[p+k*factor]
		}
		u.phases = append(u.phases, branch)
	}
	for range channels {
		u.hist = append(u.hist, make([]float32, 2*tapsPerPhase))
	}
	return u
}

// S16LE appends the upsampled form of in, interleaved S16_LE, to out and returns it.
func (u *Upsampler) S16LE(in, out []byte) []byte {
	k := len(u.phases[0])
	frameBytes := 2 * u.channels
	for f := 0; f+frameBytes <= len(in); f += frameBytes {
		silent := true
		for c := range u.channels {
			x := float32(int16(binary.LittleEndian.Uint16(in[f+2*c:])))
			u.hist[c][u.at] = x
			u.hist[c][u.at+k] = x
			silent = silent && x == 0
		}
		u.at++
		if u.at == k {
			u.at = 0
		}
		if !silent {
			u.zeros = 0
		} else if u.zeros < k {
			u.zeros++
		}
		if u.zeros == k {
			out = append(out, u.silence...)
			continue
		}
		for p := range u.factor {
			branch := u.phases[p]
			for c := range u.channels {
				// The branch's first tap belongs to the newest input, and the branch is stored reversed,
				// so it runs forward against the oldest-first window.
				y := vec.Dot(branch, u.hist[c][u.at:u.at+k])
				out = binary.LittleEndian.AppendUint16(out, uint16(u.clip(y)))
			}
		}
	}
	return out
}

func (u *Upsampler) clip(y float32) int16 {
	switch {
	case y > math.MaxInt16:
		u.clipped++
		return math.MaxInt16
	case y < math.MinInt16:
		u.clipped++
		return math.MinInt16
	}
	return int16(math.Round(float64(y)))
}

// Clipped counts output samples that came out past full scale.
func (u *Upsampler) Clipped() uint64 { return u.clipped }

// Reset forgets the history.
func (u *Upsampler) Reset() {
	for _, h := range u.hist {
		clear(h)
	}
	u.at, u.zeros = 0, len(u.phases[0])
}
