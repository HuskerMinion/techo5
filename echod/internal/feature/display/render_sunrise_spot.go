//go:build spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"math"
)

// The sunrise on the round face. The same idea as the Show's: the panel is the lamp in the room, so
// the whole face warms from the dark red of the first minutes to a pale gold, with a sun climbing
// into it and the time readable the whole way.
//
// A circle has no corners to lose, so the sky is drawn as bands across it and the sun rises from the
// bottom of the circle rather than from an edge.

// sunriseFace draws the sky, the sun and the clock. p is 0 to 1, as sunriseProgress reports.
func (r *roundRenderer) sunriseFace(s roundScene, p float64, face bool) {
	p = math.Min(math.Max(p, 0), 1)
	top := lerpRGB(color.RGBA{26, 8, 6, 255}, color.RGBA{232, 158, 78, 255}, p)
	bottom := lerpRGB(color.RGBA{74, 16, 8, 255}, color.RGBA{255, 214, 140, 255}, p)
	h := r.dst.Bounds().Dy()
	for y := 0; y < h; y++ {
		row := lerpRGB(top, bottom, float64(y)/float64(h-1))
		draw.Draw(r.dst, image.Rect(0, y, r.dst.Bounds().Dx(), y+1), image.NewUniform(row), image.Point{}, draw.Src)
	}

	rad := float64(h) * (0.13 + 0.06*p)
	cy := float64(h)*1.05 - float64(h)*0.38*p
	cx := float64(centre)
	body := lerpRGB(color.RGBA{219, 74, 28, 255}, color.RGBA{255, 246, 205, 255}, p)
	for i := 5; i > 0; i-- {
		r.discAt(cx, cy, rad+float64(i)*rad/7, softly(body, uint8(20+8*i)))
	}
	r.discAt(cx, cy, rad, body)

	if face {
		ink := color.RGBA{uint8(float64(body.R) * 0.35), uint8(float64(body.G) * 0.25), uint8(float64(body.B) * 0.2), 255}
		eye := math.Max(rad/9, 3)
		r.discAt(cx-rad/3, cy-rad/4, eye, ink)
		r.discAt(cx+rad/3, cy-rad/4, eye, ink)
		for i := -6; i <= 6; i++ {
			t := float64(i) / 6
			r.discAt(cx+t*rad*0.52, cy+rad*(0.18+0.30*(1-t*t)), math.Max(rad/14, 2), ink)
		}
	}

	ink := lerpRGB(color.RGBA{255, 236, 214, 255}, color.RGBA{58, 22, 10, 255}, math.Min(p*1.6, 1))
	hm := clockHM(s.now)
	r.centred(r.clock, hm, 200, ink)
	if suffix := clockSuffix(s.now); suffix != "" {
		r.text(r.title, suffix, centre+r.width(r.clock, hm)/2+ampmGap, 200, ink)
	}
	r.centred(r.small, s.now.Format("Monday, January 2"), 250, ink)
}

// lerpRGB mixes two colors, t from 0 (a) to 1 (b).
func lerpRGB(a, b color.RGBA, t float64) color.RGBA {
	t = math.Min(math.Max(t, 0), 1)
	mix := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
	return color.RGBA{mix(a.R, b.R), mix(a.G, b.G), mix(a.B, b.B), 255}
}

// softly is a color at a given alpha, left as it is rather than premultiplied: this renderer mixes
// with the alpha itself (blend), where the Show's draws through Go's image library and wants the
// channels scaled. Premultiplying for this one turns a glow into a shadow.
func softly(c color.RGBA, a uint8) color.RGBA {
	c.A = a
	return c
}
