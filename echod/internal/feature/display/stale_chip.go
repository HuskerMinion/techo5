//go:build !dot && !spot

package display

import (
	"image"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// A picture that has gone stale says so: the streamed dashboard and a camera's view both keep the last
// picture up when nothing more comes, and it looked exactly like a live one. The dashboard's went on
// taking taps for a page that was no longer there. The chip is the header's pill, in its colors, so it
// reads as the device speaking and not as part of the picture.

// dashLostGrace is how long a streamed dashboard may be without its connection before the chip says
// so: a server that ends a stuck session is back within a second or two, and is not announced.
const dashLostGrace = 3 * time.Second

// drawStream draws the streamed dashboard's picture; a variable for the tests.
var drawStream = func(dst *image.RGBA) bool { return dashboard.Get().DrawStream(dst) }

// dashStale is whether the streamed page's picture has had no connection behind it for a while.
func dashStale(v dashboard.View, now time.Time) bool {
	return v.Ready && !v.Lost.IsZero() && now.Sub(v.Lost) >= dashLostGrace
}

// cameraStale is whether the camera's picture on the screen is an old one: a frame failed after it,
// and until the next one comes the last is all there is.
func cameraStale(v home.CameraView) bool {
	return v.Frame != nil && v.Error != "" && len(v.Grid) == 0
}

// staleChip draws the "not live" chip and returns where: its top left at at, or with right set its
// bottom right there.
func (r *renderer) staleChip(at image.Point, right bool) image.Rectangle {
	line := tr("not_live")
	pad, high := r.s(14), r.s(28)
	b := image.Rect(at.X, at.Y, at.X+r.width(r.tiny, line)+2*pad, at.Y+high)
	if right {
		b = b.Sub(b.Size())
	}
	r.roundButton(b, float64(high)/2, walnut)
	r.text(r.tiny, line, b.Min.X+pad, b.Min.Y+r.s(21), amber)
	return b
}
