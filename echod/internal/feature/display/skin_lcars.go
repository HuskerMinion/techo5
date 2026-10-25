//go:build !dot && !spot

package display

import (
	"image"
	"image/color"

	"golang.org/x/image/vector"
)

// The LCARS skin (skin.go): the starship console look. Its colors are the theme while it is on, it
// draws in Antonio, tall and narrow, and the home clock gets the console's frame: an elbow at the top
// left and another at the bottom left, thin bars of colored segments running right from them, in the
// margins the clock leaves free. The other screens keep their own layout in the skin's colors: their
// edges are in use.

// The console's colors.
var (
	lcarsOrange   = rgb(0xff9900)
	lcarsPeach    = rgb(0xffcc99)
	lcarsLavender = rgb(0xcc99cc)
	lcarsBlue     = rgb(0x9999ff)
	lcarsTan      = rgb(0xffaa77)
)

// lcarsSegments are the bars' colors in turn, with each one's share of the bar's length.
var lcarsSegments = []struct {
	c     color.RGBA
	share float64
}{
	{lcarsPeach, 0.16}, {lcarsOrange, 0.38}, {lcarsLavender, 0.1}, {lcarsBlue, 0.22}, {lcarsTan, 0.14},
}

// The frame's place: in from the screen's edges by lcarsEdge, its bars lcarsBarH tall, in the Show
// 5's pixels.
const lcarsEdge, lcarsBarH = 8, 13

// badgeTop is where the badges at the top right of the home clock start, the alert's and the Corner
// timer's: under the console's top bar while LCARS is on, as the frame is drawn last.
func (r *renderer) badgeTop() int {
	if skin() == skinLCARS {
		return r.s(lcarsEdge + lcarsBarH + 8)
	}
	return r.s(18)
}

// lcarsFrame is the console's frame around the home clock.
func (r *renderer) lcarsFrame() {
	if skin() != skinLCARS {
		return
	}
	r.lcarsDrawn = true
	edge := r.s(lcarsEdge)
	arm := r.s(22)        // the elbows' upright, in the margin left of everything
	bar := r.s(lcarsBarH) // the bars' height, above the weather and below the footer
	gap := r.s(6)         // between segments
	reach := r.s(170)
	mid := r.h / 2

	// Top left: down to just above the middle; bottom left: from just below it.
	r.lcarsElbow(image.Rect(edge, edge, edge+reach, mid-gap/2), arm, bar, false, lcarsOrange)
	r.lcarsElbow(image.Rect(edge, mid+gap/2, edge+reach, r.h-edge), arm, bar, true, lcarsLavender)
	r.lcarsBar(image.Rect(edge+reach+gap, edge, r.w-edge, edge+bar), gap, 0)
	r.lcarsBar(image.Rect(edge+reach+gap, r.h-edge-bar, r.w-edge, r.h-edge), gap, 2)
}

// lcarsElbow fills the elbow in b: the upright arm wide down the left, the bar along the top (or along
// the bottom, flipped), joined by a broad outer curve and a tight inner one.
func (r *renderer) lcarsElbow(b image.Rectangle, arm, bar int, flipped bool, c color.RGBA) {
	outer, inner := float32(r.s(34)), float32(r.s(12))
	a, t := float32(arm), float32(bar)
	r.fillShape(b, c, func(z *vector.Rasterizer, w, h float32) {
		y := func(v float32) float32 {
			if flipped {
				return h - v
			}
			return v
		}
		z.MoveTo(0, y(outer))
		z.QuadTo(0, y(0), outer, y(0))
		z.LineTo(w, y(0))
		z.LineTo(w, y(t))
		z.LineTo(a+inner, y(t))
		z.QuadTo(a, y(t), a, y(t+inner))
		z.LineTo(a, y(h))
		z.LineTo(0, y(h))
	})
}

// lcarsBar is a bar of colored segments across b, gap apart, starting at segment first; the last one
// ends round.
func (r *renderer) lcarsBar(b image.Rectangle, gap, first int) {
	room := b.Dx() - gap*(len(lcarsSegments)-1)
	x := b.Min.X
	for i := range lcarsSegments {
		sg := lcarsSegments[(first+i)%len(lcarsSegments)]
		w := int(float64(room) * sg.share)
		if i == len(lcarsSegments)-1 {
			w = b.Max.X - x
			r.roundRect(image.Rect(x, b.Min.Y, x+w, b.Max.Y), b.Dy()/2, sg.c)
			// Square on its left: only its far end is round.
			r.roundRect(image.Rect(x, b.Min.Y, x+b.Dy(), b.Max.Y), 0, sg.c)
			break
		}
		r.roundRect(image.Rect(x, b.Min.Y, x+w, b.Max.Y), 0, sg.c)
		x += w + gap
	}
}
