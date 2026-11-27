// Package array is the geometry of a small microphone array and what follows from it: when a far
// source reaches each microphone, how two microphones' cross spectrum points at a direction, and the
// steered response that turns those into a bearing.
//
// Positions are in meters in the plane of the top face, seen from above. Bearings are radians
// clockwise from LED segment 0, so a bearing is a place on the ring and the ring can show it as it is.
// Toward is the unit vector of a bearing in the same frame, which is all that makes "clockwise" true:
// positions and bearings only have to agree with each other.
package array

import "math"

// SpeedOfSound in air at room temperature, m/s.
const SpeedOfSound = 343.0

// Vec is a position or direction in the plane, in meters.
type Vec struct{ X, Y float64 }

func (a Vec) Sub(b Vec) Vec       { return Vec{a.X - b.X, a.Y - b.Y} }
func (a Vec) Dot(b Vec) float64   { return a.X*b.X + a.Y*b.Y }
func (a Vec) Len() float64        { return math.Hypot(a.X, a.Y) }
func (a Vec) Scale(s float64) Vec { return Vec{a.X * s, a.Y * s} }
func (a Vec) Add(b Vec) Vec       { return Vec{a.X + b.X, a.Y + b.Y} }
func (a Vec) Bearing() float64    { return math.Mod(math.Atan2(a.Y, a.X)+2*math.Pi, 2*math.Pi) }
func Toward(bearing float64) Vec  { return Vec{math.Cos(bearing), math.Sin(bearing)} }
func Degrees(rad float64) float64 { return rad * 180 / math.Pi }
func Radians(deg float64) float64 { return deg * math.Pi / 180 }

// Geometry is where each microphone is.
type Geometry []Vec

// Delay is how much later microphone m hears a far source at bearing than the origin does, in seconds:
// negative for a microphone on the source's side.
func (g Geometry) Delay(m int, bearing float64) float64 {
	return -g[m].Dot(Toward(bearing)) / SpeedOfSound
}

// Pair is two microphones whose arrival difference is measured, A's time minus B's.
type Pair struct{ A, B int }

// Pairs is every pair of n microphones, each once.
func Pairs(n int) []Pair {
	var out []Pair
	for a := range n {
		for b := a + 1; b < n; b++ {
			out = append(out, Pair{a, b})
		}
	}
	return out
}

// TDOA is A's arrival minus B's for a far source at bearing, in seconds.
func (g Geometry) TDOA(p Pair, bearing float64) float64 {
	return g.Delay(p.A, bearing) - g.Delay(p.B, bearing)
}

// MaxTDOA is the largest arrival difference any pair can show, in seconds.
func (g Geometry) MaxTDOA() float64 {
	var m float64
	for _, p := range Pairs(len(g)) {
		m = max(m, g[p.A].Sub(g[p.B]).Len()/SpeedOfSound)
	}
	return m
}

// Centered moves the array so its centroid is the origin, which changes no arrival difference and
// makes every microphone's own delay as small as it can be.
func (g Geometry) Centered() Geometry {
	var c Vec
	for _, p := range g {
		c = c.Add(p)
	}
	c = c.Scale(1 / float64(len(g)))
	out := make(Geometry, len(g))
	for i, p := range g {
		out[i] = p.Sub(c)
	}
	return out
}
