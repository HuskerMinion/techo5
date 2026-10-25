//go:build !dot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"math/rand"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/lib/spectrum"
)

// The equalizer turn screen: a wall of LED segments moving with the voice being heard while it
// listens and with the answer while it speaks, a slow wave while it thinks, and the words beneath.
// Unlit segments stay faintly visible, as on a real panel, lit ones glow a little, and the bottom
// rows are reflected under the baseline.

const (
	eqBands = 32
	eqSegs  = 18

	// eqFrame is the frame time while the bars are moving: smooth enough to read as motion, and only
	// for the few seconds of a turn.
	eqFrame = 66 * time.Millisecond
)

// equalizerOn is whether turns are drawn as the equalizer, on a device that has it.
func equalizerOn() bool {
	return hasEqualizer && turnStyles[turnStyleIndex()].value != "" && !cueOn()
}

// waveOn is whether the equalizer is drawn as the wave rather than the bars.
func waveOn() bool { return turnStyles[turnStyleIndex()].value == "wave" }

// turnStyleSelect is the Home Assistant setting.
func turnStyleSelect(wake func()) *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_turn_style",
			Name:     "Turn screen",
			Icon:     "mdi:equalizer",
			Category: esphome.CategoryConfig,
		},
		Options: turnStyleOptions(),
	}
	s.OnCommand = func(v string) {
		for i, t := range turnStyles {
			if t.label == v {
				setTurnStyle(s, i)
				wake()
				return
			}
		}
	}
	return s
}

// eqView is what the equalizer draws: a bar and a peak per band, 0 to 1.
type eqView struct {
	level, peak []float64
	night       bool
	quiet       bool // every bar has fallen: nothing left to animate
	wave        bool // drawn as the wave rather than the bars
	// style is the turn screen chosen (turnStyles' value), and phase the turn's, for the pictures that
	// look different listening, thinking and answering (turn_visual.go).
	style, phase string
}

var (
	eqGreen  = color.RGBA{57, 211, 83, 255}
	eqYellow = color.RGBA{245, 213, 71, 255}
	eqRed    = color.RGBA{255, 77, 77, 255}
	eqDeep   = color.RGBA{110, 10, 10, 255}
	eqEmber  = color.RGBA{255, 60, 40, 255}
)

func mix(a, b color.RGBA, t float64) color.RGBA {
	t = min(max(t, 0), 1)
	f := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
	return color.RGBA{f(a.R, b.R), f(a.G, b.G), f(a.B, b.B), 255}
}

// eqColor is segment k's color: green rising through yellow to red, or dim reds at night.
func eqColor(k int, night bool) color.RGBA {
	t := float64(k) / (eqSegs - 1)
	if night {
		return mix(eqDeep, eqEmber, t)
	}
	if t < 0.65 {
		return mix(eqGreen, eqYellow, t/0.65)
	}
	return mix(eqYellow, eqRed, (t-0.65)/0.35)
}

// eqThinking is the wave while an answer is being worked out: low, slow, and the same for every
// frame at the same moment.
func eqThinking(now time.Time, level, peak []float64) {
	t := float64(now.UnixMilli()%100000) / 1000
	for i := range level {
		x := float64(i) / float64(len(level))
		s := math.Sin(2*math.Pi*(1.5*x-0.25*t) + 0.8)
		level[i] = 0.16 + 0.12*s*s
		peak[i] = level[i] + 0.05
	}
}

// eqFor is the view for a turn's phase at now.
func eqFor(phase string, night bool, now time.Time) *eqView {
	v := &eqView{night: night, style: turnStyles[turnStyleIndex()].value, phase: phase}
	switch phase {
	case "listening":
		v.level, v.peak = spectrum.Mic.Bands(eqBands, now)
		v.quiet = spectrum.Mic.Quiet()
	case "thinking":
		v.level, v.peak = make([]float64, eqBands), make([]float64, eqBands)
		eqThinking(now, v.level, v.peak)
	default: // replying, and the answer lingering after it while the bars fall
		v.level, v.peak = spectrum.Speaker.Bands(eqBands, now)
		v.quiet = spectrum.Speaker.Quiet()
	}
	return v
}

// drawBars draws the bars into band on dst, over ground: a wall of segments, unlit ones faintly
// visible, lit ones with a glow halo pixels wide, and the bottom rows reflected drop pixels under the
// band.
func drawBars(dst *image.RGBA, band image.Rectangle, v *eqView, ground color.RGBA, halo, drop int) {
	x0, x1, top, bot := band.Min.X, band.Max.X, band.Min.Y, band.Max.Y
	cw := float64(x1-x0) / eqBands
	gap := max(2, int(cw*0.22))
	sh := float64(bot-top) / eqSegs
	sg := max(2, int(sh*0.28))

	seg := func(c, k int) image.Rectangle {
		X0 := x0 + int(float64(c)*cw) + gap/2
		X1 := x0 + int(float64(c+1)*cw) - gap/2
		Y1 := bot - int(float64(k)*sh)
		Y0 := Y1 - int(sh) + sg
		return image.Rect(X0, Y0, X1, Y1)
	}
	fill := func(rc image.Rectangle, c color.RGBA) {
		draw.Draw(dst, rc, image.NewUniform(c), image.Point{}, draw.Src)
	}

	for c := range eqBands {
		lit := int(math.Round(v.level[c] * eqSegs))
		pk := int(math.Round(v.peak[c] * eqSegs))
		for k := range eqSegs {
			col := eqColor(k, v.night)
			rc := seg(c, k)
			switch {
			case k < lit:
				fill(rc.Inset(-halo), mix(ground, col, 0.28))
				fill(rc, col)
			case k == pk-1 && pk > lit:
				fill(rc.Inset(-halo), mix(ground, col, 0.22))
				fill(rc, mix(col, color.RGBA{255, 255, 255, 255}, 0.35))
			default:
				fill(rc, mix(ground, col, 0.10))
			}
		}
		// The reflection: the bottom rows mirrored under the baseline, fading out.
		for k, f := range []float64{0.20, 0.08} {
			col := eqColor(k, v.night)
			if k >= lit {
				f /= 3
			}
			rc := seg(c, 0).Add(image.Pt(0, int(float64(k+1)*sh)+drop))
			fill(rc, mix(ground, col, f))
		}
	}
}

// The wave turn screen: some twenty thin lines woven together, their height following the voice
// being heard or the answer being spoken, adding up where they cross so the weave glows brightest
// where it is busiest, with a white core through the middle and a few sparkles. The colors are a
// slice of a loop (amber, pink, violet, blue, cyan, green) that slides along while it talks, so it is
// colorful over time without ever being a rainbow at once.
//
// It is drawn at half the screen's resolution into a float buffer the size of the band it occupies,
// the glow is a blur of that buffer at a quarter of that size, and both are added over the ground in
// one pass that doubles each pixel onto the screen. Full resolution cost 143 ms a frame on a Show 5,
// where a frame has 66; the glow hides the difference.

// waveMix is the loop the colors are taken from, and waveNight the dim reds of night hours.
var (
	waveMix   = []color.RGBA{{255, 150, 40, 255}, {255, 50, 140, 255}, {170, 60, 255, 255}, {40, 150, 255, 255}, {0, 230, 200, 255}, {90, 255, 120, 255}}
	waveNight = []color.RGBA{{110, 8, 8, 255}, {190, 25, 20, 255}, {255, 60, 35, 255}, {150, 15, 50, 255}}
)

// waveLoop is how long the colors take to slide once around the loop, in seconds.
const waveLoop = 30.0

// waveFrame is the wave's frame time: ten a second, which slow-moving lines need no more than, and
// which a Show 5 draws in about half of (48 ms, measured).
const waveFrame = 100 * time.Millisecond

// waveLine is one line of the weave: two sines, how fast they travel, how far from the middle the
// line swings, and how bright it is.
type waveLine struct {
	f1, f2, ph, speed, spread float64
	bright                    float32
}

var waveLines = func() []waveLine {
	rnd := rand.New(rand.NewSource(3))
	out := make([]waveLine, 22)
	for i := range out {
		out[i] = waveLine{
			f1: 2.2 + 1.6*rnd.Float64(), f2: 5 + 3*rnd.Float64(), ph: 2 * math.Pi * rnd.Float64(),
			speed: 0.6 + 0.9*rnd.Float64(), spread: 0.35 + 0.65*rnd.Float64(), bright: 0.35,
		}
		if i%4 == 0 {
			out[i].bright = 0.7
		}
	}
	return out
}()

// waveSparks are the specks drifting around the weave: where each starts across the width, how fast
// it drifts, and how far from the middle it sits as a share of the weave's height there.
var waveSparks = func() [][3]float64 {
	rnd := rand.New(rand.NewSource(11))
	out := make([][3]float64, 120)
	for i := range out {
		out[i] = [3]float64{rnd.Float64(), 0.01 + 0.03*rnd.Float64(), 0.9 * rnd.NormFloat64()}
	}
	return out
}()

// sinTable is one turn of a sine in 4096 steps, for the tens of thousands of sines a frame needs.
var sinTable = func() []float32 {
	t := make([]float32, 4097)
	for i := range t {
		t[i] = float32(math.Sin(2 * math.Pi * float64(i) / 4096))
	}
	return t
}()

// fastSin is sin(a) from the table, interpolated: close enough to draw with, and far cheaper.
func fastSin(a float64) float32 {
	p := a / (2 * math.Pi) * 4096
	p -= math.Floor(p/4096) * 4096
	i := int(p)
	f := float32(p - float64(i))
	return sinTable[i] + (sinTable[i+1]-sinTable[i])*f
}

// waveBuf is the drawing's working memory, kept between frames.
type waveBuf struct {
	w, h   int
	acc    []float32 // the lines, RGB per pixel
	lw, lh int
	lo     []float32 // the near glow, a quarter of the size
	lo2    []float32 // the wide glow
	tmp    []float32
	col    []float32 // the color at each x, RGB
	amp    []float32 // how far the weave swings at each x, in pixels
	fade   []float32 // how much of the weave shows at each x, 1 but near a faded end
	lx     []int32   // for each x, the glow column left of it
	wx     []float32 // and how far toward the next one it sits
	used   []bool    // rows the lines touched
	glow   []float32 // one row of glow, interpolated between two rows of lo
}

func (b *waveBuf) size(w, h int) {
	if b.w == w && b.h == h {
		clear(b.acc)
		clear(b.used)
		return
	}
	b.w, b.h = w, h
	b.lw, b.lh = (w+3)/4, (h+3)/4
	b.acc = make([]float32, w*h*3)
	b.lo = make([]float32, b.lw*b.lh*3)
	b.lo2 = make([]float32, b.lw*b.lh*3)
	b.tmp = make([]float32, b.lw*b.lh*3)
	b.col = make([]float32, w*3)
	b.amp = make([]float32, w)
	b.fade = make([]float32, w)
	b.lx = make([]int32, w)
	b.wx = make([]float32, w)
	b.used = make([]bool, h)
	b.glow = make([]float32, b.lw*3)
	for x := range w {
		fx := (float32(x)+0.5)/4 - 0.5
		lx := min(max(int(fx), 0), b.lw-2)
		b.lx[x] = int32(lx)
		b.wx[x] = min(max(fx-float32(lx), 0), 1)
	}
}

// add puts c times v into the pixel at x, y, when it is inside.
func (b *waveBuf) add(x, y int, c []float32, v float32) {
	if y < 0 || y >= b.h || v <= 0 {
		return
	}
	b.used[y] = true
	i := (y*b.w + x) * 3
	b.acc[i] += c[0] * v
	b.acc[i+1] += c[1] * v
	b.acc[i+2] += c[2] * v
}

// plot adds a line's point at x, height y, spread over the rows either side so it reads smooth.
func (b *waveBuf) plot(x int, y float64, c []float32, v float32) {
	fy := math.Floor(y)
	f := float32(y - fy)
	iy := int(fy)
	b.add(x, iy, c, v*(1-f))
	b.add(x, iy+1, c, v*f)
	b.add(x, iy-1, c, v*0.15)
	b.add(x, iy+2, c, v*0.15)
}

// boxBlur blurs src (w by h, RGB) in place with radius r, three passes each way, through tmp.
func boxBlur(src, tmp []float32, w, h, r int) {
	for range 3 {
		blurPass(src, tmp, w, h, r, 1, w)
		blurPass(tmp, src, h, w, r, w, 1)
	}
}

// blurPass averages runs of 2r+1 pixels along one direction: count lines of n pixels, step apart
// along a line and next apart between lines.
func blurPass(src, dst []float32, n, count, r, step, next int) {
	norm := 1 / float32(2*r+1)
	for l := range count {
		base := l * next
		var s [3]float32
		for k := -r; k <= r; k++ {
			i := (base + min(max(k, 0), n-1)*step) * 3
			s[0], s[1], s[2] = s[0]+src[i], s[1]+src[i+1], s[2]+src[i+2]
		}
		for p := range n {
			o := (base + p*step) * 3
			dst[o], dst[o+1], dst[o+2] = s[0]*norm, s[1]*norm, s[2]*norm
			out := (base + max(p-r, 0)*step) * 3
			in := (base + min(p+r+1, n-1)*step) * 3
			s[0] += src[in] - src[out]
			s[1] += src[in+1] - src[out+1]
			s[2] += src[in+2] - src[out+2]
		}
	}
}

// waveColors fills b.col with the colors across the width at time t: a slice of the loop.
func (b *waveBuf) waveColors(t float64, night bool) {
	stops, start := waveMix, math.Mod(t/waveLoop, 1)*float64(len(waveMix))
	if night {
		stops, start = waveNight, 0
	}
	n := len(stops)
	for x := range b.w {
		p := math.Mod(start+float64(x)/float64(b.w-1)*2.7, float64(n))
		i := int(p)
		c := mix(stops[i], stops[(i+1)%n], p-float64(i))
		b.col[x*3], b.col[x*3+1], b.col[x*3+2] = float32(c.R)/255, float32(c.G)/255, float32(c.B)/255
	}
}

// drawWave draws the wave into band on dst, over ground, at now: the weave swings through the band's
// height and its glow spills pad beyond it, above and below. wide is the wide glow's blur radius, in
// the glow's quarter-size pixels. fade is the share of the width at each end over which the weave fades
// out, for a band that stops short of the screen's edges; 0 runs it to the edges. b is the working
// memory, kept between frames.
func drawWave(dst *image.RGBA, b *waveBuf, band image.Rectangle, pad int, v *eqView, ground color.RGBA, now time.Time, wide int, fade float64) {
	top, bot := band.Min.Y, band.Max.Y
	b.size(band.Dx()/2, (bot-top+2*pad)/2)
	mid := float64(b.h) / 2
	full := float64(bot-top) / 4 * 0.92
	t := float64(now.UnixMilli()%(1<<40)) / 1000
	b.waveColors(t, v.night)

	// How far the weave swings at each x: the bands spread across the width, tapered at both ends.
	n := len(v.level)
	for x := range b.w {
		u := float64(x) / float64(b.w-1)
		p := u * float64(n-1)
		i := min(int(p), n-2)
		e := v.level[i] + (v.level[i+1]-v.level[i])*(p-float64(i))
		taper := math.Pow(math.Sin(math.Pi*u), 0.7)
		b.amp[x] = float32(full * taper * (0.07 + 0.93*e))
		b.fade[x] = 1
		if fade > 0 {
			f := min(1, u/fade, (1-u)/fade)
			b.fade[x] = float32(f * f * (3 - 2*f))
		}
	}

	for _, l := range waveLines {
		for x := range b.w {
			a := float64(b.amp[x]) * l.spread
			u := 2 * math.Pi * float64(x) / float64(b.w)
			y := mid + a*float64(0.75*fastSin(l.f1*u+l.ph+l.speed*t)+0.25*fastSin(l.f2*u+1.7*l.ph+1.3*l.speed*t))
			b.plot(x, y, b.col[x*3:x*3+3], l.bright*b.fade[x])
		}
	}
	// The core: a brighter, near-white thread through the middle of the weave.
	var core [3]float32
	for x := range b.w {
		u := 2 * math.Pi * float64(x) / float64(b.w)
		y := mid + float64(b.amp[x])*0.12*float64(fastSin(3*u+1.2+0.8*t))
		k := b.col[x*3 : x*3+3]
		core = [3]float32{0.35*k[0] + 0.65, 0.35*k[1] + 0.65, 0.35*k[2] + 0.65}
		strength := 1.2 * b.amp[x] / float32(max(full, 1))
		b.plot(x, y, core[:], 1.2*strength*b.fade[x])
	}
	// The sparks drift slowly along and twinkle.
	for i, sp := range waveSparks {
		x := int(math.Mod(sp[0]+sp[1]*t, 1) * float64(b.w-1))
		y := mid + sp[2]*float64(b.amp[x])
		tw := float32(0.6 + 0.5*math.Sin(t*2+float64(i)))
		b.add(x, int(y), b.col[x*3:x*3+3], tw*b.fade[x])
	}

	// The glow: the lines shrunk to a quarter, blurred near and wide.
	clear(b.lo)
	for y := range b.h {
		if !b.used[y] {
			continue
		}
		acc, lo := b.acc[y*b.w*3:(y+1)*b.w*3], b.lo[(y/4)*b.lw*3:]
		for x := 0; x < b.w; x++ {
			o := (x >> 2) * 3
			lo[o] += acc[x*3]
			lo[o+1] += acc[x*3+1]
			lo[o+2] += acc[x*3+2]
		}
	}
	copy(b.lo2, b.lo)
	boxBlur(b.lo, b.tmp, b.lw, b.lh, 1)
	boxBlur(b.lo2, b.tmp, b.lw, b.lh, wide)
	// The sums above are of 16 pixels each: the average is folded into the glows' strengths.
	for i := range b.lo {
		b.lo[i] = (1.3*b.lo[i] + 0.9*b.lo2[i]) / 16
	}

	// Everything added over the ground, into the band's rows of the canvas.
	g := [3]float32{float32(ground.R) / 255, float32(ground.G) / 255, float32(ground.B) / 255}
	y0 := top - pad
	for y := range b.h {
		cy := y0 + 2*y
		if cy < 0 || cy+1 >= dst.Rect.Dy() {
			continue
		}
		fy := (float32(y)+0.5)/4 - 0.5
		ly := min(max(int(fy), 0), b.lh-2)
		wy := min(max(fy-float32(ly), 0), 1)
		up, down := b.lo[ly*b.lw*3:(ly+1)*b.lw*3], b.lo[(ly+1)*b.lw*3:(ly+2)*b.lw*3]
		var most float32
		for i := range b.glow {
			v := up[i] + (down[i]-up[i])*wy
			b.glow[i] = v
			most = max(most, v)
		}
		// A row with no line and no glow to speak of is the ground, which is already there.
		if !b.used[y] && most < 0.5/255 {
			continue
		}
		acc := b.acc[y*b.w*3:]
		row, next := dst.Pix[cy*dst.Stride+band.Min.X*4:], dst.Pix[(cy+1)*dst.Stride+band.Min.X*4:]
		for x := range b.w {
			l, wx := int(b.lx[x])*3, b.wx[x]
			gl := b.glow[l : l+6]
			o := x * 8
			for c := range 3 {
				val := g[c] + acc[x*3+c] + gl[c] + (gl[3+c]-gl[c])*wx
				u := uint8(min(max(val, 0), 1)*255 + 0.5)
				row[o+c], row[o+4+c], next[o+c], next[o+4+c] = u, u, u, u
			}
			row[o+3], row[o+7], next[o+3], next[o+7] = 255, 255, 255, 255
		}
	}

}
