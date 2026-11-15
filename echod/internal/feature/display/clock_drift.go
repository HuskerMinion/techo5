//go:build !dot && !spot

package display

import (
	"image"
	"time"
)

// The classic clock's digits and date stay where they are for an hour at a time, and some LCD panels
// keep a shadow of a picture left that long: the Echo Show 5 2nd gen's showed the last hour's digits
// behind the clock within about an hour (#560). So the time and the date wander a few pixels, a pixel
// a minute, along a path that comes back on itself every two hours: never a jump anyone sees, and no
// pixel at a stroke's edge stays lit or dark for long. The path is the time of day on the clock, so a
// frame is the same for the same time, previews and all.

// driftAcross is how far the clock wanders either way across from its place, and driftDown how far
// down, in the Show 5's pixels: across well inside the margins; down only, and only a little, so the
// digits never stand any higher than the room they were sized for under the weather (clockFaces), and
// the date stays clear of the strips under it.
const (
	driftAcross = 10
	driftDown   = 3
)

// clockDrift is how far from its place the clock is at now: a pixel a minute at most each way (on the
// Show 5), across out to driftAcross either side and back every 40 minutes, down to driftDown and back
// every 6, so the path repeats every two hours and runs on through midnight without a jump.
func (r *renderer) clockDrift(now time.Time) image.Point {
	m := now.Hour()*60 + now.Minute()
	return image.Pt(r.s(sway(m, driftAcross)), r.s(abs(sway(m, driftDown))))
}
