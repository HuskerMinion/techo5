//go:build spot

package display

import (
	"image"
	"image/draw"
	"time"
)

// The Spot's clock faces wander a few pixels against image retention, as the Show's clock does
// (clock_drift.go, #560): the time, the date and whatever a style draws around them stand in one
// place for an hour at a time otherwise. The path is the time of day on the clock, so a frame is the
// same for the same time, previews and all.

// spotDriftAcross is how far a face wanders either way across, and spotDriftUp how far up, in the
// panel's pixels. Up and not down: the circle narrows fast at the foot, where the lines are already
// fitted to its width (footRoom), and it is wide where they would go at the top. Across stays inside
// the room footRoom leaves either side of a line at the foot.
const (
	spotDriftAcross = 6
	spotDriftUp     = 3
)

// spotClockDrift is how far from its place a clock face is at now: a pixel a minute at most each way,
// across out to spotDriftAcross either side and back every 24 minutes, up to spotDriftUp and back
// every 6, so the path repeats every 24 minutes and runs on through midnight without a jump. With the
// Call button at the top of the face the face only moves across: up would take the status word under
// the button.
func spotClockDrift(now time.Time, callButton bool) image.Point {
	m := now.Hour()*60 + now.Minute()
	d := image.Pt(sway(m, spotDriftAcross), -abs(sway(m, spotDriftUp)))
	if callButton {
		d.Y = 0
	}
	return d
}

// drifted draws page moved by d. The faces are laid out at fixed places on the circle, so rather than
// move each, page draws on a spare canvas holding what is under the face (the background, the rim, a
// photo) moved the other way, which then goes back moved by d: what page drew moves, everything else
// lands where it was. Faces keep their sizes, since they are fitted where they were laid out.
func (r *roundRenderer) drifted(d image.Point, page func()) {
	if d == (image.Point{}) {
		page()
		return
	}
	dst := r.dst
	if r.driftCanvas == nil || r.driftCanvas.Rect != dst.Rect {
		r.driftCanvas = image.NewRGBA(dst.Rect)
	}
	spare := r.driftCanvas
	fillCanvas(spare, colBackground) // the strip at the edge that nothing moves into, off the circle
	draw.Draw(spare, spare.Rect, dst, spare.Rect.Min.Add(d), draw.Src)
	func() {
		r.dst = spare
		defer func() { r.dst = dst }()
		page()
	}()
	draw.Draw(dst, dst.Rect, spare, dst.Rect.Min.Sub(d), draw.Src)
}
