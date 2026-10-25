package audio

import "math"

// Converter changes interleaved 16-bit audio from one rate to another by a ratio of whole numbers:
// a station's 44.1 kHz to the speaker's 48 (160/147), or a 96 kHz file down to it (1/2).
//
// It is the textbook polyphase resampler: up by to/g, a low pass, down by from/g, with only the
// output samples that are kept ever computed. Drawing straight lines between neighboring samples
// instead folds the images of everything above a few kHz back into the treble as tones that are no
// harmonic of anything: a 10 kHz tone at 44.1 kHz came out with a ghost at 13.9 kHz only 21 dB under
// it, heard as grit on cymbals and sibilants. Here the audible ones are more than 70 dB down.
//
// The filter's cutoff sits at the slower rate's Nyquist frequency, with convertTaps taps for every
// sample at the slower rate, and more below 44.1 kHz (taps).
//
// The arithmetic is whole numbers: 16-bit samples against coefficients scaled by 1<<shift, summed in
// 32 bits. The Spot's cores ran the same filter in float32 at half the speed.
type Converter struct {
	up, down int // to/g and from/g
	channels int
	// phases[p] is the branch for an output that falls p/up of the way past its newest input, stored
	// oldest input first so it runs forwards against the history window.
	phases [][]int32
	shift  uint
	// hist is, per channel, the last k inputs twice over, newest last (as in Upsampler).
	hist [][]int32
	at   int
	// phase is where the next output falls past the newest input, in 1/up of an input sample.
	phase int

	clipped uint64
}

const (
	// convertTaps is the filter length per sample of the slower rate at 44.1 kHz and up: a
	// transition band 5.5/32 of that rate wide, centered on the cutoff. From 44.1 kHz the images
	// that get through it land between 22 and 26 kHz, and the ones above 24 fold to 22 to 24: all of
	// them above 20 kHz, where nobody hears them. Every tap is paid for on every output sample: on
	// the Spot, 48 taps in float32 cost 7.6 % of a core while music played, these 32 in whole numbers
	// 1.7 %, straight lines 0.55 %.
	convertTaps = 32

	// convertAudible is the rate below which that transition band would reach down into the treble.
	convertAudible = 44100

	// convertGain is 0.5 dB of headroom. Straight lines never went past the input's own peaks; a
	// filter overshoots the peaks between samples that music mastered to full scale carries, and
	// would clip them.
	convertGain = 0.944

	// convertMostPhases bounds the table: up is 160 for 44.1 kHz and 640 for 11.025, the worst of
	// the usual rates. A rate with no small ratio to the output gets no converter (NewConverter).
	convertMostPhases = 640
)

// NewConverter makes a converter from one rate to another for channels interleaved channels, or
// returns nil when the ratio needs more than convertMostPhases phases (no common rate does).
func NewConverter(from, to, channels int) *Converter {
	if from <= 0 || to <= 0 || channels <= 0 {
		return nil
	}
	g := gcd(from, to)
	up, down := to/g, from/g
	if up > convertMostPhases {
		return nil
	}
	k := taps(from, to)
	// The prototype runs at up*from; its cutoff is half the slower rate, and its gain up makes up for
	// the zeros the upsampling would have put between the inputs.
	h := lowpass(up*k, 0.5*float64(min(from, to))/float64(up*from), float64(up)*convertGain)

	// The scale is as fine as the sums allow: a full-scale input against the branch with the most
	// weight in it must stay inside 32 bits.
	var most float64
	for p := range up {
		var sum float64
		for i := range k {
			sum += math.Abs(float64(h[p+i*up]))
		}
		most = max(most, sum)
	}
	shift := uint(15)
	for shift > 8 && 32768*most*float64(int(1)<<shift) >= 0.99*math.MaxInt32 {
		shift--
	}

	c := &Converter{up: up, down: down, channels: channels, shift: shift}
	for p := range up {
		branch := make([]int32, k)
		for i := range branch {
			branch[k-1-i] = int32(math.Round(float64(h[p+i*up]) * float64(int(1)<<shift)))
		}
		c.phases = append(c.phases, branch)
	}
	for range channels {
		c.hist = append(c.hist, make([]int32, 2*k))
	}
	return c
}

// taps is the length of each phase, an even count for dot's two sums. Per sample of the slower rate
// it is convertTaps, and more below convertAudible in proportion, so the transition band narrows
// with the rate: a 22.05 kHz station's images of its treble would otherwise land in the treble, a
// 10 kHz tone's at 12.05 kHz only 25 dB under it. Going down it is scaled by the ratio, since the
// filter runs at the input's rate.
func taps(from, to int) int {
	slower := min(from, to)
	k := max(convertTaps, (convertTaps*convertAudible+slower-1)/slower)
	if from > to {
		k = (k*from + to - 1) / to
	}
	return (k + 1) &^ 1
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// Run appends the converted form of in, interleaved, to out and returns it. It keeps its history
// between calls, so a stream fed in pieces of any size comes out as one signal.
func (c *Converter) Run(in, out []int16) []int16 {
	k := len(c.phases[0])
	for f := 0; f+c.channels <= len(in); f += c.channels {
		for ch := range c.channels {
			x := int32(in[f+ch])
			c.hist[ch][c.at] = x
			c.hist[ch][c.at+k] = x
		}
		c.at++
		if c.at == k {
			c.at = 0
		}
		for ; c.phase < c.up; c.phase += c.down {
			branch := c.phases[c.phase]
			if c.channels == 2 {
				l, r := dot2(branch, c.hist[0][c.at:c.at+k], c.hist[1][c.at:c.at+k])
				out = append(out, c.clip(l), c.clip(r))
				continue
			}
			for ch := range c.channels {
				out = append(out, c.clip(dot(branch, c.hist[ch][c.at:c.at+k])))
			}
		}
		c.phase -= c.up
	}
	return out
}

// dot is the sum of h[i]*x[i] in two running sums: each add waits for the one before it, and two
// chains keep the core busy where one leaves it idle.
func dot(h, x []int32) int32 {
	x = x[:len(h)]
	var a, b int32
	for i := 0; i+1 < len(h); i += 2 {
		a += h[i] * x[i]
		b += h[i+1] * x[i+1]
	}
	return a + b
}

// dot2 is dot on two channels at once, each coefficient loaded once for both.
func dot2(h, x, y []int32) (int32, int32) {
	x, y = x[:len(h)], y[:len(h)]
	var a, b int32
	for i, coef := range h {
		a += coef * x[i]
		b += coef * y[i]
	}
	return a, b
}

// clip rounds a sum back to a sample, half up, and holds it to full scale.
func (c *Converter) clip(sum int32) int16 {
	v := (sum + 1<<(c.shift-1)) >> c.shift
	switch {
	case v > math.MaxInt16:
		c.clipped++
		return math.MaxInt16
	case v < math.MinInt16:
		c.clipped++
		return math.MinInt16
	}
	return int16(v)
}

// Clipped counts output samples that came out past full scale: a filter overshoots a transient by a
// few percent, so music mastered to full scale can.
func (c *Converter) Clipped() uint64 { return c.clipped }

// Delay is how far the output lags the input, in output samples.
func (c *Converter) Delay() int {
	return (c.up*len(c.phases[0]) - 1) / 2 / c.down
}
