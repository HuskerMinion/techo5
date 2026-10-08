//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"sync"
)

// Frosted glass for the deck (render_deck.go): see-through panels over a dark page lit by a few soft
// glows, with a bright top edge and a thin light rim. The page behind is drawn once per screen size
// and theme and copied in after that; the panels are blended over it a row at a time.

// backdropKey is what a deck backdrop depends on.
type backdropKey struct {
	w, h         int
	ground, glow color.RGBA
}

var (
	backdropMu sync.Mutex
	backdrops  = map[backdropKey]*image.RGBA{}
)

// glassBackdrop paints the page behind the glass: the theme's ground darkened toward the foot, with
// three wide glows, the theme's accent at the top left, a blue at the lower right and a violet off
// to the right.
func (r *paint) glassBackdrop() {
	key := backdropKey{w: r.w, h: r.h, ground: walnut, glow: amber}
	backdropMu.Lock()
	img := backdrops[key]
	if img == nil {
		img = makeBackdrop(key)
		if len(backdrops) > 8 {
			clear(backdrops)
		}
		backdrops[key] = img
	}
	backdropMu.Unlock()
	draw.Draw(r.dst, r.dst.Rect, img, image.Point{}, draw.Src)
}

func makeBackdrop(k backdropKey) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, k.w, k.h))
	deep := lerp(k.ground, color.RGBA{0, 0, 0, 255}, 0.45)
	type glow struct {
		x, y, rad float64
		c         color.RGBA
		a         float64
	}
	w, h := float64(k.w), float64(k.h)
	glows := []glow{
		{0.12 * w, 0.05 * h, 0.55 * w, k.glow, 0.30},
		{0.92 * w, 0.95 * h, 0.55 * w, color.RGBA{0x3d, 0x6f, 0xe8, 0xff}, 0.32},
		{0.70 * w, 0.30 * h, 0.35 * w, color.RGBA{0x9b, 0x5b, 0xe0, 0xff}, 0.22},
	}
	for y := range k.h {
		base := lerp(k.ground, deep, float64(y)/h)
		for x := range k.w {
			cr, cg, cb := float64(base.R), float64(base.G), float64(base.B)
			for _, g := range glows {
				dx, dy := (float64(x)-g.x)/g.rad, (float64(y)-g.y)/g.rad
				d := dx*dx + dy*dy
				if d >= 1 {
					continue
				}
				a := g.a * (1 - d) * (1 - d)
				cr += (float64(g.c.R) - cr) * a
				cg += (float64(g.c.G) - cg) * a
				cb += (float64(g.c.B) - cb) * a
			}
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = uint8(cr+0.5), uint8(cg+0.5), uint8(cb+0.5), 255
		}
	}
	return img
}

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

// glassRim is a rounded rectangle's outline, w wide just inside its edge, at opacity a: brighter
// along the top than the foot, as glass catches the light.
func (r *paint) glassRim(b image.Rectangle, rad, w float64, c color.RGBA, a float64) {
	x0, y0, x1, y1 := rectF(b)
	for y := b.Min.Y - 1; y <= b.Max.Y; y++ {
		fall := 1 - 0.55*clamp01((float64(y)-y0)/(y1-y0))
		// Only the rows near the top and foot, and the columns near the sides, can be on the rim.
		near := float64(y) < y0+rad+w+1 || float64(y) > y1-rad-w-1
		// Between the sides of a row away from the corners nothing is on the rim: jump over it.
		skipFrom, skipTo := int(math.Ceil(x0+w+1)), int(math.Floor(x1-w-1))
		for x := b.Min.X - 1; x <= b.Max.X; x++ {
			if !near && x >= skipFrom && x < skipTo {
				x = skipTo - 1
				continue
			}
			d := rrDist(float64(x)+0.5, float64(y)+0.5, x0, y0, x1, y1, rad)
			r.blendAt(x, y, c, a*fall*clamp01(0.5-(math.Abs(d+w/2)-w/2)))
		}
	}
}

// glassGlow is a soft light around a rounded rectangle, spread wide, fading out from its edge.
func (r *paint) glassGlow(b image.Rectangle, rad, spread float64, c color.RGBA, a float64) {
	x0, y0, x1, y1 := rectF(b)
	s := int(math.Ceil(spread))
	for y := b.Min.Y - s; y < b.Max.Y+s; y++ {
		inside := y > b.Min.Y+int(rad) && y < b.Max.Y-int(rad)
		for x := b.Min.X - s; x < b.Max.X+s; x++ {
			if inside && x > b.Min.X && x < b.Max.X {
				x = b.Max.X - 1
				continue
			}
			d := rrDist(float64(x)+0.5, float64(y)+0.5, x0, y0, x1, y1, rad)
			if d <= 0 || d >= spread {
				continue
			}
			f := 1 - d/spread
			r.blendAt(x, y, c, a*f*f)
		}
	}
}

// glassBevel is the raised edge of a button: a band w wide just inside its outline, lit along the
// top and shaded along the foot, fading toward the middle, so the face reads as standing off the page.
func (r *paint) glassBevel(b image.Rectangle, rad, w, a float64) {
	x0, y0, x1, y1 := rectF(b)
	light, shade := color.RGBA{0xff, 0xff, 0xff, 0xff}, color.RGBA{0, 0, 0, 0xff}
	skipFrom, skipTo := int(math.Ceil(x0+w+1)), int(math.Floor(x1-w-1))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		t := clamp01((float64(y) - y0) / (y1 - y0))
		near := float64(y) < y0+rad+w+1 || float64(y) > y1-rad-w-1
		for x := b.Min.X; x < b.Max.X; x++ {
			if !near && x >= skipFrom && x < skipTo {
				x = skipTo - 1
				continue
			}
			d := -rrDist(float64(x)+0.5, float64(y)+0.5, x0, y0, x1, y1, rad)
			if d <= 0 || d >= w {
				continue
			}
			f := 1 - d/w
			f *= f
			if t < 0.5 {
				r.blendAt(x, y, light, a*f*(1-2*t))
			} else {
				r.blendAt(x, y, shade, a*f*(2*t-1)*1.3)
			}
		}
	}
}
