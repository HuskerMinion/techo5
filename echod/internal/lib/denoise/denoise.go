// Package denoise takes steady noise out of a microphone stream: fan hum, air conditioning, the room
// itself. What survives is speech and whatever is as sudden as speech.
//
// This is the log-MMSE estimator with speech-presence uncertainty, from Loizou's Speech Enhancement:
// Theory and Practice — the same family speexdsp's preprocessor uses. Three parts work together, and
// leaving any of them out is what makes a denoiser sound worse than the noise it removed:
//
//   - the a priori SNR is a decision-directed estimate, blending the last frame's clean estimate with
//     this frame's measurement, so the gain does not swing frame to frame;
//   - the gain itself minimizes error in the log spectrum rather than the spectrum, which is closer to
//     how loudness is heard;
//   - a band the estimator thinks holds no speech falls to a floor rather than to zero, because a band
//     switching between silence and sound is what musical noise is.
//
// The noise spectrum is measured over the opening frames and then only updated when the frame looks
// like noise, so speech does not raise the estimate of what to subtract.
package denoise

import (
	"math"

	"github.com/HuskerMinion/techo5/echod/internal/lib/fft"
)

// The reference's parameters, kept at its names and values so the two can be compared.
const (
	// alpha weights the previous frame's clean estimate in the decision-directed a priori SNR.
	alpha = 0.98

	// smooth weights the held noise estimate when a frame is judged to be noise.
	smooth = 0.98

	// vad is how many dB the frame has to be over the noise estimate before it counts as speech and
	// stops updating that estimate.
	vad = 3.0

	// absent is the assumed probability that a band holds no speech.
	absent = 0.3

	// maxPost caps the posterior SNR, which is a ratio of measurements and so unbounded.
	maxPost = 40.0

	// minPrior floors the a priori SNR at -25 dB, and minGain the gain a band judged empty falls to.
	minPrior = 10.0 / 316.22776601683796 // -25 dB
	// minGain is an amplitude gain, as every gain here is (apply scales the spectrum by it): 0.1 is
	// -20 dB. It was 0.01, -40 dB, which took a quiet room below one sample step, so between words a
	// request held runs of exact digital silence that leveling could not bring back (#662).
	minGain = 0.1 // -20 dB

	// logMinGain is ln(minGain), which the gain is worked in.
	logMinGain = -2.302585092994046
)

// Filter is one stream. It is not safe for concurrent use.
type Filter struct {
	f    *fft.FFT
	size int // transform length
	// frame is the window in samples and hop half of it.
	frame int
	hop   int

	window []float64
	gain   float64 // overlap-add normalization for this window

	// noise is the held noise power per bin, and clean the previous frame's estimate, which the
	// decision-directed rule needs.
	noise []float64
	clean []float64

	spectrum []complex64
	tail     []float64 // the previous frame's second half, waiting to be added

	// opening counts the frames the noise estimate is being measured over, before any filtering.
	opening int
	started bool
}

// opening is how many frames the noise estimate is measured over before filtering starts. The
// reference uses five, on the assumption that a stream opens on the room rather than on a word.
const openingFrames = 5

// silentPower is a frame's total power, summed over its bins, below which it is digital silence rather
// than a quiet room: less than one sample step's worth per bin. A hardware mute and a suspended codec
// both produce it, for as long as they last.
const silentPower = 1.0

// New makes a filter for audio at rate, with a transform long enough for a 20 ms window.
func New(rate int) *Filter {
	frame := 20 * rate / 1000
	size := 1
	for size < frame {
		size <<= 1
	}
	// The reference oversamples: a 160-sample window at 8 kHz goes into a 512-point transform. Keeping
	// that means the gain is computed on the same bins it was tuned on.
	size *= 2

	f := &Filter{
		f:        fft.New(size),
		size:     size,
		frame:    frame,
		hop:      frame / 2,
		window:   make([]float64, frame),
		noise:    make([]float64, size),
		clean:    make([]float64, size),
		spectrum: make([]complex64, size),
		tail:     make([]float64, frame/2),
	}

	var sum float64
	for n := range f.window {
		f.window[n] = 0.54 - 0.46*math.Cos(2*math.Pi*float64(n)/float64(frame-1))
		sum += f.window[n]
	}
	f.gain = float64(f.hop) / sum
	return f
}

// Frame is the window this filter reads at a time, and Hop how far it advances.
func (f *Filter) Frame() int { return f.frame }
func (f *Filter) Hop() int   { return f.hop }

// Forget drops what the filter learned about the room, so the next frames measure it again.
func (f *Filter) Forget() {
	clear(f.noise)
	clear(f.clean)
	clear(f.tail)
	f.opening, f.started = 0, false
}

// Push filters one window of frame samples and returns hop samples of output, which lag the input by
// one hop. The caller advances by hop and keeps the window overlapping.
func (f *Filter) Push(in []float64, out []float64) {
	for n := range f.frame {
		f.spectrum[n] = complex(float32(in[n]*f.window[n]), 0)
	}
	clear(f.spectrum[f.frame:])
	f.f.Forward(f.spectrum)

	// The opening frames measure the room and are not filtered.
	if f.opening < openingFrames {
		f.opening++
		for k := range f.size {
			f.noise[k] += binPower(f.spectrum[k]) / openingFrames
		}
		copy(out, in[:f.hop])
		return
	}

	f.apply()
	f.f.Inverse(f.spectrum)

	for n := range f.hop {
		out[n] = (f.tail[n] + float64(real(f.spectrum[n]))) * f.gain
	}
	for n := range f.hop {
		f.tail[n] = float64(real(f.spectrum[f.hop+n]))
	}
	f.started = true
}

// apply replaces each bin with its estimate of the speech there, keeping the noisy phase.
func (f *Filter) apply() {
	// Whether this frame looks like noise decides if the estimate follows it.
	var signal, held float64
	for k := range f.size {
		signal += binPower(f.spectrum[k])
		held += f.noise[k]
	}
	// Digital silence says nothing about the room, so the estimates are left as they stood and the frame
	// goes out silent. Followed instead, the noise estimate decays toward zero for as long as the silence
	// lasts: after minutes the returning room reads as speech and is never learned again, and after an
	// hour the estimate underflows and every bin turns non-finite for good (the mute on a Show, 2026-09).
	if signal < silentPower*float64(f.size) {
		for k := range f.size {
			f.spectrum[k] = 0
		}
		return
	}
	speech := held > 0 && 10*math.Log10(signal/held) >= vad

	// The input is real, so bin size-k mirrors bin k: the same magnitude, and so the same noise, the
	// same estimate and the same gain. Each pair is worked out once, on the lower half.
	half := f.size / 2
	for k := 0; k <= half; k++ {
		g, scale := f.bin(k, speech)
		if scale {
			f.spectrum[k] *= complex(float32(g), 0)
		}
		if k == 0 || k == half {
			continue
		}
		j := f.size - k
		f.noise[j], f.clean[j] = f.noise[k], f.clean[k]
		if scale {
			f.spectrum[j] *= complex(float32(g), 0)
		}
	}
}

// bin moves bin k's estimates on by one frame and returns the gain for it, or false where the bin is
// to be left as it is.
func (f *Filter) bin(k int, speech bool) (float64, bool) {
	power := binPower(f.spectrum[k])

	// Both SNRs are measured against the noise as it stood, and the estimate moves afterwards, so a
	// frame is never judged against a floor it just raised itself.
	held := f.noise[k]
	if !speech {
		f.noise[k] = smooth*held + (1-smooth)*power
	}
	if held <= 0 {
		return 0, false
	}

	post := min(power/held, maxPost)

	prior := alpha + (1-alpha)*max(post-1, 0)
	if f.started {
		prior = max(minPrior, alpha*f.clean[k]/held+(1-alpha)*max(post-1, 0))
	}

	// The log-MMSE gain, and then the weight given to it by how likely the band holds speech.
	a := prior / (1 + prior)
	v := a * post
	// E1 diverges at zero, and the Inf*0 that follows would sit in clean for good.
	if v <= 0 {
		f.clean[k] = 0
		return 0, false
	}
	present := (1 - absent) / (1 - absent + absent*(1+prior)*math.Exp(-v))
	// The gain is lsa^present · minGain^(1-present), with lsa = a·exp(E1(v)/2). Worked in logs it is one
	// exp and one log rather than four, which matters at every bin of every frame.
	g := math.Exp(present*(math.Log(a)+0.5*e1(v)) + (1-present)*logMinGain)

	f.clean[k] = g * g * power
	// Whatever else ever produces a non-finite value, it must not stay: this bin starts again from what
	// it hears now.
	if math.IsNaN(f.clean[k]) || math.IsInf(f.clean[k], 0) || math.IsNaN(f.noise[k]) || math.IsInf(f.noise[k], 0) {
		f.clean[k], f.noise[k] = 0, power
		return 0, false
	}
	return g, true
}

// binPower is a bin's squared magnitude. The parts are float32, so their squares cannot overflow a
// float64, and the square root a magnitude needs is never taken.
func binPower(c complex64) float64 {
	r, i := float64(real(c)), float64(imag(c))
	return r*r + i*i
}
