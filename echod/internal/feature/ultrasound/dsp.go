// Package ultrasound is presence detection by ultrasound, as Fire OS does it on the Echo Dot 5th gen
// (USPD, Alexa.MotionSensor): the speaker plays a steady tone above hearing, the microphones hear it
// come back off the room, and somebody moving shifts the part that came off them by a few hundred
// hertz (Doppler). What leaves the package is a Motion sensor for Home Assistant, on or off.
//
// Fire OS runs all of it on the HiFi5 DSP (dsp_cancheese.bin renders the tone and hands libasp.so an
// 8 kHz baseband of four channels, which it takes to 2 kHz; asp.cfg "USPD Baseband"), and none of that
// runs here: the microphones come off the converter at 96 kHz (hardware/mic) and the speaker goes out
// at 96 kHz (hardware/speaker), so both sides are done in echod. The tone's frequency is not in any
// file Fire OS reads, so it is chosen on the unit (probe): the candidate the microphones hear best. Its
// level is the headroom Fire OS keeps for it under the limiters (asp's Tuning.UltrasoundLevel).
//
// Nothing of what the microphones hear is kept: a 2 kHz baseband around the tone, 128 ms at a time,
// reduced to two numbers per block, and the mute button stops it with the microphones.
package ultrasound

import (
	"encoding/binary"
	"math"
	"math/cmplx"

	"github.com/HuskerMinion/techo5/echod/internal/lib/fft"
)

// The baseband: 2 kHz complex, as Fire OS's own ("USPD Baseband" output 2000 Hz), in blocks of
// blockN with a new one every hop.
const (
	baseRate = 2000
	blockN   = 256
	hop      = 128
	binHz    = float64(baseRate) / blockN
)

// The bands of a block's spectrum, in hertz either side of the tone. Walking is 0.3 to 1.5 m/s, a
// Doppler shift of 2v/c of the tone: 50 to 280 Hz at 32 kHz, and an arm or a turn of the head is
// slower. Above motionTo nobody moves fast enough to be there, so what is in noiseFrom..noiseTo is the
// noise the motion band is measured against: whatever leaks through from the music or the room
// raises both together.
const (
	motionFrom = 12.0
	motionTo   = 400.0
	noiseFrom  = 500.0
	noiseTo    = 900.0
)

// biquad is one second-order section, transposed direct form II.
type biquad struct {
	b0, b1, b2, a1, a2 float32
	z1, z2             float32
}

func (f *biquad) step(x float32) float32 {
	y := f.b0*x + f.z1
	f.z1 = f.b1*x - f.a1*y + f.z2
	f.z2 = f.b2*x - f.a2*y
	return y
}

// highpass is a second-order Butterworth-family high-pass section at fc with quality q.
func highpass(fc, q float64, rate int) biquad {
	w := 2 * math.Pi * fc / float64(rate)
	alpha := math.Sin(w) / (2 * q)
	c := math.Cos(w)
	a0 := 1 + alpha
	return biquad{
		b0: float32((1 + c) / 2 / a0), b1: float32(-(1 + c) / a0), b2: float32((1 + c) / 2 / a0),
		a1: float32(-2 * c / a0), a2: float32((1 - alpha) / a0),
	}
}

// demod takes one microphone from the converter's rate down to the baseband around hz: everything
// under the audio band's edge is cut first (a fourth-order high-pass at 18 kHz, which music and
// speech do not get through), then the tone is mixed down to 0 Hz, summed in groups of first (nulls
// at every multiple of rate/first, which is where the next stage would alias from), and low-passed
// and decimated the rest of the way.
type demod struct {
	hp    [2]biquad
	cos   []float32
	sin   []float32
	at    int
	first int
	sum   complex64
	n     int
	lp    []float32 // low-pass at rate/first, cut at the baseband's Nyquist
	hist  []complex64
	hpos  int
	keep  int // decimation after the first stage
	kn    int
}

// newDemod is a demodulator for hz at rate, or nil where the rate is not a whole multiple of the
// baseband's or the tone cannot loop exactly.
func newDemod(hz, rate int) *demod {
	if rate%baseRate != 0 || 2*hz >= rate || hz <= 0 {
		return nil
	}
	n := rate / gcdInt(rate, hz)
	if n > 4096 {
		return nil
	}
	d := &demod{
		hp: [2]biquad{highpass(18000, 0.5412, rate), highpass(18000, 1.3066, rate)},
	}
	d.cos, d.sin = make([]float32, n), make([]float32, n)
	for i := range n {
		a := 2 * math.Pi * float64(hz) * float64(i) / float64(rate)
		d.cos[i], d.sin[i] = float32(math.Cos(a)), float32(-math.Sin(a))
	}
	// First stage to some 12 kHz (8 at 96 kHz), the rest by a windowed-sinc low-pass.
	total := rate / baseRate
	d.first = 1
	for _, f := range []int{8, 6, 4, 3, 2} {
		if total%f == 0 && total/f >= 2 {
			d.first = f
			break
		}
	}
	d.keep = total / d.first
	mid := float64(rate / d.first)
	taps := 16 * d.keep
	d.lp = make([]float32, taps)
	cut := 0.5 * baseRate / mid // the baseband's Nyquist, as a fraction of mid's rate
	var sum float64
	for i := range taps {
		x := float64(i) - float64(taps-1)/2
		s := 2 * cut
		if x != 0 {
			s = math.Sin(2*math.Pi*cut*x) / (math.Pi * x)
		}
		w := 0.42 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(taps-1)) + 0.08*math.Cos(4*math.Pi*float64(i)/float64(taps-1))
		d.lp[i] = float32(s * w)
		sum += s * w
	}
	for i := range d.lp {
		d.lp[i] /= float32(sum)
	}
	d.hist = make([]complex64, taps)
	return d
}

func gcdInt(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// push takes one sample and says whether a baseband sample came out of it.
func (d *demod) push(x float32) (complex64, bool) {
	x = d.hp[1].step(d.hp[0].step(x))
	d.sum += complex(x*d.cos[d.at], x*d.sin[d.at])
	if d.at++; d.at == len(d.cos) {
		d.at = 0
	}
	if d.n++; d.n < d.first {
		return 0, false
	}
	v := d.sum / complex(float32(d.first), 0)
	d.sum, d.n = 0, 0

	d.hist[d.hpos] = v
	if d.hpos++; d.hpos == len(d.hist) {
		d.hpos = 0
	}
	if d.kn++; d.kn < d.keep {
		return 0, false
	}
	d.kn = 0
	var y complex64
	j := d.hpos
	for _, c := range d.lp {
		y += d.hist[j] * complex(c, 0)
		if j++; j == len(d.hist) {
			j = 0
		}
	}
	return y * 2, true // the mix halves the tone's amplitude
}

// reading is what one block of one microphone's baseband says.
type reading struct {
	// carrier is the tone as heard, in dB over the noise in a single bin: how well this microphone
	// hears the tone at all (the speaker's own path, mostly, which does not move).
	carrier float64
	// motion is the motion band over the noise band, per bin, in dB: about 0 in a still room.
	motion float64
}

// analyzer turns one microphone's baseband into readings, a block at a time.
type analyzer struct {
	f      *fft.FFT
	win    []float32
	winSum float64 // Σw, the gain a constant gets
	winPow float64 // Σw², the gain noise gets
	ring   []complex64
	pos    int
	since  int
	dc     complex64
	fill   int
	work   []complex64
}

func newAnalyzer() *analyzer {
	a := &analyzer{f: fft.New(blockN), win: make([]float32, blockN), ring: make([]complex64, blockN),
		work: make([]complex64, blockN)}
	for i := range a.win {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/blockN)
		a.win[i] = float32(w)
		a.winSum += w
		a.winPow += w * w
	}
	return a
}

// dcCo is how fast the still part of what is heard is followed: some 0.4 s. That part is the tone
// straight off the speaker and off the walls; taken out before the transform, its leakage does not
// fill the bins next to it, where slow movement is.
var dcCo = float32(1 - math.Exp(-1/(0.4*baseRate)))

// push takes one baseband sample, and gives a reading every hop once a whole block is in.
func (a *analyzer) push(v complex64) (reading, bool) {
	a.dc += (v - a.dc) * complex(dcCo, 0)
	a.ring[a.pos] = v - a.dc
	if a.pos++; a.pos == blockN {
		a.pos = 0
	}
	if a.fill < blockN+int(2*baseRate) { // the block, and two seconds for the still part to settle
		a.fill++
	}
	if a.since++; a.since < hop || a.fill < blockN+int(2*baseRate) {
		return reading{}, false
	}
	a.since = 0
	for i := range blockN {
		a.work[i] = a.ring[(a.pos+i)%blockN] * complex(a.win[i], 0)
	}
	a.f.Forward(a.work)

	band := func(from, to float64) float64 {
		var s float64
		n := 0
		for k := int(math.Ceil(from / binHz)); float64(k)*binHz <= to && k < blockN/2; k++ {
			for _, b := range [2]int{k, blockN - k} {
				p := cmplx.Abs(complex128(a.work[b]))
				s += p * p
				n++
			}
		}
		return s / float64(max(n, 1))
	}
	noise := band(noiseFrom, noiseTo)
	motion := band(motionFrom, motionTo)
	if noise <= 0 {
		noise = 1e-30
	}
	// The tone's power as one bin would hold it, against the noise in one bin.
	c := cmplx.Abs(complex128(a.dc)) * a.winSum
	return reading{
		carrier: 10 * math.Log10(max(c*c, 1e-30)/noise),
		motion:  10 * math.Log10(max(motion, 1e-30)/noise),
	}, true
}

// listener is the whole receive side: one demodulator and analyzer per microphone, fed raw capture.
type listener struct {
	hz, rate int
	mics     []*demod
	an       []*analyzer
	got      []reading
	have     []bool
}

func newListener(hz, rate, mics int) *listener {
	l := &listener{hz: hz, rate: rate}
	for range mics {
		d := newDemod(hz, rate)
		if d == nil {
			return nil
		}
		l.mics = append(l.mics, d)
		l.an = append(l.an, newAnalyzer())
	}
	l.got, l.have = make([]reading, mics), make([]bool, mics)
	return l
}

// feed takes interleaved little-endian signed PCM, channels wide, width bytes a sample, the first
// len(l.mics) channels microphones at full scale full, and calls out with each block's readings: the
// microphone that hears the tone best for the carrier, and the one that sees the most movement.
func (l *listener) feed(pcm []byte, channels, width int, full float32, out func(reading)) {
	step := channels * width
	for at := 0; at+step <= len(pcm); at += step {
		for m, d := range l.mics {
			var x float32
			b := pcm[at+m*width:]
			switch width {
			case 2:
				x = float32(int16(binary.LittleEndian.Uint16(b)))
			case 4:
				x = float32(int32(binary.LittleEndian.Uint32(b)))
			default:
				return
			}
			y, ok := d.push(x / full)
			if !ok {
				continue
			}
			if r, ok := l.an[m].push(y); ok {
				l.got[m], l.have[m] = r, true
			}
		}
		if !l.have[len(l.have)-1] {
			continue
		}
		best := reading{carrier: math.Inf(-1), motion: math.Inf(-1)}
		for m := range l.got {
			best.carrier = max(best.carrier, l.got[m].carrier)
			best.motion = max(best.motion, l.got[m].motion)
			l.have[m] = false
		}
		out(best)
	}
}
