//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"math"
)

// Weather icons, drawn rather than shipped: a sun, a moon, clouds, rain, snow, a bolt, fog,
// composed from discs, rounded blocks and strokes so they scale to any size on the panel.
var (
	sunGold   = color.RGBA{0xf2, 0xb1, 0x3c, 0xff}
	sunPale   = color.RGBA{0xf7, 0xd0, 0x7a, 0xff}
	cloudGray = color.RGBA{0xb8, 0xc0, 0xc8, 0xff}
	cloudDark = color.RGBA{0x7c, 0x86, 0x90, 0xff}
	rainBlue  = color.RGBA{0x6f, 0xa8, 0xdc, 0xff}
	snowWhite = color.RGBA{0xe8, 0xf0, 0xf8, 0xff}
	boltGold  = color.RGBA{0xff, 0xd2, 0x4d, 0xff}
	moonPale  = color.RGBA{0xd8, 0xdc, 0xe4, 0xff}
)

// box strokes a rectangle's outline, thick pixels wide.
func (r *renderer) box(rect image.Rectangle, c color.Color, thick int) {
	src := image.NewUniform(c)
	draw.Draw(r.dst, image.Rect(rect.Min.X, rect.Min.Y, rect.Max.X, rect.Min.Y+thick), src, image.Point{}, draw.Over)
	draw.Draw(r.dst, image.Rect(rect.Min.X, rect.Max.Y-thick, rect.Max.X, rect.Max.Y), src, image.Point{}, draw.Over)
	draw.Draw(r.dst, image.Rect(rect.Min.X, rect.Min.Y, rect.Min.X+thick, rect.Max.Y), src, image.Point{}, draw.Over)
	draw.Draw(r.dst, image.Rect(rect.Max.X-thick, rect.Min.Y, rect.Max.X, rect.Max.Y), src, image.Point{}, draw.Over)
}

// faded runs paint onto a scratch layer and lays it over the canvas at the given opacity (0–255),
// for a picture that sits behind the page rather than on it.
func (r *renderer) faded(alpha uint8, paint func()) {
	real := r.dst
	layer := image.NewRGBA(real.Bounds())
	r.dst = layer
	paint()
	r.dst = real
	draw.DrawMask(real, real.Bounds(), layer, image.Point{}, image.NewUniform(color.Alpha{A: alpha}), image.Point{}, draw.Over)
}

// disc fills a circle.
func (r *renderer) disc(cx, cy, rad int, c color.Color) {
	src := image.NewUniform(c)
	for y := -rad; y <= rad; y++ {
		half := int(math.Sqrt(float64(rad*rad - y*y)))
		draw.Draw(r.dst, image.Rect(cx-half, cy+y, cx+half+1, cy+y+1), src, image.Point{}, draw.Over)
	}
}

// stroke draws a line of a given thickness, sampled per pixel — short lines only.
func (r *renderer) stroke(x0, y0, x1, y1, thick int, c color.Color) {
	dx, dy := float64(x1-x0), float64(y1-y0)
	n := int(math.Max(math.Abs(dx), math.Abs(dy)))
	if n == 0 {
		n = 1
	}
	for i := 0; i <= n; i++ {
		t := float64(i) / float64(n)
		r.disc(x0+int(dx*t), y0+int(dy*t), thick/2, c)
	}
}

// cloud is the classic shape: three discs on a rounded base, its bottom edge at y, width w.
func (r *renderer) cloud(cx, y, w int, c color.Color) {
	h := w / 3
	r.disc(cx-w/4, y-h/2, h/2, c)
	r.disc(cx+w/6, y-h/2, h*2/3, c)
	r.disc(cx-w/16, y-h*5/6, h*3/5, c)
	draw.Draw(r.dst, image.Rect(cx-w/4, y-h/2, cx+w/6+h*2/3, y), image.NewUniform(c), image.Point{}, draw.Over)
}

func (r *renderer) sun(cx, cy, rad int) {
	for i := 0; i < 8; i++ {
		a := float64(i) * math.Pi / 4
		x0 := cx + int(float64(rad)*1.35*math.Cos(a))
		y0 := cy + int(float64(rad)*1.35*math.Sin(a))
		x1 := cx + int(float64(rad)*1.8*math.Cos(a))
		y1 := cy + int(float64(rad)*1.8*math.Sin(a))
		r.stroke(x0, y0, x1, y1, max(rad/5, 2), sunPale)
	}
	r.disc(cx, cy, rad, sunGold)
}

func (r *renderer) moon(cx, cy, rad int) {
	r.disc(cx, cy, rad, moonPale)
	r.disc(cx+rad/2, cy-rad/3, rad*4/5, walnut)
}

func (r *renderer) drops(cx, y, w int, c color.Color, n int) {
	for i := 0; i < n; i++ {
		x := cx - w/3 + i*(w*2/3)/max(n-1, 1)
		r.stroke(x+4, y+4, x-2, y+w/5, max(w/16, 2), c)
	}
}

func (r *renderer) flakes(cx, y, w int) {
	for i := 0; i < 3; i++ {
		x := cx - w/3 + i*(w/3)
		r.disc(x, y+w/8+(i%2)*w/12, max(w/14, 2), snowWhite)
	}
}

func (r *renderer) bolt(cx, y, w int) {
	s := w / 5
	r.stroke(cx+s/2, y-s/2, cx-s/2, y+s, max(s/3, 2), boltGold)
	r.stroke(cx-s/2, y+s, cx+s/2, y+s, max(s/3, 2), boltGold)
	r.stroke(cx+s/2, y+s, cx-s/3, y+s*5/2, max(s/3, 2), boltGold)
}

func (r *renderer) fog(cx, y, w int) {
	for i := 0; i < 3; i++ {
		yy := y - w/6 + i*(w/8)
		r.stroke(cx-w/2, yy, cx+w/2, yy, max(w/18, 2), cloudGray)
	}
}

// weatherIcon draws Home Assistant's condition centered at cx, cy in a box about size across.
func (r *renderer) weatherIcon(cond string, cx, cy, size int) {
	w := size
	switch cond {
	case "sunny":
		r.sun(cx, cy, w/4)
	case "clear-night":
		r.moon(cx, cy, w/4)
	case "partlycloudy":
		r.sun(cx-w/6, cy-w/6, w/6)
		r.cloud(cx+w/12, cy+w/4, w*3/4, cloudGray)
	case "cloudy":
		r.cloud(cx, cy+w/4, w, cloudGray)
	case "rainy":
		r.cloud(cx, cy+w/8, w*4/5, cloudGray)
		r.drops(cx, cy+w/6, w*4/5, rainBlue, 3)
	case "pouring":
		r.cloud(cx, cy+w/8, w*4/5, cloudDark)
		r.drops(cx, cy+w/6, w*4/5, rainBlue, 5)
	case "lightning", "lightning-rainy":
		r.cloud(cx, cy+w/8, w*4/5, cloudDark)
		r.bolt(cx, cy+w/5, w)
		if cond == "lightning-rainy" {
			r.drops(cx+w/4, cy+w/6, w/2, rainBlue, 2)
		}
	case "snowy", "snowy-rainy":
		r.cloud(cx, cy+w/8, w*4/5, cloudGray)
		r.flakes(cx, cy+w/6, w*4/5)
		if cond == "snowy-rainy" {
			r.drops(cx+w/4, cy+w/6, w/3, rainBlue, 2)
		}
	case "fog":
		r.cloud(cx, cy, w*3/4, cloudGray)
		r.fog(cx, cy+w/4, w)
	case "hail":
		r.cloud(cx, cy+w/8, w*4/5, cloudDark)
		r.flakes(cx, cy+w/6, w*4/5)
	case "windy", "windy-variant":
		r.cloud(cx, cy+w/8, w*3/4, cloudGray)
		r.fog(cx+w/6, cy+w/3, w*3/4)
	case "exceptional":
		r.disc(cx, cy, w/4, boltGold)
	default:
		r.cloud(cx, cy+w/4, w, cloudGray)
	}
}
