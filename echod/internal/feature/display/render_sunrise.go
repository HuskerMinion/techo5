//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"math"
)

// The sunrise page: what the screen shows while the light before an alarm comes up.
//
// The panel is the only lamp in the room, so what is drawn matters more than the number the
// backlight is set to: a screen filled with warm color puts out far more light than a clock on a
// black background at the same brightness. The sky warms from the dark red of the first minute
// through orange to a pale gold, a sun climbs into it on the same curve as the light, and the time
// stays readable the whole way, because the first thing anybody does is squint at it.

// sunriseFace is whether the sun is drawn with a face on it, which is a matter of taste rather than
// of waking up. See config.Alarms.SunriseFace.

// sunrisePage draws the sky, the sun and the clock over it. p is 0 to 1, as sunriseProgress reports.
func (r *renderer) sunrisePage(s scene, p float64, face bool) {
	p = math.Min(math.Max(p, 0), 1)
	r.sunriseSky(p)
	r.sunriseSun(p, face)
	r.sunriseClock(s, p)
}

// sunriseSky fills the screen, dark red at the start and pale gold by the end, with the horizon
// lighter than the top so the light seems to come from below.
func (r *renderer) sunriseSky(p float64) {
	top := lerpRGB(color.RGBA{26, 8, 6, 255}, color.RGBA{232, 158, 78, 255}, p)
	bottom := lerpRGB(color.RGBA{74, 16, 8, 255}, color.RGBA{255, 214, 140, 255}, p)
	for y := 0; y < r.h; y++ {
		f := float64(y) / float64(r.h-1)
		row := lerpRGB(top, bottom, f)
		draw.Draw(r.dst, image.Rect(0, y, r.w, y+1), image.NewUniform(row), image.Point{}, draw.Src)
	}
}

// sunriseSun climbs from below the bottom edge towards the middle, growing and paling as it comes:
// deep red at the horizon, gold by the time it is up, as a real one does.
func (r *renderer) sunriseSun(p float64, face bool) {
	// It stops short of the clock: the time has to stay readable, which matters more than the sun
	// getting all the way up.
	rad := int(float64(r.h) * (0.15 + 0.07*p))
	cy := int(float64(r.h)*1.12 - float64(r.h)*0.40*p)
	cx := r.w / 2
	body := lerpRGB(color.RGBA{219, 74, 28, 255}, color.RGBA{255, 246, 205, 255}, p)

	// A soft edge: rings of the sun's color, each a little wider and fainter than the last, so it
	// sits in the sky rather than being cut out of it.
	for i := 6; i > 0; i-- {
		r.disc(cx, cy, rad+i*rad/8, faded(body, uint8(18+8*i)))
	}
	r.disc(cx, cy, rad, body)

	if !face {
		return
	}
	// The face, in the sun's own color darkened, so it is there without being a cartoon on a lamp.
	ink := color.RGBA{uint8(float64(body.R) * 0.35), uint8(float64(body.G) * 0.25), uint8(float64(body.B) * 0.2), 255}
	eye := max(rad/9, 3)
	r.disc(cx-rad/3, cy-rad/4, eye, ink)
	r.disc(cx+rad/3, cy-rad/4, eye, ink)
	// The smile: a row of discs along an arc, thicker in the middle.
	for i := -6; i <= 6; i++ {
		t := float64(i) / 6
		x := cx + int(t*float64(rad)*0.52)
		y := cy + int(float64(rad)*(0.18+0.30*(1-t*t)))
		r.disc(x, y, max(rad/14, 2), ink)
	}
}

// sunriseClock is the time over the sky: dark ink once the sky is light, pale while it is still dim,
// so it can be read at either end.
func (r *renderer) sunriseClock(s scene, p float64) {
	ink := lerpRGB(color.RGBA{255, 236, 214, 255}, color.RGBA{58, 22, 10, 255}, math.Min(p*1.6, 1))
	hour := clockHM(s.now)
	ampm := clockSuffix(s.now)
	hw := r.width(r.clock, hour)
	aw := r.width(r.ampm, ampm)
	gap := 18
	if ampm == "" {
		gap = 0
	}
	x := (r.w - hw - gap - aw) / 2
	base := r.h/2 - 56
	r.text(r.clock, hour, x, base, ink)
	r.text(r.ampm, ampm, x+hw+gap, base, ink)
	date := s.now.Format("Monday, January 2")
	r.text(r.small, date, (r.w-r.width(r.small, date))/2, base+64, ink)
}

// faded is a color at a given alpha, premultiplied — which is what Go's RGBA holds, so dimming the
// alpha alone leaves a color that is not the one that was asked for.
func faded(c color.RGBA, a uint8) color.RGBA {
	f := float64(a) / 255
	return color.RGBA{uint8(float64(c.R) * f), uint8(float64(c.G) * f), uint8(float64(c.B) * f), a}
}

// lerpRGB mixes two colors, t from 0 (a) to 1 (b).
func lerpRGB(a, b color.RGBA, t float64) color.RGBA {
	t = math.Min(math.Max(t, 0), 1)
	mix := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
	return color.RGBA{mix(a.R, b.R), mix(a.G, b.G), mix(a.B, b.B), 255}
}
