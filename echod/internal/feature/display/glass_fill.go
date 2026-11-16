//go:build !dot

package display

import (
	"image"
	"image/color"
	"math"
)

// The blend the deck's glass is made of (render_glass.go), which a press is shown with on both panels
// too (press_draw.go).

// glassFill blends c over a rounded rectangle, from aTop at the top to aBottom at the foot.
func (r *paint) glassFill(b image.Rectangle, rad float64, c color.RGBA, aTop, aBottom float64) {
	x0, y0, x1, y1 := rectF(b)
	corner := min(int(math.Ceil(rad))+1, b.Dx()/2)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		a := aTop + (aBottom-aTop)*clamp01((float64(y)-y0)/(y1-y0))
		for x := b.Min.X - 1; x < b.Min.X+corner; x++ {
			r.blendAt(x, y, c, a*clamp01(0.5-rrDist(float64(x)+0.5, float64(y)+0.5, x0, y0, x1, y1, rad)))
		}
		for x := b.Max.X - corner; x <= b.Max.X; x++ {
			r.blendAt(x, y, c, a*clamp01(0.5-rrDist(float64(x)+0.5, float64(y)+0.5, x0, y0, x1, y1, rad)))
		}
		r.blendSpan(y, b.Min.X+corner, b.Max.X-corner, c, a)
	}
}

// blendSpan blends c over a row from x0 to x1 at opacity a, in fixed point.
func (r *paint) blendSpan(y, x0, x1 int, c color.RGBA, a float64) {
	if a <= 0 || y < r.dst.Rect.Min.Y || y >= r.dst.Rect.Max.Y {
		return
	}
	x0, x1 = max(x0, r.dst.Rect.Min.X), min(x1, r.dst.Rect.Max.X)
	if x0 >= x1 {
		return
	}
	k := uint32(math.Min(a, 1)*256 + 0.5)
	inv := 256 - k
	cr, cg, cb := uint32(c.R)*k, uint32(c.G)*k, uint32(c.B)*k
	row := r.dst.Pix[r.dst.PixOffset(x0, y):r.dst.PixOffset(x1, y)]
	for i := 0; i < len(row); i += 4 {
		row[i] = uint8((uint32(row[i])*inv + cr) >> 8)
		row[i+1] = uint8((uint32(row[i+1])*inv + cg) >> 8)
		row[i+2] = uint8((uint32(row[i+2])*inv + cb) >> 8)
	}
}
