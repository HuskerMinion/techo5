package asp

import "math"

// The listener's own tone control, which the vendor's chain has as "UserEQ" and we did not.
//
// The tuning is Amazon's judgment about the driver, and it is a strong one: on the Show it puts the
// low end some sixteen decibels above the mids, because the speaker on its own is thin. That is the
// right starting point and the wrong finishing point for a room somebody actually sits in, and
// without a control the only choices are the vendor's tuning or none at all — which is a choice
// between too much bass and none.
//
// So: two shelves, off by default, in front of the tuning rather than after it. In front, because a
// listener who turns the bass down should be asking the compressor for less as well as hearing less;
// after it, the low band's protection would already have run at the level the shelf just took away,
// which is work done for nothing and, at the top of the dial, excursion asked of the driver for
// nothing.
const (
	// bassHinge and trebleHinge are where each shelf turns. The bass hinge sits above the tuning's
	// lift so the control moves what somebody means by "bass"; the treble hinge is in the air rather
	// than the presence region, so turning it down softens without dulling speech.
	bassHinge   = 250.0
	trebleHinge = 4000.0

	// ToneRange bounds either shelf in dB. Wide enough to be worth having, narrow enough that nobody
	// can put the driver somewhere the tuning was not designed for.
	ToneRange = 6.0
)

// Tone is a pair of shelf gains in dB, zero for the tuning as the vendor left it.
type Tone struct{ Bass, Treble float64 }

// Flat is a tone control that does nothing, which is what a device nobody has touched has.
func (t Tone) Flat() bool { return t.Bass == 0 && t.Treble == 0 }

// clamped keeps a stored value inside what the control offers, so a configuration written by hand
// cannot ask for something the driver was never meant to do.
func (t Tone) clamped() Tone {
	return Tone{
		Bass:   math.Max(-ToneRange, math.Min(ToneRange, t.Bass)),
		Treble: math.Max(-ToneRange, math.Min(ToneRange, t.Treble)),
	}
}

// tone is the running state: one shelf each, or nothing at all while both are zero.
type tone struct {
	low  *biquad
	high *biquad
}

func newTone(t Tone, rate int) *tone {
	t = t.clamped()
	s := &tone{}
	if t.Bass != 0 {
		s.low = newShelf(bassHinge, t.Bass, rate, false)
	}
	if t.Treble != 0 {
		s.high = newShelf(trebleHinge, t.Treble, rate, true)
	}
	return s
}

func (s *tone) process(x []float32) {
	if s.low != nil {
		for i, v := range x {
			x[i] = s.low.step(v)
		}
	}
	if s.high != nil {
		for i, v := range x {
			x[i] = s.high.step(v)
		}
	}
}

func (s *tone) reset() {
	for _, f := range []*biquad{s.low, s.high} {
		if f != nil {
			f.z1, f.z2 = 0, 0
		}
	}
}

// newShelf is the usual second-order shelving section: flat on one side of the hinge, gainDB on the
// other, at the gentle slope a tone control wants rather than the steep one a crossover does.
func newShelf(fc, gainDB float64, rate int, high bool) *biquad {
	a := math.Pow(10, gainDB/40)
	w := 2 * math.Pi * fc / float64(rate)
	cos, sin := math.Cos(w), math.Sin(w)
	// The shelf's slope at S of 1: as steep as it goes without lifting a corner past the gain it was
	// asked for, which is what makes a tone control sound like one rather than like a filter.
	alpha := sin / 2 * math.Sqrt2
	twoSqrtAAlpha := 2 * math.Sqrt(a) * alpha

	var b0, b1, b2, a0, a1, a2 float64
	if high {
		b0 = a * ((a + 1) + (a-1)*cos + twoSqrtAAlpha)
		b1 = -2 * a * ((a - 1) + (a+1)*cos)
		b2 = a * ((a + 1) + (a-1)*cos - twoSqrtAAlpha)
		a0 = (a + 1) - (a-1)*cos + twoSqrtAAlpha
		a1 = 2 * ((a - 1) - (a+1)*cos)
		a2 = (a + 1) - (a-1)*cos - twoSqrtAAlpha
	} else {
		b0 = a * ((a + 1) - (a-1)*cos + twoSqrtAAlpha)
		b1 = 2 * a * ((a - 1) - (a+1)*cos)
		b2 = a * ((a + 1) - (a-1)*cos - twoSqrtAAlpha)
		a0 = (a + 1) + (a-1)*cos + twoSqrtAAlpha
		a1 = -2 * ((a - 1) + (a+1)*cos)
		a2 = (a + 1) + (a-1)*cos - twoSqrtAAlpha
	}
	return &biquad{b0: b0 / a0, b1: b1 / a0, b2: b2 / a0, a1: a1 / a0, a2: a2 / a0}
}
