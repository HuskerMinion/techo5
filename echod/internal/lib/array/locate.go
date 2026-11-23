package array

import (
	"math"
)

// Locator finds which way a sound comes from: the steered response power with the phase transform
// (SRP-PHAT), every pair's cross spectrum steered at a grid of bearings and summed. It is the
// estimate Amazon's own front end makes on this array (GCC-PHAT between 500 Hz and 7 kHz), and with
// three microphones that are not in a line it covers the whole circle with no front-back mirror.
//
// It keeps a decaying sum of cross spectra, so the answer is the recent past's, weighted by what the
// caller trusted each frame with. It is not safe for concurrent use.
type Locator struct {
	g        Geometry
	spec     *Spectra
	pairs    []Pair
	bearings int

	// steer[b][p][k] is e^{jωτ}: pair p's bin k as it would be turned back by the arrival difference
	// a source at bearing b gives it.
	steer [][][]complex64

	acc  [][]complex128
	x    [][]complex64
	held float64

	// Decay is how much of the past each Add keeps, 0 to 1.
	Decay float64
}

// NewLocator steers g at bearings evenly spaced round the circle, for blocks of n samples at rate.
func NewLocator(g Geometry, n int, rate, loHz, hiHz float64, bearings int) *Locator {
	l := &Locator{g: g, spec: NewSpectra(n, rate, loHz, hiHz), pairs: Pairs(len(g)), bearings: bearings, Decay: 0.9}
	bins := l.spec.Bins()
	l.steer = make([][][]complex64, bearings)
	for b := range l.steer {
		bearing := 2 * math.Pi * float64(b) / float64(bearings)
		l.steer[b] = make([][]complex64, len(l.pairs))
		for p, pair := range l.pairs {
			tau := g.TDOA(pair, bearing)
			row := make([]complex64, bins)
			for k := range row {
				w := l.spec.Omega(k) * tau
				row[k] = complex(float32(math.Cos(w)), float32(math.Sin(w)))
			}
			l.steer[b][p] = row
		}
	}
	l.acc = make([][]complex128, len(l.pairs))
	for p := range l.acc {
		l.acc[p] = make([]complex128, bins)
	}
	l.x = make([][]complex64, len(g))
	for m := range l.x {
		l.x[m] = make([]complex64, bins)
	}
	return l
}

// Size is the block length Add takes.
func (l *Locator) Size() int { return l.spec.Size() }

// Add takes one block per microphone, weight being how much this block is to count.
func (l *Locator) Add(block [][]float32, weight float64) {
	for m := range l.x {
		l.spec.Into(block[m], l.x[m])
	}
	l.accumulate(weight)
}

// AddSpectra is Add for a caller that has already transformed the block, at the Locator's size and
// rate (a Beamformer's Spectrum): full holds every bin up to half the rate, per microphone.
func (l *Locator) AddSpectra(full [][]complex64, weight float64) {
	for m := range l.x {
		copy(l.x[m], full[m][l.spec.lo:l.spec.hi+1])
	}
	l.accumulate(weight)
}

func (l *Locator) accumulate(weight float64) {
	d := complex(l.Decay, 0)
	for p := range l.acc {
		for k := range l.acc[p] {
			l.acc[p][k] *= d
		}
		PHAT(l.acc[p], l.x[l.pairs[p].A], l.x[l.pairs[p].B], weight)
	}
	l.held = l.held*l.Decay + weight
}

// Forget throws the past away.
func (l *Locator) Forget() {
	for p := range l.acc {
		clear(l.acc[p])
	}
	l.held = 0
}

// Power is the steered response at each bearing, each normalised to 1 for a perfect match.
func (l *Locator) Power() []float64 {
	out := make([]float64, l.bearings)
	if l.held <= 0 {
		return out
	}
	norm := l.held * float64(len(l.pairs)*l.spec.Bins())
	for b := range out {
		var p float64
		for i, row := range l.steer[b] {
			acc := l.acc[i]
			for k, s := range row {
				c := acc[k]
				p += real(c)*float64(real(s)) - imag(c)*float64(imag(s))
			}
		}
		out[b] = p / norm
	}
	return out
}

// Bearing is where the sound comes from, radians clockwise from segment 0, and how sure that is: the
// peak's height over the response's average, as a fraction of a perfect match. A diffuse room gives
// near nothing; one talker in it a few tenths.
func (l *Locator) Bearing() (bearing, confidence float64) {
	pw := l.Power()
	best, mean := 0, 0.0
	for b, v := range pw {
		mean += v
		if v > pw[best] {
			best = b
		}
	}
	mean /= float64(len(pw))

	// Between the grid's points, from the parabola through the peak and its neighbours.
	n := len(pw)
	a, c, z := pw[(best+n-1)%n], pw[best], pw[(best+1)%n]
	shift := 0.0
	if den := a - 2*c + z; den < 0 {
		shift = 0.5 * (a - z) / den
	}
	step := 2 * math.Pi / float64(n)
	bearing = math.Mod((float64(best)+shift)*step+2*math.Pi, 2*math.Pi)
	return bearing, c - mean
}
