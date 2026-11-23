package array

import (
	"math"
	"math/cmplx"

	"github.com/HuskerMinion/techo5/echod/internal/lib/fft"
)

// Beamformer steers an array at one of a ring of fixed directions in the short-time frequency domain,
// with superdirective weights: for each bin, the combination that passes a far source at the steered
// bearing unchanged and lets through as little as it can of a diffuse room, the sound that comes from
// everywhere at once.
//
// An array of 22 mm is far smaller than a speech wavelength, so adding its microphones up (delay and
// sum) leaves everything below a few kHz the same from every direction. Superdirective weights subtract
// as well as add and get directivity out of it anyway, at the price of amplifying what the microphones
// do not share — their own hiss. Diagonal loading trades the one against the other; the loading is
// raised, bin by bin, until the white noise gain is at least minWNG, so no bin pays more in hiss than it
// buys in room.
//
// It works in blocks of Size with a hop of half that, a square-root Hann window on the way in and on the
// way out, which overlap-add back to the input exactly where every bin's weight is the same. Its delay
// is one block. It is not safe for concurrent use.
type Beamformer struct {
	g     Geometry
	n     int
	rate  float64
	beams int
	fft   *fft.FFT
	win   []float32

	// w[b][k][m] is microphone m's weight in bin k for beam b, conjugated, so a bin of the output is
	// Σ w·X.
	w [][][]complex64

	in    [][]float32 // the last n samples per microphone
	fill  int         // new samples in since the last block
	out   []float32   // overlap-add accumulator, n long
	ready []float32   // finished output waiting to be read
	buf   []complex64
	x     [][]complex64

	// Beam is the direction being listened to; it may be changed between calls.
	Beam int

	// Spectrum, when set, is handed every block's spectra (all bins, every microphone) before the
	// beam is applied, for a locator to share the transform.
	Spectrum func(x [][]complex64)
}

// NewBeamformer builds the weights for beams evenly spaced round the circle, beam 0 at bearing 0.
func NewBeamformer(g Geometry, n int, rate float64, beams int, minWNGdB float64) *Beamformer {
	b := &Beamformer{g: g, n: n, rate: rate, beams: beams, fft: fft.New(n), win: make([]float32, n),
		out: make([]float32, n), buf: make([]complex64, n)}
	for i := range b.win {
		b.win[i] = float32(math.Sqrt(0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n))))
	}
	b.in = make([][]float32, len(g))
	b.x = make([][]complex64, len(g))
	for m := range g {
		b.in[m] = make([]float32, n)
		b.x[m] = make([]complex64, n/2+1)
	}

	b.w = make([][][]complex64, beams)
	for beam := range beams {
		bearing := 2 * math.Pi * float64(beam) / float64(beams)
		b.w[beam] = make([][]complex64, n/2+1)
		for k := range n/2 + 1 {
			omega := 2 * math.Pi * float64(k) * rate / float64(n)
			w := Superdirective(g, omega, bearing, math.Pow(10, minWNGdB/10))
			row := make([]complex64, len(g))
			for m := range row {
				row[m] = complex64(cmplx.Conj(w[m]))
			}
			b.w[beam][k] = row
		}
	}
	return b
}

// Beams is how many directions it can listen in.
func (b *Beamformer) Beams() int { return b.beams }

// Steering is the far-field response of g to a source at bearing, at angular frequency omega: each
// microphone's phase relative to the origin.
func Steering(g Geometry, omega, bearing float64) []complex128 {
	d := make([]complex128, len(g))
	for m := range g {
		d[m] = cmplx.Exp(complex(0, -omega*g.Delay(m, bearing)))
	}
	return d
}

// Superdirective is the minimum variance distortionless weight vector against a diffuse field, loaded
// until its white noise gain reaches minWNG (a power ratio; the number of microphones is delay and
// sum's, the most any weights can have).
func Superdirective(g Geometry, omega, bearing, minWNG float64) []complex128 {
	n := len(g)
	d := Steering(g, omega, bearing)

	// Γ: how alike two microphones hear a diffuse field in the plane's 3-D surroundings, sinc of their
	// spacing in wavelengths.
	gamma := make([][]complex128, n)
	for i := range gamma {
		gamma[i] = make([]complex128, n)
		for j := range gamma[i] {
			x := omega * g[i].Sub(g[j]).Len() / SpeedOfSound
			v := 1.0
			if x > 1e-9 {
				v = math.Sin(x) / x
			}
			gamma[i][j] = complex(v, 0)
		}
	}

	var w []complex128
	for mu := 1e-4; ; mu *= 2 {
		a := make([][]complex128, n)
		for i := range a {
			a[i] = append([]complex128(nil), gamma[i]...)
			a[i][i] += complex(mu, 0)
		}
		w = solve(a, d)
		var den complex128
		for m := range w {
			den += cmplx.Conj(d[m]) * w[m]
		}
		for m := range w {
			w[m] /= den
		}
		var norm float64
		for _, v := range w {
			norm += real(v)*real(v) + imag(v)*imag(v)
		}
		// WNG = |wᴴd|² / wᴴw, and wᴴd is 1.
		if 1/norm >= minWNG || mu > 1e3 {
			return w
		}
	}
}

// solve is Gaussian elimination with partial pivoting, for the few-by-few systems here.
func solve(a [][]complex128, b []complex128) []complex128 {
	n := len(b)
	x := append([]complex128(nil), b...)
	for c := range n {
		p := c
		for r := c + 1; r < n; r++ {
			if cmplx.Abs(a[r][c]) > cmplx.Abs(a[p][c]) {
				p = r
			}
		}
		a[c], a[p] = a[p], a[c]
		x[c], x[p] = x[p], x[c]
		for r := c + 1; r < n; r++ {
			f := a[r][c] / a[c][c]
			for k := c; k < n; k++ {
				a[r][k] -= f * a[c][k]
			}
			x[r] -= f * x[c]
		}
	}
	for c := n - 1; c >= 0; c-- {
		for k := c + 1; k < n; k++ {
			x[c] -= a[c][k] * x[k]
		}
		x[c] /= a[c][c]
	}
	return x
}

// Process takes the next samples of every microphone, all the same length, and returns as many
// samples of the beam, one block late.
func (b *Beamformer) Process(mics [][]float32) []float32 {
	hop := b.n / 2
	for i := range mics[0] {
		for m := range b.in {
			b.in[m][b.n-hop+b.fill] = mics[m][i]
		}
		b.fill++
		if b.fill == hop {
			b.block()
			b.fill = 0
		}
	}
	// What came in is what goes out, so the caller's frames keep their length; the first block's
	// worth is the silence before the first block was done.
	want := len(mics[0])
	for len(b.ready) < want {
		b.ready = append(make([]float32, want-len(b.ready)), b.ready...)
	}
	out := append([]float32(nil), b.ready[:want]...)
	b.ready = b.ready[want:]
	return out
}

// block transforms the last n samples, applies the beam, and adds the result into the output.
func (b *Beamformer) block() {
	half := b.n / 2
	for m := range b.in {
		for i := range b.buf {
			b.buf[i] = complex(b.in[m][i]*b.win[i], 0)
		}
		b.fft.Forward(b.buf)
		copy(b.x[m], b.buf[:half+1])
		// Slide the history along for the next block.
		copy(b.in[m], b.in[m][half:])
	}
	if b.Spectrum != nil {
		b.Spectrum(b.x)
	}

	w := b.w[b.Beam]
	for k := range half + 1 {
		var y complex64
		for m := range b.x {
			y += w[k][m] * b.x[m][k]
		}
		b.buf[k] = y
	}
	for k := 1; k < half; k++ {
		b.buf[b.n-k] = complex(real(b.buf[k]), -imag(b.buf[k]))
	}
	b.fft.Inverse(b.buf)

	for i := range b.out {
		b.out[i] += real(b.buf[i]) * b.win[i]
	}
	b.ready = append(b.ready, b.out[:half]...)
	copy(b.out, b.out[half:])
	clear(b.out[half:])
}

// DirectivityDB is how much more a beam passes from its own bearing than from a diffuse room, at
// angular frequency omega, in dB: what steering buys against everything else in the room.
func (b *Beamformer) DirectivityDB(beam int, omega float64) float64 {
	k := int(math.Round(omega * float64(b.n) / (2 * math.Pi * b.rate)))
	w := make([]complex128, len(b.g))
	for m := range w {
		w[m] = cmplx.Conj(complex128(b.w[beam][k][m]))
	}
	var diffuse float64
	for i := range w {
		for j := range w {
			x := omega * b.g[i].Sub(b.g[j]).Len() / SpeedOfSound
			v := 1.0
			if x > 1e-9 {
				v = math.Sin(x) / x
			}
			diffuse += real(cmplx.Conj(w[i]) * w[j] * complex(v, 0))
		}
	}
	return -10 * math.Log10(diffuse)
}
