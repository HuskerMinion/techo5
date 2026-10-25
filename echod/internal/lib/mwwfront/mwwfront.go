// Package mwwfront is microWakeWord's feature front end, bit for bit: TensorFlow Lite Micro's audio front
// end (tensorflow/lite/experimental/microfrontend/lib) with the settings microWakeWord trains with. 30 ms
// Hann windows, a 512 point 16 bit fixed point FFT (KISS FFT), 40 mel channels from 125 to 7500 Hz,
// noise reduction (smoothing bits 10, even channels 0.025, odd 0.06, min signal 0.05), PCAN gain control
// (strength 0.95, offset 80, gain bits 21) and the log scale (shift 6). Everything per window is integer
// arithmetic, as in the original, and the tables are made in single precision as its current version
// makes them, so the features are the ones the models were trained on.
//
// Ported to Go from mww_features.c in echo-dot-assist, https://github.com/Gamer92000/echo-dot-assist
// (commit b9ed1848), Copyright (c) 2026 Julian Imhof (Gamer92000), MIT License. That code is derived from
// TensorFlow Lite Micro's microfrontend, Copyright 2018 The TensorFlow Authors, Apache License 2.0, and
// from KISS FFT's fixed point real FFT, Copyright (c) 2003-2010 Mark Borgerding, BSD 3-Clause License.
// The license texts are in the repository's NOTICE file.
//
// Pure Go on purpose: the daemon builds for 32-bit ARM, arm64 and Android without cgo.
package mwwfront

import "math"

const (
	// Channels is the number of features per window.
	Channels = 40

	// SampleRate is the audio it reads.
	SampleRate = 16000

	// WindowMs is the length of a window. The step between windows is the model's.
	WindowMs = 30

	windowLen = SampleRate * WindowMs / 1000

	windowBits      = 12
	filterbankBits  = 12
	nrBits          = 14 // noise reduction's fixed point
	nrSmoothingBits = 10
	pcanSNRBits     = 12
	pcanOutputBits  = 6
	logScaleLog2    = 16
	logSegmentsLog2 = 7
	logCoeff        = 45426 // ln(2) << 16
	logScaleShift   = 6
	fftN            = 512 // real FFT: a 256 point complex one, radix 4 throughout
	fftC            = fftN / 2

	// Noise reduction's smoothing per channel, its minimum signal, all in nrBits fixed point. The C
	// truncates 0.025 * 16384 and so on to integers.
	nrEven   = 409 // 0.025
	nrOdd    = 983 // 0.06
	nrMinRem = 819 // 0.05
)

// Frontend turns 30 ms windows of 16 kHz audio into features. It keeps the noise estimates between
// windows, so windows have to come in order, one step apart. Not safe for concurrent use.
type Frontend struct {
	stepMs int

	estimate [Channels]uint32 // noise reduction's noise estimate per channel

	out [Channels]uint16
	win [fftN]int16
	tmp [fftC]cpx
	fq  [fftC + 1]cpx
	nrg [fftC + 1]int32
}

// New makes a front end for a model whose feature step is stepMs (zero is StepMs's default).
func New(stepMs int) *Frontend {
	return &Frontend{stepMs: StepMs(stepMs)}
}

// StepMs is the feature step a model runs at, given the one its manifest says: zero, a manifest that
// does not say, is 20 ms, what github.com/zserge/microwakeword's NewDetector takes it to mean. The
// daemon, the tools and the replay tests all go by this, so a model gets the same step everywhere.
func StepMs(stepMs int) int {
	if stepMs <= 0 {
		return 20
	}
	return stepMs
}

// WindowSamples is how many samples ProcessFrame reads.
func (f *Frontend) WindowSamples() int { return windowLen }

// StepSamples is how far apart windows start.
func (f *Frontend) StepSamples() int { return SampleRate * f.stepMs / 1000 }

// Reset forgets the noise estimates.
func (f *Frontend) Reset() { f.estimate = [Channels]uint32{} }

// ProcessFrame takes one window of WindowSamples samples and returns its features, 0 to about 670 each.
// The slice is the front end's own and is overwritten by the next call.
func (f *Frontend) ProcessFrame(samples []int16) []uint16 {
	t := tab
	samples = samples[:windowLen]

	// The window, scaled up for the FFT's resolution as far as the loudest sample allows.
	win := &f.win
	var maxabs int16
	for i := 0; i < windowLen; i++ {
		v := int16((int32(samples[i]) * int32(t.coef[i])) >> windowBits)
		win[i] = v
		if v < 0 {
			v = -v
		}
		if v > maxabs {
			maxabs = v
		}
	}
	shift := 15 - msb32(uint32(maxabs))
	for i := 0; i < windowLen; i++ {
		win[i] = int16(uint16(win[i]) << shift)
	}
	for i := windowLen; i < fftN; i++ {
		win[i] = 0
	}
	f.fftr()

	// Filterbank: energies, weighted into channels. The bins below start have zero weights.
	energy := &f.nrg
	*energy = [fftC + 1]int32{}
	for i := t.start; i < t.end; i++ {
		re, im := int32(f.fq[i].r), int32(f.fq[i].i)
		energy[i] = re*re + im*im // wraps as the C's does
	}
	var work [Channels + 1]uint64
	var wacc, uacc uint64
	for c := 0; c < Channels+1; c++ {
		fs, ws := int(t.chanFreqStart[c]), int(t.chanWeightStart[c])
		for j := 0; j < int(t.chanWidth[c]); j++ {
			mag := uint64(int64(energy[fs+j]))
			wacc += uint64(int64(t.weights[ws+j])) * mag
			uacc += uint64(int64(t.unweights[ws+j])) * mag
		}
		work[c] = wacc
		wacc, uacc = uacc, 0
	}
	var sig [Channels]uint32
	for c := 0; c < Channels; c++ {
		sig[c] = sqrt64(work[c+1]) >> shift
	}

	// Noise reduction.
	for c := 0; c < Channels; c++ {
		sm := uint32(nrEven)
		if c&1 != 0 {
			sm = nrOdd
		}
		up := sig[c] << nrSmoothingBits
		est := uint32((uint64(up)*uint64(sm) + uint64(f.estimate[c])*uint64((1<<nrBits)-sm)) >> nrBits)
		f.estimate[c] = est
		if est > up {
			est = up
		}
		floor := uint32((uint64(sig[c]) * nrMinRem) >> nrBits)
		sub := (up - est) >> nrSmoothingBits
		sig[c] = max(sub, floor)
	}

	// PCAN.
	for c := 0; c < Channels; c++ {
		gain := uint32(int32(wideDynamic(f.estimate[c])))
		snr := uint32((uint64(sig[c]) * uint64(gain)) >> t.snrShift)
		if snr < 2<<pcanSNRBits {
			sig[c] = (snr * snr) >> (2 + 2*pcanSNRBits - pcanOutputBits)
		} else {
			sig[c] = (snr >> (pcanSNRBits - pcanOutputBits)) - (1 << pcanOutputBits)
		}
	}

	// Log.
	for c := 0; c < Channels; c++ {
		v := sig[c] << t.correction
		if v > 1 {
			v = logScaled(v)
		} else {
			v = 0
		}
		f.out[c] = uint16(min(v, 0xFFFF))
	}
	return f.out[:]
}

// Quantize turns features into the int8 a microWakeWord model reads, as ESPHome and microWakeWord do.
func Quantize(raw []uint16, out []int8) {
	for i, v := range raw {
		q := (int32(v)*256+333)/666 - 128
		out[i] = int8(max(-128, min(127, q)))
	}
}

func msb32(x uint32) int {
	if x == 0 {
		return 0
	}
	n := 0
	for x != 0 {
		x >>= 1
		n++
	}
	return n
}

func msb64(x uint64) int {
	if x>>32 != 0 {
		return 32 + msb32(uint32(x>>32))
	}
	return msb32(uint32(x))
}

func sqrt32(num uint32) uint32 {
	if num == 0 {
		return 0
	}
	var res uint32
	maxb := (32 - msb32(num)) | 1
	bit := uint32(1) << (31 - maxb)
	for it := (31-maxb)/2 + 1; it > 0; it-- {
		if num >= res+bit {
			num -= res + bit
			res = (res >> 1) + bit
		} else {
			res >>= 1
		}
		bit >>= 2
	}
	if num > res && res != 0xFFFF {
		res++
	}
	return uint32(uint16(res))
}

func sqrt64(num uint64) uint32 {
	if num>>32 == 0 {
		return sqrt32(uint32(num))
	}
	var res uint64
	maxb := (64 - msb64(num)) | 1
	bit := uint64(1) << (63 - maxb)
	for it := (63-maxb)/2 + 1; it > 0; it-- {
		if num >= res+bit {
			num -= res + bit
			res = (res >> 1) + bit
		} else {
			res >>= 1
		}
		bit >>= 2
	}
	if num > res && res != 0xFFFFFFFF {
		res++
	}
	return uint32(res)
}

// logLUT is log_lut.h: (log2(1 + k/128) - k/128) << 16.
var logLUT = [...]uint16{
	0, 224, 442, 654, 861, 1063, 1259, 1450, 1636, 1817, 1992, 2163, 2329, 2490, 2646, 2797, 2944, 3087, 3224,
	3358, 3487, 3611, 3732, 3848, 3960, 4068, 4172, 4272, 4368, 4460, 4549, 4633, 4714, 4791, 4864, 4934, 5001, 5063,
	5123, 5178, 5231, 5280, 5326, 5368, 5408, 5444, 5477, 5507, 5533, 5557, 5578, 5595, 5610, 5622, 5631, 5637, 5640,
	5641, 5638, 5633, 5626, 5615, 5602, 5586, 5568, 5547, 5524, 5498, 5470, 5439, 5406, 5370, 5332, 5291, 5249, 5203,
	5156, 5106, 5054, 5000, 4944, 4885, 4825, 4762, 4697, 4630, 4561, 4490, 4416, 4341, 4264, 4184, 4103, 4020, 3935,
	3848, 3759, 3668, 3575, 3481, 3384, 3286, 3186, 3084, 2981, 2875, 2768, 2659, 2549, 2437, 2323, 2207, 2090, 1971,
	1851, 1729, 1605, 1480, 1353, 1224, 1094, 963, 830, 695, 559, 421, 282, 142, 0}

func logScaled(x uint32) uint32 {
	integer := uint32(msb32(x)) - 1
	frac := int32(int64(x) - int64(1)<<integer)
	if integer < logScaleLog2 {
		frac <<= logScaleLog2 - integer
	} else {
		frac >>= integer - logScaleLog2
	}
	base := uint32(frac) >> (logScaleLog2 - logSegmentsLog2)
	const unit = (1 << logScaleLog2) >> logSegmentsLog2
	c0, c1 := int32(logLUT[base]), int32(logLUT[base+1])
	seg := int32(unit * base)
	rel := ((c1 - c0) * (frac - seg)) >> logScaleLog2
	fraction := uint32(frac + c0 + rel)
	log2 := (integer << logScaleLog2) + fraction
	const round = 1 << (logScaleLog2 - 1)
	loge := uint32((uint64(logCoeff)*uint64(log2) + round) >> logScaleLog2)
	return ((loge << logScaleShift) + round) >> logScaleLog2
}

// wideDynamic is PCAN's gain for a noise estimate, read off a piecewise quadratic table.
func wideDynamic(x uint32) int16 {
	lut := tab.gainLUT[:]
	if x <= 2 {
		return lut[x]
	}
	iv := int16(msb32(x))
	lut = lut[4*int(iv)-6:]
	var frac int16
	if iv < 11 {
		frac = int16((x << (11 - iv)) & 0x3FF)
	} else {
		frac = int16((x >> (iv - 11)) & 0x3FF)
	}
	r := (int32(lut[2]) * int32(frac)) >> 5
	r += int32(uint32(int32(lut[1])) << 5)
	r *= int32(frac)
	r = (r + (1 << 14)) >> 15
	r += int32(lut[0])
	return int16(r)
}

// ---------------------------------------------------------------- KISS FFT, FIXED_POINT 16

type cpx struct{ r, i int16 }

func sround(x int32) int16 { return int16((x + (1 << 14)) >> 15) }

func fixdiv(c *cpx, k int32) {
	c.r = sround(int32(c.r) * (32767 / k))
	c.i = sround(int32(c.i) * (32767 / k))
}

func cmul(a, b cpx) cpx {
	return cpx{
		r: sround(int32(a.r)*int32(b.r) - int32(a.i)*int32(b.i)),
		i: sround(int32(a.r)*int32(b.i) + int32(a.i)*int32(b.r)),
	}
}

func bfly4(out []cpx, fstride, m int) {
	tw := &tab.twiddles
	var s0, s1, s2, s3, s4, s5 cpx
	for k := 0; k < m; k++ {
		o := out[k:]
		fixdiv(&o[0], 4)
		fixdiv(&o[m], 4)
		fixdiv(&o[2*m], 4)
		fixdiv(&o[3*m], 4)
		s0 = cmul(o[m], tw[k*fstride])
		s1 = cmul(o[2*m], tw[2*k*fstride])
		s2 = cmul(o[3*m], tw[3*k*fstride])
		s5 = cpx{o[0].r - s1.r, o[0].i - s1.i}
		o[0].r += s1.r
		o[0].i += s1.i
		s3 = cpx{s0.r + s2.r, s0.i + s2.i}
		s4 = cpx{s0.r - s2.r, s0.i - s2.i}
		o[2*m] = cpx{o[0].r - s3.r, o[0].i - s3.i}
		o[0].r += s3.r
		o[0].i += s3.i
		o[m] = cpx{s5.r + s4.i, s5.i - s4.r}
		o[3*m] = cpx{s5.r - s4.i, s5.i + s4.r}
	}
}

// work is kf_work for 256 = 4 x 4 x 4 x 4: m is the stage's length / 4. in holds the real samples read as
// complex pairs; fi indexes it in pairs.
func work(out []cpx, in *[fftN]int16, fi, fstride, m int) {
	if m == 1 {
		for k := 0; k < 4; k++ {
			out[k] = cpx{in[2*fi], in[2*fi+1]}
			fi += fstride
		}
	} else {
		for k := 0; k < 4; k++ {
			work(out[k*m:], in, fi, fstride*4, m/4)
			fi += fstride
		}
	}
	bfly4(out, fstride, m)
}

// fftr is kiss_fftr: fftN real samples to fftC + 1 bins.
func (f *Frontend) fftr() {
	tmp, freq := &f.tmp, &f.fq
	work(tmp[:], &f.win, 0, 1, fftC/4)
	tdc := tmp[0]
	fixdiv(&tdc, 2)
	freq[0] = cpx{tdc.r + tdc.i, 0}
	freq[fftC] = cpx{tdc.r - tdc.i, 0}
	for k := 1; k <= fftC/2; k++ {
		fpk := tmp[k]
		fpnk := cpx{tmp[fftC-k].r, -tmp[fftC-k].i}
		fixdiv(&fpk, 2)
		fixdiv(&fpnk, 2)
		f1k := cpx{fpk.r + fpnk.r, fpk.i + fpnk.i}
		f2k := cpx{fpk.r - fpnk.r, fpk.i - fpnk.i}
		tw := cmul(f2k, tab.superTw[k-1])
		// The sums are taken at full width and halved before they are stored, as the C's int promotion does.
		freq[k] = cpx{int16((int32(f1k.r) + int32(tw.r)) >> 1), int16((int32(f1k.i) + int32(tw.i)) >> 1)}
		freq[fftC-k] = cpx{int16((int32(f1k.r) - int32(tw.r)) >> 1), int16((int32(tw.i) - int32(f1k.i)) >> 1)}
	}
}

// ---------------------------------------------------------------- tables

// tables is everything that does not change: made once, the same for every front end.
type tables struct {
	coef [windowLen]int16

	start, end                                int
	chanFreqStart, chanWeightStart, chanWidth [Channels + 1]int16
	weights, unweights                        [512]int16

	gainLUT    [4*32 - 3]int16
	snrShift   uint
	correction uint

	twiddles [fftC]cpx
	superTw  [fftC / 2]cpx
}

var tab = makeTables()

// The tables are single precision, as TFLite Micro makes them, from correctly rounded float functions
// (a double function rounded to float). Every product that is then added to is converted to float32
// explicitly: Go may otherwise fuse the two into one multiply-add (it does on arm64), which rounds
// once instead of twice and moves a weight by a step.
func log1pf(x float32) float32 { return float32(math.Log1p(float64(x))) }
func cosf(x float32) float32   { return float32(math.Cos(float64(x))) }
func powf(x, y float32) float32 {
	return float32(math.Pow(float64(x), float64(y)))
}

func mel(hz float32) float32 { return float32(1127.0 * log1pf(hz/700.0)) }

func cexp(phase float64) cpx {
	return cpx{
		r: int16(math.Floor(0.5 + float64(32767*math.Cos(phase)))),
		i: int16(math.Floor(0.5 + float64(32767*math.Sin(phase)))),
	}
}

func makeTables() *tables {
	t := &tables{}

	const pi = 3.141592653589793238462643383279502884197169399375105820974944
	for i := 0; i < fftC; i++ {
		t.twiddles[i] = cexp(-2 * pi * float64(i) / fftC)
	}
	for i := 0; i < fftC/2; i++ {
		t.superTw[i] = cexp(-3.14159265358979323846264338327 * (float64(i+1)/fftC + .5))
	}

	arg := float32(math.Pi) * 2.0 / float32(windowLen)
	for i := 0; i < windowLen; i++ {
		h := 0.5 - float32(0.5*cosf(float32(arg*(float32(i)+0.5))))
		t.coef[i] = int16(math.Floor(float64(float32(float32(h*(1<<windowBits)) + 0.5))))
	}

	if !t.filterbank() {
		panic("mwwfront: filterbank does not fit") // cannot happen: the settings are constants
	}
	t.pcan()
	return t
}

func (t *tables) filterbank() bool {
	const (
		n1    = Channels + 1
		align = 2
		block = 4
	)
	const lower, upper float32 = 125.0, 7500.0
	var center [n1]float32
	var starts, widths [n1]int16
	melLow := mel(lower)
	melSpacing := (mel(upper) - melLow) / float32(n1)
	for i := 0; i < n1; i++ {
		center[i] = melLow + float32(melSpacing*float32(i+1))
	}
	hzPerBin := float32(0.5*16000) / (float32(fftC+1) - 1)
	t.start = int(1.5 + float64(lower/hzPerBin))
	t.end = 0
	freqStart, weightStart, zeros := t.start, 0, false
	for c := 0; c < n1; c++ {
		fi := freqStart
		for mel(float32(float32(fi)*hzPerBin)) <= center[c] {
			fi++
		}
		width := fi - freqStart
		starts[c], widths[c] = int16(freqStart), int16(width)
		if width == 0 { // gets no bins: points at a block of zero weights at the front
			t.chanFreqStart[c], t.chanWeightStart[c], t.chanWidth[c] = 0, 0, block
			if !zeros {
				zeros = true
				for j := 0; j < c; j++ {
					t.chanWeightStart[j] += block
				}
				weightStart += block
			}
		} else {
			aligned := freqStart / align * align
			aw := freqStart - aligned + width
			padded := ((aw-1)/block + 1) * block
			t.chanFreqStart[c], t.chanWeightStart[c], t.chanWidth[c] = int16(aligned), int16(weightStart), int16(padded)
			weightStart += padded
		}
		freqStart = fi
	}
	if weightStart > len(t.weights) {
		return false
	}
	for c := 0; c < n1; c++ {
		freq := int(starts[c])
		off := freq - int(t.chanFreqStart[c])
		ws := int(t.chanWeightStart[c])
		denom := melLow
		if c > 0 {
			denom = center[c-1]
		}
		for j := 0; j < int(widths[c]); j++ {
			w := (center[c] - mel(float32(float32(freq)*hzPerBin))) / (center[c] - denom)
			t.weights[ws+off+j] = int16(math.Floor(float64(float32(float32(w*(1<<filterbankBits)) + 0.5))))
			t.unweights[ws+off+j] = int16(math.Floor(float64(float32(float32((1.0-w)*(1<<filterbankBits)) + 0.5))))
			freq++
		}
		if freq > t.end {
			t.end = freq
		}
	}
	// The channels read up to chanFreqStart + chanWidth bins, which has to stay inside the spectrum.
	for c := 0; c < n1; c++ {
		if int(t.chanFreqStart[c])+int(t.chanWidth[c]) > fftC+1 {
			return false
		}
	}
	return t.end < fftC+1
}

func pcanGain(inputBits int, x uint32) int16 {
	const strength, offset float32 = 0.95, 80.0
	const gainBits = 21
	xf := float32(x) / float32(uint32(1)<<inputBits)
	g := float32(float32(uint32(1)<<gainBits) * powf(xf+offset, -strength))
	if g > 0x7fff {
		return 0x7fff
	}
	return int16(g + 0.5)
}

func (t *tables) pcan() {
	correction := msb32(fftN) - 1 - filterbankBits/2
	inputBits := nrSmoothingBits - correction
	t.correction = uint(correction)
	t.snrShift = uint(21 - correction - pcanSNRBits)
	lut := t.gainLUT[:]
	lut[0] = pcanGain(inputBits, 0)
	lut[1] = pcanGain(inputBits, 1)
	for iv := 2; iv <= 32; iv++ {
		x0 := uint32(1) << (iv - 1)
		x1 := x0 + (x0 >> 1)
		x2 := 2 * x0
		if iv == 32 {
			x2 = x0 + (x0 - 1)
		}
		y0, y1, y2 := pcanGain(inputBits, x0), pcanGain(inputBits, x1), pcanGain(inputBits, x2)
		d1, d2 := int32(y1)-int32(y0), int32(y2)-int32(y0)
		a1 := 4*d1 - d2
		a2 := d2 - a1
		lut[4*iv-6], lut[4*iv-5], lut[4*iv-4] = y0, int16(a1), int16(a2)
	}
}
