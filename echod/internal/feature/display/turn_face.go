//go:build !dot

package display

import (
	"image"
	"image/color"
	"math"
	"time"
)

// The Face turn screen: a face of a few glowing strokes, two rounded eyes and a mouth that is a line
// until it speaks, in the manner of the little desk companions that have one. It shows what the
// device is doing the way a face would: listening, it looks at you with wide eyes, head a little to one
// side, the eyes swelling with the voice; thinking, it squints up and away with its mouth pulled to the
// side and dots rising beside it; answering, the mouth opens and closes with the words being spoken;
// and when it has said its piece it smiles with its eyes. It blinks now and then, and its look wanders
// a little, so it is never quite still.
//
// One expression eases into the next over a fraction of a second rather than jumping, so the face
// keeps what it looked like between frames (faceNow). Only what moves with the voice (the mouth
// talking, the eyes swelling) follows the levels directly.

// faceLook is one expression, its sizes as shares of the face's height.
type faceLook struct {
	eyeW, eyeH  float64 // an eye's size
	eyeR        float64 // the roundness of its corners, as a share of its width
	gap         float64 // between the eyes' middles
	lookX       float64 // where the eyes look, from straight ahead
	lookY       float64
	happy       float64 // 0 the rounded eyes, 1 the smiling arcs (^ ^)
	mouthW      float64
	mouthOpen   float64 // how far the mouth is open, as a height
	mouthSmile  float64 // how far its middle sits below its corners
	mouthX      float64 // its middle, from under the middle of the eyes
	mouthTilt   float64 // radians
	tilt        float64 // the whole face's, radians
	dots        float64 // the thinking dots, 0 to 1
	stroke      float64 // the width of the mouth's line and the smiling eyes' arcs
	glow        float64 // how far the glow reaches
	blinkEvery  float64 // seconds between blinks, about
	speechDepth float64 // how far the mouth opens with the voice being spoken
	listenDepth float64 // how far the eyes swell with the voice being heard
}

// faceRest is the face at ease, and what each phase changes of it.
var faceRest = faceLook{
	eyeW: 0.17, eyeH: 0.25, eyeR: 0.24, gap: 0.27, mouthW: 0.24, stroke: 0.034, glow: 0.05,
	blinkEvery: 4.2,
}

// faceFor is the expression the face moves toward in phase at t, in seconds.
func faceFor(phase string, t float64) faceLook {
	f := faceRest
	switch phase {
	case "listening":
		// Wide awake and turned a little to one side, the eyes glancing about the person talking.
		f.eyeW, f.eyeH = 0.18, 0.28
		f.tilt = 0.07
		f.lookX, f.lookY = glance(t, 1.7, 0.035), -0.015+glance(t+40, 2.3, 0.015)
		f.mouthW, f.mouthSmile = 0.16, 0.012
		f.listenDepth = 0.14
	case "thinking":
		// Squinting up and away, the mouth pulled to the side, the dots rising.
		f.eyeH = 0.15
		f.lookX = 0.07 + 0.025*math.Sin(t*0.9)
		f.lookY = -0.07 + 0.012*math.Sin(t*1.7)
		f.tilt = -0.05
		f.mouthW, f.mouthX, f.mouthTilt, f.mouthSmile = 0.12, 0.06, -0.18, -0.006
		f.dots = 1
		f.blinkEvery = 7
	case "replying":
		f.lookX, f.lookY = glance(t, 2.6, 0.02), glance(t+17, 3.1, 0.01)
		f.mouthW, f.mouthSmile = 0.22, 0.02
		f.speechDepth = 0.11
	case "lingering":
		// Said its piece: the eyes smile.
		f.happy = 1
		f.mouthW, f.mouthSmile = 0.2, 0.045
		f.blinkEvery = 0
	}
	return f
}

// glance is a look that moves to a new spot every so often, as eyes do, each within reach of straight
// ahead: the same at the same t, so a frame drawn twice is drawn the same.
func glance(t, every, reach float64) float64 {
	n := math.Floor(t / every)
	return reach * (2*faceHash(n) - 1)
}

// faceHash is a number from 0 to 1 for n, the same for the same n.
func faceHash(n float64) float64 {
	v := math.Sin(n*127.1+311.7) * 43758.5453
	return v - math.Floor(v)
}

// blinkAt is how far open the eyes are at t, 0 to 1, for blinks about every seconds apart: each
// takes a sixth of a second, and now and then one is a double.
func blinkAt(t, every float64) float64 {
	if every <= 0 {
		return 1
	}
	n := math.Floor(t / every)
	start := n*every + faceHash(n)*every*0.6
	open := 1.0
	for i, at := range []float64{start, start + 0.32} {
		if i == 1 && faceHash(n+0.5) < 0.7 {
			break
		}
		if d := t - at; d >= 0 && d < 0.16 {
			open = min(open, math.Abs(math.Cos(math.Pi*d/0.16)))
		}
	}
	return open
}

func bool01(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// faceState is the face as last drawn, for the next frame to ease from.
var faceState struct {
	look faceLook
	at   time.Time
	set  bool
}

// glowReach is how far the glow goes, in the glow's own reach (faceLook.glow).
const glowReach = 3.0

// faceEase is how quickly the face takes up a new expression: about this long to get most of the way.
const faceEase = 0.16

// faceNow moves the face kept toward target, by how long it has been since the last frame. A first
// frame, a clock going back or a long pause starts it at the target.
func faceNow(target faceLook, now time.Time) faceLook {
	dt := now.Sub(faceState.at).Seconds()
	if !faceState.set || dt < 0 || dt > 1.5 {
		faceState.look, faceState.at, faceState.set = target, now, true
		return target
	}
	k := 1 - math.Exp(-dt/faceEase)
	f := &faceState.look
	for _, p := range [][2]*float64{
		{&f.eyeW, &target.eyeW}, {&f.eyeH, &target.eyeH}, {&f.eyeR, &target.eyeR}, {&f.gap, &target.gap},
		{&f.lookX, &target.lookX}, {&f.lookY, &target.lookY}, {&f.happy, &target.happy},
		{&f.mouthW, &target.mouthW}, {&f.mouthOpen, &target.mouthOpen}, {&f.mouthSmile, &target.mouthSmile},
		{&f.mouthX, &target.mouthX}, {&f.mouthTilt, &target.mouthTilt}, {&f.tilt, &target.tilt},
		{&f.dots, &target.dots}, {&f.stroke, &target.stroke}, {&f.glow, &target.glow},
		{&f.speechDepth, &target.speechDepth}, {&f.listenDepth, &target.listenDepth},
	} {
		*p[0] += (*p[1] - *p[0]) * k
	}
	f.blinkEvery = target.blinkEvery
	faceState.at = now
	return *f
}

// faceSettled is whether the face has stopped moving toward its expression, for a turn that is over:
// no more frames are needed once it has. Any other turn screen is settled.
func faceSettled(v *eqView) bool {
	if v == nil || v.style != "face" || !faceState.set {
		return true
	}
	target := faceFor(v.phase, 0)
	f := faceState.look
	return math.Abs(f.happy-target.happy) < 0.02 && math.Abs(f.mouthSmile-target.mouthSmile) < 0.002 &&
		math.Abs(f.eyeH-target.eyeH) < 0.003 && math.Abs(f.tilt-target.tilt) < 0.003 && f.dots < 0.02
}

// faceColors are the face's strokes and glow: a cool white glowing a little in the phase's color, dim
// reds at night, and on a light ground a dark ink with no glow at all.
func faceColors(phase string, night bool, ground color.RGBA) (ink, glow color.RGBA, glows bool) {
	switch {
	case night:
		return color.RGBA{255, 96, 70, 255}, color.RGBA{170, 24, 12, 255}, true
	case luma(ground.R, ground.G, ground.B) > 128:
		return mix(ground, color.RGBA{0, 0, 0, 255}, 0.85), ground, false
	case phase == "listening":
		return color.RGBA{238, 246, 255, 255}, color.RGBA{70, 160, 255, 255}, true
	case phase == "thinking":
		return color.RGBA{242, 238, 255, 255}, color.RGBA{150, 100, 255, 255}, true
	}
	return color.RGBA{255, 250, 240, 255}, color.RGBA{255, 170, 90, 255}, true
}

// drawFace draws the face in the middle of band, over ground, for the turn in v at now.
func drawFace(dst *image.RGBA, band image.Rectangle, v *eqView, now time.Time, ground color.RGBA) {
	t := float64(now.UnixMilli()%(1<<40)) / 1000
	f := faceNow(faceFor(v.phase, t), now)
	e := turnEnergy(v)
	ink, glowC, glows := faceColors(v.phase, v.night, ground)

	u := float64(min(band.Dx(), band.Dy()))
	cx, cy := float64(band.Min.X+band.Max.X)/2, float64(band.Min.Y+band.Max.Y)/2
	sin, cos := math.Sincos(f.tilt)
	// toFace is a point on the screen in the face's own coordinates, in shares of u from its middle.
	toFace := func(x, y float64) (float64, float64) {
		dx, dy := (x-cx)/u, (y-cy)/u
		return dx*cos + dy*sin, -dx*sin + dy*cos
	}
	// box is the screen's pixels around a part of the face that reaches rx across and ry up and down
	// from x, y in its coordinates.
	box := func(x, y, rx, ry float64) image.Rectangle {
		sx, sy := cx+(x*cos-y*sin)*u, cy+(x*sin+y*cos)*u
		w := int((rx*math.Abs(cos)+ry*math.Abs(sin))*u) + 2
		h := int((rx*math.Abs(sin)+ry*math.Abs(cos))*u) + 2
		return image.Rect(int(sx)-w, int(sy)-h, int(sx)+w, int(sy)+h).Intersect(dst.Rect)
	}
	glowR := f.glow
	if !glows {
		glowR = 0
	}
	// paint lays a part of the face over the pixels of b: dist is how far a point (in face coordinates)
	// is from its edge, negative inside, and strength how much of it shows. Worked out a 2 by 2 block at
	// a time, and pixel by pixel only for a block the edge runs through: the glow is soft enough not to
	// show the blocks, and a frame costs a quarter.
	px := 1 / u
	at := func(x, y float64, dist func(x, y float64) float64) float64 {
		fx, fy := toFace(x, y)
		return dist(fx, fy) / px
	}
	put := func(x, y int, d, strength float64) {
		// The glow falls away to nothing glowReach out, so it never ends at the edge of b.
		if q := d * px / glowR; glowR > 0 && d > -0.5 && q < glowReach {
			fall := 1 - q/glowReach
			if a := strength * 0.42 * fall * fall / (1 + q*q*3); a > 0.5/255 {
				turnPut(dst, x, y, glowC, a)
			}
		}
		if d < 0.5 {
			turnPut(dst, x, y, ink, strength*clamp01(0.5-d))
		}
	}
	paint := func(b image.Rectangle, strength float64, dist func(x, y float64) float64) {
		if strength <= 0.01 {
			return
		}
		for y := b.Min.Y; y < b.Max.Y; y += 2 {
			for x := b.Min.X; x < b.Max.X; x += 2 {
				d := at(float64(x)+1, float64(y)+1, dist)
				if math.Abs(d) < 2.5 {
					for k := range 4 {
						xx, yy := x+k%2, y+k/2
						put(xx, yy, at(float64(xx)+0.5, float64(yy)+0.5, dist), strength)
					}
					continue
				}
				for k := range 4 {
					put(x+k%2, y+k/2, d, strength)
				}
			}
		}
	}

	// The eyes: rounded, swelling with a voice being heard, closing to a line for a blink.
	swell := 1 + f.listenDepth*e
	open := blinkAt(t, f.blinkEvery)
	// Into a smile the eyes close first, over the first half of the way, and the arcs come up over the
	// second: a face, not two pictures one over the other.
	shut := clamp01(1 - 2*f.happy)
	arcs := clamp01(2*f.happy - 1)
	ew, eh := f.eyeW*swell, max(f.eyeH*swell*open*shut, f.stroke)
	ey := -0.12 + f.lookY
	for _, side := range []float64{-1, 1} {
		ex := side*f.gap/2 + f.lookX
		// Room for the rounded eye and the smiling one, which is a little wider and sits a little lower.
		g, ar, ay := glowR*glowReach, ew*0.55, ey+f.eyeH*0.18
		b := box(ex, ey, math.Max(ew/2, ar+f.stroke/2)+g, math.Max(eh/2, f.eyeH*0.18+ar+f.stroke)+g)
		rad := math.Min(ew*f.eyeR*2, math.Min(ew, eh)/2)
		paint(b, bool01(f.happy < 0.5), func(x, y float64) float64 {
			return rrDist(x, y, ex-ew/2, ey-eh/2, ex+ew/2, ey+eh/2, rad)
		})
		// The smiling eye: an arch the eye's width, its ends down.
		paint(b, arcs, func(x, y float64) float64 {
			dx, dy := x-ex, y-ay
			if dy > 0 {
				return math.Hypot(math.Abs(dx)-ar, dy) - f.stroke/2
			}
			return math.Abs(math.Hypot(dx, dy)-ar) - f.stroke/2
		})
	}

	// The mouth: a line, curved up for a smile, opening into a rounded shape as it talks, a little
	// narrower the wider it opens.
	talk := f.speechDepth * e
	if talk > 0 {
		talk *= 0.7 + 0.3*math.Abs(math.Sin(t*11)) // syllables, so a steady voice still moves it
	}
	mo := f.mouthOpen + talk
	mx, my := f.mouthX+f.lookX*0.4, 0.2+f.lookY*0.4+mo*0.3
	ms, mc := math.Sincos(-f.mouthTilt)
	hw := f.mouthW / 2 * (1 - 0.35*clamp01(mo/f.mouthW))
	mh := mo + f.stroke
	paint(box(mx, my, hw+glowR*glowReach, mh/2+math.Abs(f.mouthSmile)+glowR*glowReach), 1, func(x, y float64) float64 {
		dx, dy := x-mx, y-my
		lx, ly := dx*mc-dy*ms, dx*ms+dy*mc
		k := clamp01(math.Abs(lx) / hw)
		ly -= f.mouthSmile * (1 - k*k)
		return rrDist(lx, ly, -hw, -mh/2, hw, mh/2, math.Min(hw, mh/2))
	})

	// The thinking dots, rising beside the face, lighting one after another.
	for i := range 3 {
		fi := float64(i)
		r := 0.018 + 0.008*fi
		dx, dy := 0.33+0.07*fi, -0.3-0.07*fi
		pulse := 0.35 + 0.65*math.Pow(0.5+0.5*math.Sin(t*4-fi*1.1), 2)
		paint(box(dx, dy, r+glowR*glowReach, r+glowR*glowReach), f.dots*pulse, func(x, y float64) float64 {
			return math.Hypot(x-dx, y-dy) - r
		})
	}
}
