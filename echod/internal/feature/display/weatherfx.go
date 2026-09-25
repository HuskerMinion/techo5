//go:build !dot

package display

import (
	"image"
	"image/color"
	"log/slog"
	"math"
	"strings"
	"sync/atomic"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The weather page's sky: rain falling while it rains, snow while it snows, a flash in a thunderstorm,
// fog drifting, all drawn over the page while it is up. Nothing is kept from frame to frame: where each
// drop is comes from the time and the drop's number, so there is nothing to run when the page is not
// showing and nothing to go out of step. Clear, sunny and cloudy skies stay still, so movement means
// something is falling.

// fxFrame is how often the page is drawn while its sky moves: smooth enough for rain, and a fraction of
// what the panel could do, since the page is up for half a minute at a time.
const fxFrame = 83 * time.Millisecond

// weatherAnimation is the setting, read by each frame without the config's lock (see clock24).
var weatherAnimation atomic.Bool

// skyFx is what a sky does.
type skyFx int

const (
	fxNone skyFx = iota
	fxRain
	fxPour
	fxSnow
	fxSleet
	fxHail
	fxStorm
	fxFog
	fxWind
)

// fxFor is the sky for one of Home Assistant's weather conditions.
func fxFor(cond string) skyFx {
	switch strings.ToLower(cond) {
	case "rainy":
		return fxRain
	case "pouring":
		return fxPour
	case "snowy":
		return fxSnow
	case "snowy-rainy":
		return fxSleet
	case "hail":
		return fxHail
	case "lightning", "lightning-rainy":
		return fxStorm
	case "fog":
		return fxFog
	case "windy", "windy-variant":
		return fxWind
	}
	return fxNone
}

// weatherNow is the condition the weather page shows: the reading, or today's forecast without one.
func weatherNow(w home.Weather, days forecastDays) string {
	if w.Condition != "" || len(days) == 0 {
		return w.Condition
	}
	return days[0].Condition
}

// skyNow is the sky to draw for a condition, or none while the setting is off.
func skyNow(cond string) skyFx {
	if !weatherAnimation.Load() {
		return fxNone
	}
	return fxFor(cond)
}

// Colors: rain a cool gray-blue, snow and hail near white, all drawn part-transparent so the page
// reads through them.
var (
	fxRainColor = color.RGBA{0xa8, 0xc4, 0xe0, 0xff}
	fxSnowColor = color.RGBA{0xf2, 0xf4, 0xf8, 0xff}
	fxFogColor  = color.RGBA{0xc8, 0xc4, 0xbe, 0xff}
)

// rnd is a fixed pseudo-random number in [0, 1) for particle i's property k: the same every frame, so
// a drop keeps its column and speed.
func rnd(i, k int) float64 {
	x := uint32(i)*374761393 + uint32(k)*668265263 + 0x9e3779b9 // uint32: the Shows are 32-bit
	x = (x ^ (x >> 13)) * 1274126177
	x ^= x >> 16
	return float64(x) / 4294967296.0
}

// wrap is v into [0, n).
func wrap(v, n float64) float64 {
	v = math.Mod(v, n)
	if v < 0 {
		v += n
	}
	return v
}

// sky draws fx over area at time t; a thunderstorm's lightning strikes within bolt. Sizes are the
// Show 5's, through p.s.
func (p *paint) sky(fx skyFx, t time.Time, area, bolt image.Rectangle) {
	secs := float64(t.UnixMilli()) / 1000 // a float64 keeps a tenth of a microsecond at this size
	switch fx {
	case fxRain:
		p.rain(secs, area, 55, 1.0, 0.32)
	case fxPour:
		p.rain(secs, area, 120, 1.35, 0.38)
	case fxSnow:
		p.snow(secs, area, 45, 0.85)
	case fxSleet:
		p.rain(secs, area, 35, 1.0, 0.3)
		p.snow(secs, area, 22, 0.7)
	case fxHail:
		p.hail(secs, area, 40)
	case fxStorm:
		p.rain(secs, area, 90, 1.25, 0.34)
		p.lightning(secs, bolt)
	case fxFog:
		p.fog(secs, area)
	case fxWind:
		p.wind(secs, area, 14)
	}
}

// rain is n falling streaks, a little slanted, at speed times the page's height per second.
func (p *paint) rain(secs float64, a image.Rectangle, n int, speed, alpha float64) {
	w, h := float64(a.Dx()), float64(a.Dy())
	length := float64(p.s(22))
	slant := length * 0.18
	for i := range n {
		v := h * speed * (0.75 + 0.5*rnd(i, 1))
		x := float64(a.Min.X) + rnd(i, 0)*w
		y := float64(a.Min.Y) + wrap(rnd(i, 2)*(h+length)+secs*v, h+length) - length
		c := fxRainColor
		p.fxLine(x+slant, y, x, y+length, math.Max(1, float64(p.s(2))*0.75), c, alpha*(0.6+0.4*rnd(i, 3)))
	}
}

// snow is n flakes drifting down, swaying from side to side and turning slowly. Each is a six-armed
// crystal; the larger ones have a pair of branches on every arm.
func (p *paint) snow(secs float64, a image.Rectangle, n int, alpha float64) {
	w, h := float64(a.Dx()), float64(a.Dy())
	for i := range n {
		r := float64(p.s(4)) + rnd(i, 3)*float64(p.s(5))
		v := h * (0.07 + 0.06*rnd(i, 1)) * (0.7 + 0.3*r/float64(p.s(9))) // the big ones fall a little faster
		x := float64(a.Min.X) + wrap(rnd(i, 0)*w+math.Sin(secs*(0.6+0.5*rnd(i, 4))+rnd(i, 5)*6.28)*float64(p.s(14)), w)
		y := float64(a.Min.Y) + wrap(rnd(i, 2)*(h+2*r)+secs*v, h+2*r) - r
		turn := rnd(i, 7)*math.Pi + secs*(rnd(i, 8)-0.5)*0.8
		p.fxFlake(x, y, r, turn, fxSnowColor, alpha*(0.6+0.4*rnd(i, 6)))
	}
}

// fxFlake is a snowflake of radius r at (cx, cy), turned by turn: three strokes crossing at sixty
// degrees make the six arms, and an arm long enough to show them gets two branches near its tip.
func (p *paint) fxFlake(cx, cy, r, turn float64, c color.RGBA, alpha float64) {
	stroke := math.Max(1, float64(p.s(2))*0.6)
	branched := r >= float64(p.s(6))
	for k := range 6 {
		ang := turn + float64(k)*math.Pi/3
		dx, dy := math.Cos(ang), math.Sin(ang)
		p.fxLine(cx, cy, cx+dx*r, cy+dy*r, stroke, c, alpha)
		if !branched {
			continue
		}
		// Two short branches from 60% of the way out, angled back toward the center.
		bx, by := cx+dx*r*0.6, cy+dy*r*0.6
		for _, side := range []float64{-1, 1} {
			ba := ang + side*math.Pi/4
			p.fxLine(bx, by, bx+math.Cos(ba)*r*0.35, by+math.Sin(ba)*r*0.35, stroke, c, alpha)
		}
	}
}

// hail is n small pellets falling fast.
func (p *paint) hail(secs float64, a image.Rectangle, n int) {
	w, h := float64(a.Dx()), float64(a.Dy())
	for i := range n {
		r := float64(p.s(2)) + rnd(i, 3)*float64(p.s(1))
		v := h * (0.9 + 0.4*rnd(i, 1))
		x := float64(a.Min.X) + rnd(i, 0)*w
		y := float64(a.Min.Y) + wrap(rnd(i, 2)*(h+2*r)+secs*v, h+2*r) - r
		p.fxDisc(x, y, r, fxSnowColor, 0.7)
	}
}

// fxBoltColor is lightning: near white with a little warmth, the page's cream at its brightest.
var fxBoltColor = color.RGBA{0xff, 0xf6, 0xd8, 0xff}

// lightning is a bolt striking down through b every nine seconds or so: it flickers twice and fades,
// never at the same moment of the nine twice in a row, and each strike has a shape of its own - a
// jagged path from the top of b to the bottom, and a short branch from partway down.
func (p *paint) lightning(secs float64, b image.Rectangle) {
	cycle := int(secs / 9)
	at := secs - float64(cycle)*9 - rnd(cycle, 7)*5 // somewhere in the first five seconds of each nine
	var level float64
	switch {
	case at >= 0 && at < 0.10:
		level = 1
	case at >= 0.10 && at < 0.18:
		level = 0.25
	case at >= 0.18 && at < 0.30:
		level = 0.85
	case at >= 0.30 && at < 0.60:
		level = 0.85 * (1 - (at-0.30)/0.30) // the fade after the second flicker
	}
	if level <= 0 || b.Empty() {
		return
	}
	// The path: segments down the height of b, each stepping sideways by up to a fifth of b's width.
	const segs = 9
	xs, ys := make([]float64, segs+1), make([]float64, segs+1)
	mid, step := float64(b.Min.X+b.Max.X)/2, float64(b.Dx())/3
	x := mid + (rnd(cycle, 11)-0.5)*step
	for k := range segs + 1 {
		xs[k] = math.Max(float64(b.Min.X), math.Min(float64(b.Max.X), x))
		ys[k] = float64(b.Min.Y) + float64(b.Dy())*float64(k)/segs
		x += (rnd(cycle, 20+k) - 0.5) * 2 * step
	}
	wide, glow, core := float64(p.s(26)), float64(p.s(12)), math.Max(2, float64(p.s(5)))
	stroke := func(x0, y0, x1, y1 float64) {
		p.fxLine(x0, y0, x1, y1, wide, fxBoltColor, 0.08*level)
		p.fxLine(x0, y0, x1, y1, glow, fxBoltColor, 0.22*level)
		p.fxLine(x0, y0, x1, y1, core, fxBoltColor, level)
	}
	for k := range segs {
		stroke(xs[k], ys[k], xs[k+1], ys[k+1])
	}
	// Two branches, one from each half of the way down, off to either side and thinning out.
	for n, k := range []int{2 + int(rnd(cycle, 40)*2), 5 + int(rnd(cycle, 43)*2)} {
		dir := 1.0
		if (rnd(cycle, 41) < 0.5) != (n == 1) {
			dir = -1
		}
		bx, by := xs[k], ys[k]
		ex, ey := bx+dir*step*1.4, by+float64(b.Dy())/segs*1.8
		mx, my := (bx+ex)/2+(rnd(cycle, 42+n)-0.5)*step*0.8, (by+ey)/2
		p.fxLine(bx, by, mx, my, glow*0.6, fxBoltColor, 0.15*level)
		p.fxLine(bx, by, mx, my, core*0.6, fxBoltColor, 0.8*level)
		p.fxLine(mx, my, ex, ey, core*0.4, fxBoltColor, 0.5*level)
	}
}

// fog is three soft bands drifting slowly across, each fading in and out.
func (p *paint) fog(secs float64, a image.Rectangle) {
	h := float64(a.Dy())
	ripple := make([]float64, a.Dx())
	for i := range 3 {
		// A slow ripple along the band, so it drifts rather than sits; worked out once a column.
		for x := range ripple {
			ripple[x] = 0.75 + 0.25*math.Sin(float64(a.Min.X+x)*0.012+secs*0.4+float64(i))
		}
		mid := float64(a.Min.Y) + h*(0.25+0.25*float64(i)) + math.Sin(secs*0.15+float64(i)*2)*float64(p.s(20))
		half := float64(p.s(40))
		strength := 0.10 + 0.06*math.Sin(secs*0.25+float64(i)*1.7)
		for y := int(mid - half); y < int(mid+half); y++ {
			d := math.Abs(float64(y)-mid) / half
			rowA := strength * (1 - d*d)
			if rowA <= 0.005 {
				continue
			}
			for x := a.Min.X; x < a.Max.X; x++ {
				p.blendAt(x, y, fxFogColor, rowA*ripple[x-a.Min.X])
			}
		}
	}
}

// wind is n faint streaks blowing across.
func (p *paint) wind(secs float64, a image.Rectangle, n int) {
	w, h := float64(a.Dx()), float64(a.Dy())
	length := float64(p.s(70))
	for i := range n {
		v := w * (0.5 + 0.4*rnd(i, 1))
		x := float64(a.Min.X) + wrap(rnd(i, 0)*(w+length)+secs*v, w+length) - length
		y := float64(a.Min.Y) + rnd(i, 2)*h
		p.fxLine(x, y, x+length, y-length*0.05, math.Max(1, float64(p.s(2))*0.6), fxFogColor, 0.18)
	}
}

// fxLine is aaLine at a given opacity.
func (p *paint) fxLine(x0, y0, x1, y1, w float64, c color.RGBA, alpha float64) {
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
			p.blendAt(x, y, c, clamp01(0.5-d)*alpha)
		}
	}
}

// fxDisc is a small soft-edged dot.
func (p *paint) fxDisc(cx, cy, r float64, c color.RGBA, alpha float64) {
	for y := int(cy - r - 1); y <= int(cy+r+1); y++ {
		for x := int(cx - r - 1); x <= int(cx+r+1); x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) - r
			p.blendAt(x, y, c, clamp01(0.5-d)*alpha)
		}
	}
}

// weatherAnimationSwitch is the Home Assistant switch for it; wake redraws the screen once it changes.
func weatherAnimationSwitch(wake func()) *esphome.Switch {
	s := &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "screen_weather_animation",
			Name:     "Weather animation",
			Icon:     "mdi:weather-pouring",
			Category: esphome.CategoryConfig,
		},
	}
	s.OnCommand = func(on bool) {
		setWeatherAnimationSaved(s, on)
		wake()
	}
	return s
}

// setWeatherAnimationSaved changes the setting and keeps it.
func setWeatherAnimationSaved(s *esphome.Switch, on bool) {
	if err := config.Set().Screen().WeatherStill(!on); err != nil {
		slog.Error("saving the weather animation setting failed", "err", err)
		return
	}
	setWeatherAnimation(s, on)
	slog.Info("screen: weather animation", "on", on)
}

func setWeatherAnimation(s *esphome.Switch, on bool) {
	weatherAnimation.Store(on)
	s.Set(on)
}
