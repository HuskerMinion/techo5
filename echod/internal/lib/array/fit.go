package array

import (
	"errors"
	"math"
)

// Observation is one measured arrival difference: pair P heard a source at Bearing, A later than B by
// TDOA seconds.
type Observation struct {
	P       Pair
	Bearing float64
	TDOA    float64
}

// Fit finds the positions of n microphones that best explain the observations, centred on their
// centroid.
//
// A far source's arrival difference is linear in the pair's offset: TDOA = -(pA - pB)·u / c. So each
// pair's offset is a least squares fit of two unknowns over every bearing it was heard from, and the
// positions follow from the offsets by a second least squares that anchors their sum at zero. Nothing
// is iterated and nothing needs a first guess.
//
// residual is the root mean square of what the fitted geometry leaves unexplained, in seconds.
func Fit(n int, obs []Observation) (g Geometry, residual float64, err error) {
	pairs := Pairs(n)
	offset := make([]Vec, len(pairs))
	for i, p := range pairs {
		var sxx, sxy, syy, bx, by float64
		count := 0
		for _, o := range obs {
			if o.P != p {
				continue
			}
			u := Toward(o.Bearing)
			t := -o.TDOA * SpeedOfSound
			sxx += u.X * u.X
			sxy += u.X * u.Y
			syy += u.Y * u.Y
			bx += u.X * t
			by += u.Y * t
			count++
		}
		det := sxx*syy - sxy*sxy
		if count < 2 || math.Abs(det) < 1e-9 {
			return nil, 0, errors.New("array: too few distinct bearings for a pair")
		}
		offset[i] = Vec{(syy*bx - sxy*by) / det, (sxx*by - sxy*bx) / det}
	}

	// Positions from offsets: minimise Σ|pA - pB - d|² with Σp = 0. For a complete set of pairs this
	// has a closed form: p_m = (1/n) Σ_k (p_m - p_k), the average of every offset from m.
	g = make(Geometry, n)
	for i, p := range pairs {
		g[p.A] = g[p.A].Add(offset[i])
		g[p.B] = g[p.B].Sub(offset[i])
	}
	for m := range g {
		g[m] = g[m].Scale(1 / float64(n))
	}

	var sq float64
	for _, o := range obs {
		e := g.TDOA(o.P, o.Bearing) - o.TDOA
		sq += e * e
	}
	if len(obs) > 0 {
		residual = math.Sqrt(sq / float64(len(obs)))
	}
	return g, residual, nil
}
