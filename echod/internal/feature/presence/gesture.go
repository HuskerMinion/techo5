//go:build !dot

package presence

import "math"

// A hand held over the camera, as a gesture: the picture is replaced by something smooth (a hand at a
// few centimeters is far out of focus) that is not the room, for between coverShortest and
// coverLongest, and then the room comes back. While gestures are on, the camera's exposure is held
// still through the sudden change (camera.SetGestureExposure), so the hand stays a hand instead of
// being brightened into a blur that looks like anything else. A light switched off also takes the room
// away, but what is left is a dark room, grainy at that exposure, not a smooth hand; a light switched
// on washes the picture out. Neither is taken for a cover.
//
// Measured on a Show 5 and a Show 8, 2026-10-04 (recordings of covers, waves, light switches and
// people moving, kept off the repository): a hand's roughness, the mean difference between neighboring
// cells over the mean brightness, is 0.09 to 0.17 on or nearly on the lens; a dark room's 0.26 to 0.32;
// an ordinary lit room's about 0.21. A hand a few inches away is another matter: it is in focus, takes
// only part of the picture, and reads 0.22 to 0.28 with the room half there, much as somebody leaning
// in does. It is not counted. On the last recording all three covers on the lens were seen, and none of
// the light switches, the leaning in, the touching or the hands at a distance were.
type coverDetector struct {
	scene  []float64 // the room as it usually looks (an average of recent ordinary frames)
	since  float64   // seconds, when the picture was first covered; 0 while it is not
	frames int       // covered frames so far
}

const (
	coverGone     = 0.5  // likeness to the room below which the room is gone
	coverBack     = 0.75 // likeness at which it is back
	coverSmooth   = 0.2  // roughness below which what is in front of the lens is smooth
	coverBright   = 220  // a picture this bright on average is a light washing it out, not a hand
	coverShortest = 0.4  // seconds
	coverLongest  = 4.0
)

// step takes the next grid at time t (seconds) and reports whether a cover just ended: the hand came
// away after the right length of time.
func (c *coverDetector) step(g []uint8, t float64) bool {
	n := len(g)
	if n != gridW*gridH {
		return false
	}
	var sum float64
	for _, v := range g {
		sum += float64(v)
	}
	mean := sum / float64(n)
	if c.scene == nil {
		c.scene = make([]float64, n)
		for i, v := range g {
			c.scene[i] = float64(v)
		}
		return false
	}
	like := likeness(g, c.scene)
	if c.since == 0 {
		smooth := roughness(g)/math.Max(mean, 1) < coverSmooth
		if like < coverGone && smooth && mean < coverBright && mean > 4 {
			c.since, c.frames = t, 1
			return false
		}
		if like >= coverBack {
			c.learn(g)
		} else if like < coverGone {
			// The room changed and stayed changed (a light, the device moved): learn the new one.
			c.relearn(g)
		}
		return false
	}
	if like >= coverBack {
		long := t - c.since
		c.since, c.frames = 0, 0
		return long >= coverShortest && long <= coverLongest
	}
	if t-c.since > coverLongest+1 || roughness(g)/math.Max(mean, 1) > coverSmooth*1.4 {
		// Covered too long, or what is in front turned grainy (a dark room): not a hand.
		c.since, c.frames = 0, 0
		c.relearn(g)
		return false
	}
	c.frames++
	return false
}

func (c *coverDetector) learn(g []uint8) {
	for i, v := range g {
		c.scene[i] += (float64(v) - c.scene[i]) * 0.2
	}
}

func (c *coverDetector) relearn(g []uint8) {
	for i, v := range g {
		c.scene[i] = float64(v)
	}
}

// likeness is the correlation of a grid with the scene, -1 to 1: the same room, whatever its exposure,
// is near 1.
func likeness(g []uint8, scene []float64) float64 {
	n := float64(len(g))
	var ma, mb float64
	for i, v := range g {
		ma += float64(v)
		mb += scene[i]
	}
	ma /= n
	mb /= n
	var num, da, db float64
	for i, v := range g {
		x, y := float64(v)-ma, scene[i]-mb
		num += x * y
		da += x * x
		db += y * y
	}
	if da == 0 || db == 0 {
		return 0
	}
	return num / math.Sqrt(da*db)
}

// roughness is the mean difference between neighboring cells, across and down.
func roughness(g []uint8) float64 {
	var sum float64
	var n int
	for y := range gridH {
		for x := range gridW {
			v := float64(g[y*gridW+x])
			if x+1 < gridW {
				sum += math.Abs(float64(g[y*gridW+x+1]) - v)
				n++
			}
			if y+1 < gridH {
				sum += math.Abs(float64(g[(y+1)*gridW+x]) - v)
				n++
			}
		}
	}
	return sum / float64(n)
}
