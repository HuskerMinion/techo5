//go:build !dot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"time"
)

// Three more turn screens beside the wave and the bars (turn_draw.go), on the Show and the Spot alike,
// each drawn from the same levels the bars are: VU, a pair of moving-needle meters; Orb, a ball of light
// that swells with the voice; and HAL 9000, the computer's red eye, glowing harder as it hears and
// speaks. Each fills a band of the screen over the turn page's ground and leaves the words to the page.

// turnEnergy is how loud the voice is now, 0 to 1, from the bands: their mean, lifted so that ordinary
// speech moves things well.
func turnEnergy(v *eqView) float64 {
	if len(v.level) == 0 {
		return 0
	}
	sum := 0.0
	for _, l := range v.level {
		sum += l
	}
	return clamp01(math.Sqrt(sum/float64(len(v.level))) * 1.35)
}

// halfEnergy is turnEnergy over one half of the bands: the low half or the high half, the VU meters'
// left and right.
func halfEnergy(v *eqView, high bool) float64 {
	n := len(v.level) / 2
	part := v.level[:n]
	if high {
		part = v.level[n:]
	}
	if len(part) == 0 {
		return 0
	}
	sum := 0.0
	for _, l := range part {
		sum += l
	}
	return clamp01(math.Sqrt(sum/float64(len(part))) * 1.5)
}

// turnPut lays c over the pixel at x, y with coverage a, for the drawings here.
func turnPut(dst *image.RGBA, x, y int, c color.RGBA, a float64) {
	if a <= 0 || !(image.Point{x, y}).In(dst.Rect) {
		return
	}
	a = math.Min(a, 1)
	i := dst.PixOffset(x, y)
	p := dst.Pix[i : i+4 : i+4]
	p[0] = uint8(float64(p[0])*(1-a) + float64(c.R)*a + 0.5)
	p[1] = uint8(float64(p[1])*(1-a) + float64(c.G)*a + 0.5)
	p[2] = uint8(float64(p[2])*(1-a) + float64(c.B)*a + 0.5)
	p[3] = 255
}

// turnLine draws a soft line w wide from x0, y0 to x1, y1.
func turnLine(dst *image.RGBA, x0, y0, x1, y1, w float64, c color.RGBA, alpha float64) {
	dx, dy := x1-x0, y1-y0
	l2 := dx*dx + dy*dy
	for y := int(math.Min(y0, y1) - w - 1); y <= int(math.Max(y0, y1)+w+1); y++ {
		for x := int(math.Min(x0, x1) - w - 1); x <= int(math.Max(x0, x1)+w+1); x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			t := 0.0
			if l2 > 0 {
				t = clamp01(((px-x0)*dx + (py-y0)*dy) / l2)
			}
			d := math.Hypot(px-(x0+t*dx), py-(y0+t*dy)) - w/2
			turnPut(dst, x, y, c, clamp01(0.5-d)*alpha)
		}
	}
}

// The meters' colors: an ivory face lit warm from behind, black marks, the red of the scale's top, and
// at night a face dimmed to a glow.
var (
	vuFace      = color.RGBA{238, 226, 196, 255}
	vuFaceNight = color.RGBA{90, 30, 22, 255}
	vuInk       = color.RGBA{30, 26, 22, 255}
	vuInkNight  = color.RGBA{255, 110, 80, 255}
	vuRed       = color.RGBA{200, 36, 30, 255}
)

// keptStep is the steps, in pixels, a kept drawing's height goes in: a long answer streaming in takes
// the band down a little at a time, and each new height would be a new drawing every frame.
const keptStep = 16

// keptBand is band with its height taken down to a whole number of keptSteps, in the middle of where
// it was.
func keptBand(band image.Rectangle) image.Rectangle {
	h := max(band.Dy()/keptStep*keptStep, min(band.Dy(), keptStep))
	top := band.Min.Y + (band.Dy()-h)/2
	return image.Rect(band.Min.X, top, band.Max.X, top+h)
}

// vuKept is the meters' faces as last drawn, without their needles: they only change with the band's
// size, the ground and the night, so a frame copies them and draws two needles.
var vuKept struct {
	key    [4]int
	ground color.RGBA
	img    *image.RGBA
}

// drawVU is two moving-needle meters side by side in band, the low voices on the left and the high on
// the right: an arc of marks, the top fifth of it red, and the needle swinging up it.
func drawVU(dst *image.RGBA, band image.Rectangle, v *eqView, ground color.RGBA) {
	band = keptBand(band)
	face, ink := vuFace, vuInk
	night := 0
	if v.night {
		face, ink, night = vuFaceNight, vuInkNight, 1
	}
	gap := band.Dx() / 24
	w := min((band.Dx()-gap*3)/2, band.Dy()*16/10)
	h := w * 10 / 16
	y0 := (band.Dy() - h) / 2
	x0 := (band.Dx() - 2*w - gap) / 2
	meters := [2]image.Rectangle{}
	for i := range meters {
		meters[i] = image.Rect(x0+i*(w+gap), y0, x0+i*(w+gap)+w, y0+h)
	}
	key := [4]int{band.Dx(), band.Dy(), night, 0}
	if vuKept.img == nil || vuKept.key != key || vuKept.ground != ground {
		img := image.NewRGBA(image.Rect(0, 0, band.Dx(), band.Dy()))
		draw.Draw(img, img.Rect, image.NewUniform(ground), image.Point{}, draw.Src)
		for _, b := range meters {
			drawMeterFace(img, b, face, ink, ground)
		}
		vuKept.key, vuKept.ground, vuKept.img = key, ground, img
	}
	draw.Draw(dst, band, vuKept.img, image.Point{}, draw.Src)
	for i, high := range []bool{false, true} {
		b := meters[i].Add(band.Min)
		drawNeedle(dst, b, halfEnergy(v, high), ink)
		// The lip of the case over the pivot, again over the needle: it comes out from under it.
		lip := image.Rect(b.Min.X, b.Max.Y-b.Dy()/7, b.Max.X, b.Max.Y)
		draw.Draw(dst, lip, vuKept.img, lip.Min.Sub(band.Min), draw.Src)
	}
}

// meterPivot is where a meter in b swings from, below its face, and the scale's radius.
func meterPivot(b image.Rectangle) (px, py, arc float64) {
	return float64(b.Min.X+b.Max.X) / 2, float64(b.Max.Y) + float64(b.Dy())*0.35, float64(b.Dy())
}

// drawNeedle is a meter's needle at level, 0 to 1.
func drawNeedle(dst *image.RGBA, b image.Rectangle, level float64, ink color.RGBA) {
	px, py, arc := meterPivot(b)
	at := func(r, deg float64) (float64, float64) {
		a := (deg - 90) * math.Pi / 180
		return px + r*math.Cos(a), py + r*math.Sin(a)
	}
	thin := math.Max(1, float64(b.Dy())/90)
	deg := -50 + 100*clamp01(level)
	// From just above the lip, so none of it shows below the face at the scale's ends.
	x0, y0 := at(float64(b.Dy())*0.62, deg)
	x1, y1 := at(arc+float64(b.Dy())*0.04, deg)
	turnLine(dst, x0, y0, x1, y1, thin*1.6, ink, 1)
}

// drawMeterFace is one meter in b without its needle: its face, the scale, and the lip of the case.
func drawMeterFace(dst *image.RGBA, b image.Rectangle, face, ink, ground color.RGBA) {
	rad := float64(b.Dy()) * 0.12
	// The face: a rounded panel, lighter in the middle as if lit from behind.
	cx, cy := float64(b.Min.X+b.Max.X)/2, float64(b.Min.Y+b.Max.Y)/2
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			d := rrDist(float64(x)+0.5, float64(y)+0.5, float64(b.Min.X), float64(b.Min.Y), float64(b.Max.X), float64(b.Max.Y), rad)
			if d > 0.5 {
				continue
			}
			k := math.Hypot((float64(x)-cx)/float64(b.Dx()), (float64(y)-cy)/float64(b.Dy()))
			turnPut(dst, x, y, mix(face, ground, 0.35*k), clamp01(0.5-d))
		}
	}
	// The scale: an arc around a pivot below the face, from -50 to +50 degrees.
	px, py, arc := meterPivot(b)
	at := func(r, deg float64) (float64, float64) {
		a := (deg - 90) * math.Pi / 180
		return px + r*math.Cos(a), py + r*math.Sin(a)
	}
	thin := math.Max(1, float64(b.Dy())/90)
	for i := 0; i <= 20; i++ {
		deg := -50 + 100*float64(i)/20
		c := ink
		if i >= 16 {
			c = vuRed
		}
		long := i%4 == 0
		in := arc - float64(b.Dy())*0.07
		if long {
			in = arc - float64(b.Dy())*0.13
		}
		x0, y0 := at(arc, deg)
		x1, y1 := at(in, deg)
		w := thin
		if long {
			w *= 1.8
		}
		turnLine(dst, x0, y0, x1, y1, w, c, 1)
	}
	// The arc itself, black up to the red, red past it.
	for i := 0; i < 100; i++ {
		d0, d1 := -50+float64(i), -50+float64(i+1)
		c := ink
		if i >= 80 {
			c = vuRed
		}
		x0, y0 := at(arc, d0)
		x1, y1 := at(arc, d1)
		turnLine(dst, x0, y0, x1, y1, thin*1.4, c, 1)
	}
	// The lip of the case over the pivot: the needle comes out from under it (drawVU).
	lip := image.Rect(b.Min.X, b.Max.Y-b.Dy()/7, b.Max.X, b.Max.Y)
	for y := lip.Min.Y; y < lip.Max.Y; y++ {
		for x := lip.Min.X; x < lip.Max.X; x++ {
			d := rrDist(float64(x)+0.5, float64(y)+0.5, float64(b.Min.X), float64(b.Min.Y), float64(b.Max.X), float64(b.Max.Y), rad)
			if d > 0.5 {
				continue
			}
			turnPut(dst, x, y, mix(ink, ground, 0.55), clamp01(0.5-d))
		}
	}
}

// orbColors are the orb's colors for each phase: cool while it listens, violet while it thinks, warm
// while it answers; and dim reds at night.
func orbColors(phase string, night bool) (core, glow color.RGBA) {
	switch {
	case night:
		return color.RGBA{255, 120, 90, 255}, color.RGBA{150, 20, 12, 255}
	case phase == "listening":
		return color.RGBA{200, 245, 255, 255}, color.RGBA{30, 150, 255, 255}
	case phase == "thinking":
		return color.RGBA{235, 210, 255, 255}, color.RGBA{140, 70, 255, 255}
	}
	return color.RGBA{255, 240, 200, 255}, color.RGBA{255, 140, 40, 255}
}

// drawOrb is a ball of light in the middle of band, swelling with the voice: a bright core, a glow
// falling away round it, and a faint ring at its edge that wavers with the bands.
func drawOrb(dst *image.RGBA, band image.Rectangle, v *eqView, phase string, now time.Time) {
	core, glow := orbColors(phase, v.night)
	e := turnEnergy(v)
	t := float64(now.UnixMilli()%100000) / 1000
	if phase == "thinking" {
		e = 0.25 + 0.15*math.Sin(t*2.4) // a slow breath while it works it out
	}
	cx, cy := float64(band.Min.X+band.Max.X)/2, float64(band.Min.Y+band.Max.Y)/2
	base := float64(band.Dy()) * 0.26
	r0 := base * (0.75 + 0.6*e)
	reach := math.Min(r0*3, float64(band.Dy())/2+base)
	n := len(v.level)
	// Worked out a 2 by 2 block at a time: a glow this soft loses nothing, and a frame costs a quarter.
	block := func(x, y int, c color.RGBA, a float64) {
		turnPut(dst, x, y, c, a)
		turnPut(dst, x+1, y, c, a)
		turnPut(dst, x, y+1, c, a)
		turnPut(dst, x+1, y+1, c, a)
	}
	for y := int(cy - reach); y <= int(cy+reach); y += 2 {
		for x := int(cx - reach); x <= int(cx+reach); x += 2 {
			dx, dy := float64(x)+1-cx, float64(y)+1-cy
			d := math.Hypot(dx, dy)
			if d > reach {
				continue
			}
			// The edge wavers with the band at this angle; only near the edge is the angle worth working out.
			edge := r0
			if q0 := d / r0; n > 0 && q0 > 0.8 && q0 < 1.25 {
				ang := math.Atan2(dy, dx)
				k := int((ang + math.Pi) / (2 * math.Pi) * float64(n))
				edge *= 1 + 0.12*v.level[min(k, n-1)]
			}
			q := d / edge
			switch {
			case q < 1:
				// Inside: the core color at the middle, toward the glow color at the edge.
				block(x, y, mix(core, glow, q*q), 1)
			default:
				// Outside: the glow fading with distance, faster the quieter it is.
				f := (q - 1) * 3.2
				a := (0.55 + 0.35*e) / (1 + f*f)
				block(x, y, glow, a)
			}
		}
	}
}

// drawHAL is the computer's eye in the middle of band: a brushed silver rim, the black lens, and a red
// glow in it that brightens as it hears and speaks and breathes slowly while it thinks, around a small
// hot center.
func drawHAL(dst *image.RGBA, band image.Rectangle, v *eqView, phase string, now time.Time) {
	e := turnEnergy(v)
	if phase == "thinking" {
		t := float64(now.UnixMilli()%100000) / 1000
		e = 0.3 + 0.2*math.Sin(t*2)
	}
	rad := float64(keptBand(band).Dy()) * 0.46
	side := int(2*rad) + 4
	step := int(math.Round(clamp01(0.35+0.65*e) * (halSteps - 1)))
	img := halKept(side, step)
	at := image.Pt((band.Min.X+band.Max.X-side)/2, (band.Min.Y+band.Max.Y-side)/2)
	draw.Draw(dst, image.Rectangle{at, at.Add(img.Rect.Size())}, img, image.Point{}, draw.Over)
}

// halSteps is how many brightnesses of the eye are drawn and kept: enough that it glides.
const halSteps = 12

// halEyes keeps the eye at each brightness for one size, drawn on a clear ground and laid over the page.
var halEyes struct {
	side int
	imgs [halSteps]*image.RGBA
}

// halKept is the eye side pixels across at brightness step, drawn the first time it is asked for.
func halKept(side, step int) *image.RGBA {
	if halEyes.side != side {
		halEyes.side, halEyes.imgs = side, [halSteps]*image.RGBA{}
	}
	if img := halEyes.imgs[step]; img != nil {
		return img
	}
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	c := float64(side) / 2
	halEye(img, c, c, c-2, float64(step)/(halSteps-1))
	halEyes.imgs[step] = img
	return img
}

// halEye draws the eye at cx, cy, rad across, its glow at bright from 0 to 1.
func halEye(dst *image.RGBA, cx, cy, rad, bright float64) {
	rim, lens := rad*0.88, rad*0.82
	glowR := lens * (0.7 + 0.3*bright)
	for y := int(cy - rad - 1); y <= int(cy+rad+1); y++ {
		for x := int(cx - rad - 1); x <= int(cx+rad+1); x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			d := math.Hypot(dx, dy)
			switch {
			case d > rad+0.5:
				continue
			case d > rim:
				// Silver, lighter at the top left as if lit from there, darker toward its inner edge.
				k := 0.55 + 0.35*(-dx-dy)/(2*rad)
				k *= 0.75 + 0.25*(d-rim)/(rad-rim)
				g := uint8(70 + 160*clamp01(k))
				turnPut(dst, x, y, color.RGBA{g, g, g + 6, 255}, math.Min(rad+0.5-d, 1))
			case d > lens:
				turnPut(dst, x, y, color.RGBA{6, 6, 8, 255}, 1)
			default:
				t := d / glowR
				c := color.RGBA{10, 0, 0, 255}
				if t < 1 {
					g := math.Pow(1-t, 1.1) * (0.6 + 0.4*bright)
					c = mix(c, color.RGBA{255, 30, 14, 255}, math.Min(g*1.4, 1))
					if t < 0.22 {
						c = mix(c, color.RGBA{255, 225, 140, 255}, (1-t/0.22)*(0.55+0.45*bright))
					}
				}
				// A faint highlight on the glass, top left.
				hx, hy := dx+lens*0.35, dy+lens*0.35
				if h := math.Hypot(hx, hy*1.6); h < lens*0.22 {
					c = mix(c, color.RGBA{255, 255, 255, 255}, 0.18*(1-h/(lens*0.22)))
				}
				turnPut(dst, x, y, c, 1)
			}
		}
	}
}
