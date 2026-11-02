//go:build !dot

package display

import (
	"image"
	"image/color"
	"math"
)

// Rain on the glass and a storm's lightning, drawn over the weather page and the weather art like the
// rest of weatherfx.go: nothing is kept between frames, so each drop and each strike comes from the time
// and its own number.
//
// A drop on a window is a small lens: it shows the scene behind it upside down and smaller, with a dark
// rim where it bends the light away, a bright spot where the sky shines off it and a lighter crescent at
// its foot where the light gathers. None is a perfect circle. The big ones gather weight and run down in
// fits and starts, wandering a little, and leave a wet trail with a bead or two in it that dries away.

// glassRain is n drops on the glass over a, each living its own few seconds: it lands, sits, and either
// dries away or, if it is big enough, runs down.
func (p *paint) glassRain(secs float64, a image.Rectangle, n int, alpha float64) {
	if a.Empty() {
		return
	}
	w, h := float64(a.Dx()), float64(a.Dy())
	// A mist of tiny droplets, three for every drop, each there for half a minute or so.
	for i := range 3 * n {
		period := 20 + 25*rnd(i, 81)
		u := secs + rnd(i, 82)*period
		cycle := math.Floor(u / period)
		u -= cycle * period
		seed := i*104729 + int(math.Mod(cycle, 1000003)) + 7
		fade := clamp01(u/0.3) * clamp01((period-u)/3) * alpha * 0.8
		r := float64(p.s(1)) * (0.9 + 1.6*rnd(seed, 1))
		p.glassDrop(float64(a.Min.X)+rnd(seed, 3)*w, float64(a.Min.Y)+rnd(seed, 4)*h, r, 0, seed, fade)
	}
	for i := range n {
		period := 8 + 10*rnd(i, 61)
		u := secs + rnd(i, 62)*period
		cycle := math.Floor(u / period)
		u -= cycle * period
		seed := i*7919 + int(math.Mod(cycle, 1000003))

		r0 := float64(p.s(2)) + float64(p.s(13))*rnd(seed, 1)*rnd(seed, 2) // most drops are small
		x0 := float64(a.Min.X) + rnd(seed, 3)*w
		y0 := float64(a.Min.Y) + rnd(seed, 4)*h*0.85
		landed := clamp01(u / 0.15)
		fade := landed * clamp01((period-u)/1.2) * alpha
		if fade <= 0 {
			continue
		}
		runs := r0 > float64(p.s(6)) && rnd(seed, 5) < 0.6
		start := 1 + rnd(seed, 6)*(period-7)
		if !runs || u < start {
			p.glassDrop(x0, y0, r0*(0.7+0.3*landed), 0, seed, fade)
			continue
		}
		// Running: a speed that comes and goes, as a drop sticks and lets go, so it moves in fits.
		t := u - start
		v := h * (0.1 + 0.14*rnd(seed, 7))
		f := 1.2 + 1.3*rnd(seed, 8)
		// A drop runs until it has left too much of itself behind, then stops where it is.
		dist := math.Min(v*(t-math.Sin(2*math.Pi*f*t)/(2*math.Pi*f)), h*(0.12+0.5*rnd(seed, 9)))
		p.glassTrail(x0, y0, dist, v, t, r0, seed, fade)
		y := y0 + dist
		if y-r0 > float64(a.Max.Y) {
			continue
		}
		r := r0 * (1 - 0.3*clamp01(t/2))
		p.glassDrop(x0+p.wander(seed, dist), y, r, 0.5*clamp01(t/0.4), seed, fade)
	}
}

// wander is how far sideways a running drop has strayed by the time it has run dist: it follows the
// glass's own unevenness, the same path every time for the same drop.
func (p *paint) wander(seed int, dist float64) float64 {
	a, b := float64(p.s(40)), float64(p.s(14))
	return float64(p.s(5)) * (0.65*math.Sin(dist/a+rnd(seed, 20)*6.3) + 0.35*math.Sin(dist/b+rnd(seed, 21)*6.3) -
		0.65*math.Sin(rnd(seed, 20)*6.3) - 0.35*math.Sin(rnd(seed, 21)*6.3))
}

// glassTrail is the wet streak a drop of radius r leaves running dist down from (x0, y0) at about speed v,
// t seconds after it set off: drying from the top, where it has been longest, with beads left behind
// here and there.
func (p *paint) glassTrail(x0, y0, dist, v, t, r float64, seed int, alpha float64) {
	if dist <= 0 {
		return
	}
	step := math.Max(3, r*0.8)
	wet := math.Max(1, r*0.26)
	for d := 0.0; d < dist; d += step {
		age := math.Max(0, t-d/v)
		a := alpha * math.Exp(-age/1.8)
		if a < 0.03 {
			continue
		}
		xa, ya := x0+p.wander(seed, d), y0+d
		xb, yb := x0+p.wander(seed, d+step), y0+math.Min(d+step, dist)
		thin := 1 - 0.4*clamp01(age/4)
		p.fxLine(xa, ya, xb, yb, wet*2*thin, fxGlassShade, 0.1*a)
		p.fxLine(xa-wet*0.4, ya, xb-wet*0.4, yb, wet*0.6*thin, fxGlassLight, 0.16*a)
	}
	// A bead every so often, where the trail left some water behind.
	gap := float64(p.s(18))
	for k := 1; float64(k)*gap < dist-r*2; k++ {
		if rnd(seed, 30+k) < 0.75 {
			continue
		}
		d := float64(k)*gap + (rnd(seed, 50+k)-0.5)*gap*0.6
		a := alpha * math.Exp(-math.Max(0, t-d/v)/5)
		p.glassDrop(x0+p.wander(seed, d), y0+d, r*(0.22+0.15*rnd(seed, 70+k)), 0, seed*31+k, a)
	}
}

// The glass's light and shade: the sky's light off the water, and the dark of light bent away.
var (
	fxGlassLight = color.RGBA{0xf4, 0xf8, 0xff, 0xff}
	fxGlassShade = color.RGBA{0x10, 0x16, 0x20, 0xff}
)

// glassDrop is one drop of radius r centered at (cx, cy), drawn as a lens over what is already there.
// stretch, 0 to about 0.5, draws a running drop: longer, heavier at its foot and a little narrower. Its
// outline wobbles by its seed, so no two drops have the same shape.
func (p *paint) glassDrop(cx, cy, r, stretch float64, seed int, alpha float64) {
	if r < 0.8 || alpha <= 0 {
		return
	}
	if r < 2.5 {
		// Too small to show a lens: a speck of shade with the light caught on its upper side.
		p.fxDisc(cx, cy, r, fxGlassShade, 0.28*alpha)
		p.fxDisc(cx-0.3*r, cy-0.35*r, math.Max(0.5, 0.45*r), fxGlassLight, 0.6*alpha)
		return
	}
	reach := r * 1.25
	x0, x1 := int(cx-reach)-1, int(cx+reach)+2
	y0, y1 := int(cy-reach*(1+0.25*stretch))-1, int(cy+reach*(1+stretch))+2
	b := image.Rect(x0, y0, x1, y1).Intersect(p.dst.Rect)
	if b.Empty() {
		return
	}
	// What is behind the drop, before it is drawn over: the lens shows it.
	bw, bh := b.Dx(), b.Dy()
	if cap(p.glass) < 4*bw*bh {
		p.glass = make([]uint8, 4*bw*bh)
	}
	behind := p.glass[:4*bw*bh]
	for y := range bh {
		i := p.dst.PixOffset(b.Min.X, b.Min.Y+y)
		copy(behind[4*bw*y:4*bw*(y+1)], p.dst.Pix[i:i+4*bw])
	}
	// The outline's wobble, as two harmonics of the angle round the middle, worked from the angle's
	// sine and cosine so no trigonometry runs per pixel.
	s2, c2 := math.Sincos(rnd(seed, 11) * 6.3)
	s3, c3 := math.Sincos(rnd(seed, 12) * 6.3)
	a2, a3 := 0.05+0.07*rnd(seed, 14), 0.03+0.05*rnd(seed, 15)
	most := r*(1+a2+a3) + 0.5
	hx, hy := cx-0.3*r, cy-0.36*r
	hr := 0.15*r + 0.5
	lr, lg, lb := float64(fxGlassLight.R), float64(fxGlassLight.G), float64(fxGlassLight.B)
	sr, sg, sb := float64(fxGlassShade.R), float64(fxGlassShade.G), float64(fxGlassShade.B)
	up, down, narrow := 1/(1+0.25*stretch), 1/(1+stretch), 1+0.2*stretch
	for y := b.Min.Y; y < b.Max.Y; y++ {
		dy := float64(y) + 0.5 - cy
		ny := dy * up
		if dy > 0 {
			ny = dy * down
		}
		row := p.dst.PixOffset(b.Min.X, y)
		for x := b.Min.X; x < b.Max.X; x++ {
			dx := float64(x) + 0.5 - cx
			nx := dx * narrow
			d := math.Sqrt(nx*nx + ny*ny)
			if d > most {
				continue
			}
			sn, cs := 0.0, 1.0
			if d > 0 {
				sn, cs = ny/d, nx/d
			}
			sin2, cos2 := 2*sn*cs, cs*cs-sn*sn
			sin3, cos3 := sn*(3-4*sn*sn), cs*(4*cs*cs-3)
			edge := r * (1 + a2*(sin2*c2+cos2*s2) + a3*(sin3*c3+cos3*s3))
			cover := clamp01(edge-d+0.5) * alpha
			if cover <= 0 {
				continue
			}
			q := clamp01(d / edge)
			// The lens: the scene behind, turned over and made smaller toward the middle.
			k := 0.55 * (1 - 0.35*q*q)
			lx := min(max(int(cx-dx*k)-b.Min.X, 0), bw-1)
			ly := min(max(int(cy-dy*k)-b.Min.Y, 0), bh-1)
			src := behind[4*(ly*bw+lx):]
			cr := float64(src[0])*0.9 + lr*0.1
			cg := float64(src[1])*0.9 + lg*0.1
			cb := float64(src[2])*0.9 + lb*0.1
			// The rim: dark where the light is bent away, most at the top; light gathers at the foot.
			top := 0.5 - 0.5*sn
			if dark := smooth(0.6, 1, q) * (0.22 + 0.45*top); dark > 0 {
				cr, cg, cb = cr+(sr-cr)*dark, cg+(sg-cg)*dark, cb+(sb-cb)*dark
			}
			hdx, hdy := (float64(x)+0.5-hx)/hr, (float64(y)+0.5-hy)/hr
			light := 0.0
			if h2 := hdx*hdx + hdy*hdy; h2 < 4 { // past that the highlight is under a thousandth
				light = 0.85 * math.Exp(-2*h2)
			}
			if q > 0.4 && top < 0.5 {
				light += 0.4 * smooth(0.4, 0.82, q) * (1 - smooth(0.82, 1, q)) * (1 - top)
			}
			if light = clamp01(light); light > 0 {
				cr, cg, cb = cr+(lr-cr)*light, cg+(lg-cg)*light, cb+(lb-cb)*light
			}
			px := p.dst.Pix[row+4*(x-b.Min.X) : row+4*(x-b.Min.X)+4 : row+4*(x-b.Min.X)+4]
			cover = math.Min(cover, 1)
			px[0] = uint8(float64(px[0]) + (cr-float64(px[0]))*cover + 0.5)
			px[1] = uint8(float64(px[1]) + (cg-float64(px[1]))*cover + 0.5)
			px[2] = uint8(float64(px[2]) + (cb-float64(px[2]))*cover + 0.5)
		}
	}
}

// smooth is 0 below e0, 1 above e1, and an S between.
func smooth(e0, e1, v float64) float64 {
	t := clamp01((v - e0) / (e1 - e0))
	return t * t * (3 - 2*t)
}

// Lightning's colors: a channel near white, its glow and the sky it lights a cold blue-violet.
var (
	fxBoltColor = color.RGBA{0xfa, 0xfb, 0xff, 0xff}
	fxBoltGlow  = color.RGBA{0xb4, 0xc0, 0xff, 0xff}
	fxFlashSky  = color.RGBA{0xd6, 0xdc, 0xff, 0xff}
)

// strikeSlot is how the storm's time is cut up: any slot may hold a strike, never two slots running, so
// strikes come at no rhythm at all, a few seconds to half a minute apart and about eight on average.
const strikeSlot = 1.2

func strikes(slot int) bool {
	hit := func(s int) bool { return rnd(s, 101) < 0.2 }
	return hit(slot) && !hit(slot-1)
}

// lightning is a storm's strikes: the sky over a lit by each flicker, and most of the time a bolt down
// through b, branching, a new shape each strike. A strike is two to four strokes down the same channel
// in under half a second, each fading fast; now and then a strike stays in the cloud and only the sky
// lights.
func (p *paint) lightning(secs float64, a, b image.Rectangle) {
	slot := int(math.Floor(secs / strikeSlot))
	for s := slot - 1; s <= slot; s++ {
		if !strikes(s) {
			continue
		}
		t := secs - (float64(s)+rnd(s, 102)*0.6)*strikeSlot
		if t < 0 || t > 1.6 {
			continue
		}
		level := strokeLevel(s, t)
		if level < 0.02 {
			continue
		}
		cloud := rnd(s, 103) < 0.3
		p.flashSky(a, b, s, level, cloud)
		if !cloud && !b.Empty() {
			p.bolt(b, s, level)
		}
	}
}

// strokeLevel is how bright strike s is t seconds in: its strokes, each up at once and fading in a
// few hundredths of a second, the later ones weaker and at uneven gaps.
func strokeLevel(s int, t float64) float64 {
	n := 2 + int(rnd(s, 104)*3)
	at, level := 0.0, 0.0
	for k := range n {
		peak := 1.0
		if k > 0 {
			at += 0.05 + 0.14*rnd(s, 110+k)
			peak = 0.5 + 0.5*rnd(s, 120+k)
		}
		if t >= at {
			level = math.Max(level, peak*math.Exp(-(t-at)/0.07))
		}
	}
	return level
}

// flashSky lights the sky over a: brightest at the top and toward where the strike is, and for a strike
// that stays in the cloud, wider and stronger, as the whole cloud glows.
func (p *paint) flashSky(a, b image.Rectangle, s int, level float64, cloud bool) {
	a = a.Intersect(p.dst.Rect)
	if a.Empty() {
		return
	}
	strength, spread := 0.3, 0.8
	cx := float64(b.Min.X+b.Max.X) / 2
	if cloud || b.Empty() {
		strength, spread = 0.42, 0.5
		cx = float64(a.Min.X) + rnd(s, 105)*float64(a.Dx())
	}
	w, h := float64(a.Dx()), float64(a.Dy())
	col := make([]float64, a.Dx())
	for x := range col {
		e := (float64(a.Min.X+x) - cx) / (spread * w)
		col[x] = math.Exp(-e * e)
	}
	c := fxFlashSky
	for y := a.Min.Y; y < a.Max.Y; y++ {
		row := level * strength * (1 - 0.65*float64(y-a.Min.Y)/h)
		i := p.dst.PixOffset(a.Min.X, y)
		for x := range col {
			k := row * (0.35 + 0.65*col[x])
			px := p.dst.Pix[i : i+4 : i+4]
			px[0] = uint8(float64(px[0]) + (float64(c.R)-float64(px[0]))*k)
			px[1] = uint8(float64(px[1]) + (float64(c.G)-float64(px[1]))*k)
			px[2] = uint8(float64(px[2]) + (float64(c.B)-float64(px[2]))*k)
			i += 4
		}
	}
}

// boltRand is a strike's own run of numbers, so its shape holds through its strokes.
type boltRand uint32

func (r *boltRand) next() float64 {
	x := uint32(*r)
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	*r = boltRand(x)
	return float64(x) / 4294967296.0
}

// boltSeg is a stretch of the channel: its ends, its width and how bright it is next to the main one.
type boltSeg struct{ x0, y0, x1, y1, w, a float64 }

// bolt draws strike s down through b at level: a channel from the top of b to near its foot, jagged at
// every scale, with branches off it that thin and fade, and branches off some of those.
func (p *paint) bolt(b image.Rectangle, s int, level float64) {
	rng := boltRand(uint32(s)*2654435761 | 1)
	top, foot := float64(b.Min.Y), float64(b.Max.Y)-rng.next()*0.2*float64(b.Dy())
	x0 := float64(b.Min.X) + (0.2+0.6*rng.next())*float64(b.Dx())
	x1 := float64(b.Min.X) + (0.15+0.7*rng.next())*float64(b.Dx())
	main := float64(p.s(4))
	var segs []boltSeg
	branches := 0
	var channel func(x0, y0, x1, y1, w, a float64, levels int, branchy float64)
	channel = func(x0, y0, x1, y1, w, a float64, levels int, branchy float64) {
		pts := [][2]float64{{x0, y0}, {x1, y1}}
		rough := 0.42
		for range levels {
			next := make([][2]float64, 0, 2*len(pts))
			for i := 0; i+1 < len(pts); i++ {
				ax, ay, bx, by := pts[i][0], pts[i][1], pts[i+1][0], pts[i+1][1]
				l := math.Hypot(bx-ax, by-ay)
				off := (rng.next() - 0.5) * 2 * rough * l
				nx, ny := -(by-ay)/math.Max(l, 1e-6), (bx-ax)/math.Max(l, 1e-6)
				next = append(next, pts[i], [2]float64{(ax+bx)/2 + nx*off, (ay+by)/2 + ny*off})
			}
			pts = append(next, pts[len(pts)-1])
			rough *= 0.82
		}
		total := float64(len(pts) - 1)
		for i := 0; i+1 < len(pts); i++ {
			along := float64(i) / total
			segs = append(segs, boltSeg{pts[i][0], pts[i][1], pts[i+1][0], pts[i+1][1],
				w * (1 - 0.45*along), a * (1 - 0.55*along*branchy)})
			if branches < 12 && i > 2 && i+3 < len(pts) && rng.next() < 0.09*branchy+0.03 {
				branches++
				dirx, diry := pts[i+1][0]-pts[i][0], pts[i+1][1]-pts[i][1]
				l := math.Max(math.Hypot(dirx, diry), 1e-6)
				turn := (0.4 + 0.6*rng.next()) * math.Copysign(1, rng.next()-0.5)
				cs, sn := math.Cos(turn), math.Sin(turn)
				ux, uy := (dirx*cs-diry*sn)/l, (dirx*sn+diry*cs)/l
				if uy < 0.25 {
					uy = 0.25 // a branch heads down, as the channel does
				}
				reach := (foot - top) * (0.12 + 0.28*rng.next()) * math.Max(0.5, a)
				channel(pts[i][0], pts[i][1], pts[i][0]+ux*reach, pts[i][1]+uy*reach,
					w*0.45, a*0.6, max(levels-2, 3), branchy*0.6)
			}
		}
	}
	channel(x0, top, x1, foot, main, 1, 6, 1)
	// Kept inside b's width: a bolt wider than its room is drawn narrower, round its middle.
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, g := range segs {
		lo, hi = math.Min(lo, math.Min(g.x0, g.x1)), math.Max(hi, math.Max(g.x0, g.x1))
	}
	if room := float64(b.Dx()); hi-lo > room {
		mid, k := (lo+hi)/2, room/(hi-lo)
		cmid := float64(b.Min.X+b.Max.X) / 2
		for i := range segs {
			segs[i].x0 = cmid + (segs[i].x0-mid)*k
			segs[i].x1 = cmid + (segs[i].x1-mid)*k
		}
	}
	for _, g := range segs {
		if g.a > 0.5 {
			p.fxLine(g.x0, g.y0, g.x1, g.y1, g.w*7, fxBoltGlow, 0.08*level*g.a)
		}
		p.fxLine(g.x0, g.y0, g.x1, g.y1, g.w*2.8, fxBoltGlow, 0.3*level*g.a)
	}
	for _, g := range segs {
		p.fxLine(g.x0, g.y0, g.x1, g.y1, math.Max(1, g.w), fxBoltColor, math.Min(1, 1.15*level)*g.a)
	}
}
