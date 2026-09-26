//go:build !dot

package display

import (
	"image"
	"image/color"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// Town names on the rain map, the way a weather app shows them: a small dot where the town is and its
// name beside it in light letters with a dark edge, so it reads over rain, cloud and ground alike.
// The towns come largest first; each is named only where it has room, clear of the names already
// placed and of what the page keeps for itself (its bands, its buttons, the home marker).

var (
	placeInk  = color.RGBA{0xee, 0xea, 0xe2, 0xff}
	placeEdge = color.RGBA{0x0c, 0x0a, 0x08, 0xd0}
)

// drawPlaces names up to max of places on dst in face. keepOut are areas to leave alone; fits reports
// whether a label's box is somewhere the page shows the map (the whole of it on the Show, inside the
// circle on the Spot).
func drawPlaces(dst *image.RGBA, face font.Face, places []home.RadarPlace, keepOut []image.Rectangle,
	fits func(image.Rectangle) bool, max int) {
	m := face.Metrics()
	asc, desc := m.Ascent.Ceil(), m.Descent.Ceil()
	gap := asc / 3
	var placed []image.Rectangle
	for _, p := range places {
		if len(placed) == max {
			return
		}
		w := font.MeasureString(face, p.Name).Ceil()
		base := p.At.Y + asc/2 - 1
		// The name to the right of its dot, or to the left where the right is taken.
		right := image.Rect(p.At.X-4, base-asc-2, p.At.X+6+w+3, base+desc+2)
		left := image.Rect(p.At.X-7-w-3, base-asc-2, p.At.X+4, base+desc+2)
		free := func(b image.Rectangle) bool {
			return fits(b) && !overlaps(b, placed, gap) && !overlaps(b, keepOut, 0)
		}
		var x int
		switch {
		case free(right):
			placed, x = append(placed, right), p.At.X+7
		case free(left):
			placed, x = append(placed, left), p.At.X-7-w
		default:
			continue
		}
		dot(dst, p.At, 3)
		for _, o := range [][2]int{{-2, 0}, {2, 0}, {0, -2}, {0, 2}, {-1, -1}, {1, 1}, {-1, 1}, {1, -1}} {
			textAt(dst, face, p.Name, x+o[0], base+o[1], placeEdge)
		}
		textAt(dst, face, p.Name, x, base, placeInk)
	}
}

func overlaps(r image.Rectangle, others []image.Rectangle, gap int) bool {
	g := r.Inset(-gap)
	for _, o := range others {
		if g.Overlaps(o) {
			return true
		}
	}
	return false
}

// dot is the town's mark: a light disc with a dark rim.
func dot(dst *image.RGBA, c image.Point, radius int) {
	for dy := -radius - 1; dy <= radius+1; dy++ {
		for dx := -radius - 1; dx <= radius+1; dx++ {
			d := dx*dx + dy*dy
			switch {
			case d <= radius*radius:
				dst.Set(c.X+dx, c.Y+dy, placeInk)
			case d <= (radius+1)*(radius+1):
				dst.Set(c.X+dx, c.Y+dy, placeEdge)
			}
		}
	}
}

func textAt(dst *image.RGBA, face font.Face, s string, x, baseline int, c color.Color) {
	d := font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, baseline)}
	d.DrawString(s)
}
