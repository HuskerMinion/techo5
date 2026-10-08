//go:build !dot

package display

import (
	"image"
	"image/color"
	"math"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/moon"
)

// The weather art's moon: where it really is over home, along an arc from moonrise on one side of the
// sky to moonset on the other, and lit as it really is, the dark part a faint earthshine so the whole
// disc reads. Bright at night; by day, when it is up, the pale moon a blue sky shows. It moves, so it
// is laid over the landscape each time the art is composed, behind the mountains and under the clouds,
// rather than painted into the landscape, which is drawn once for hours.

// artMoon is the moon as the weather art draws it at a moment.
type artMoon struct {
	phase  moon.Phase
	up     bool    // above the horizon at home
	along  float64 // through its pass, 0 at moonrise, 1 at moonset
	south  bool    // home is south of the equator: the moon crosses right to left, and upside down
	placed bool    // from home's place; without it, the phase alone, drawn at night where it always was
}

// moonKept is the moon last worked out, for the minute it is for.
var moonKept struct {
	mu       sync.Mutex
	minute   int64
	lat, lon float64
	placed   bool
	m        artMoon
}

// moonNow is the moon over home at now, worked out once a minute: it takes a few minutes to move a
// pixel across the art, and finding its pass is a few hundred steps of the moon's place.
func moonNow(now time.Time) artMoon {
	lat, lon, ok := home.Get().Place(now)
	k := &moonKept
	k.mu.Lock()
	defer k.mu.Unlock()
	minute := now.Unix() / 60
	if k.minute != minute || k.lat != lat || k.lon != lon || k.placed != ok {
		k.minute, k.lat, k.lon, k.placed = minute, lat, lon, ok
		k.m = moonAt(lat, lon, ok, now)
	}
	return k.m
}

// moonAt is the moon at lat, lon at now; with placed false, home's place is not known yet, and only
// the phase is.
func moonAt(lat, lon float64, placed bool, now time.Time) artMoon {
	m := artMoon{phase: moon.PhaseAt(now), placed: placed, south: lat < 0}
	if placed {
		s := moon.At(lat, lon, now)
		m.up, m.along = s.Up, s.Along(now)
	}
	return m
}

// skyPath is where the sun or the moon is in k's sky when it is along through its pass, 0 at rising
// and 1 at setting: its center on the horizon at either end, where the mountains hide it until it has
// climbed, and highest halfway. It rises on the left, in the east of a sky seen facing south, and on
// the right where home is south of the equator and the sky is seen facing north. The round Spot shows
// only the circle in its square, so the arc there keeps clear of the corners.
func skyPath(k artKey, along float64, south bool) (x, y float64) {
	w, h := float64(k.w), float64(k.h)
	side, top := 0.07, 0.12
	if k.w == k.h {
		side, top = 0.18, 0.2
	}
	p := along
	if south {
		p = 1 - p
	}
	base := h * artHorizon
	return w * (side + (1-2*side)*p), base - (base-h*top)*math.Sin(math.Pi*along)
}

// moonLight is how the moon shows at a part of the day: its lit part's color and how much of it shows,
// how much the dark part shows, and its glow.
type moonLight struct {
	lit        color.RGBA
	litA, dkA  float64
	glowA      float64
	earthshine color.RGBA // what the dark part is lightened toward
}

var moonLights = map[artTime]moonLight{
	artNight: {lit: color.RGBA{250, 246, 228, 255}, litA: 1, dkA: 1, glowA: 0.22, earthshine: color.RGBA{96, 106, 136, 255}},
	artDawn:  {lit: color.RGBA{250, 240, 236, 255}, litA: 0.62, dkA: 0.08, earthshine: color.RGBA{150, 140, 160, 255}},
	artDusk:  {lit: color.RGBA{250, 236, 226, 255}, litA: 0.62, dkA: 0.08, earthshine: color.RGBA{150, 130, 150, 255}},
	artDay:   {lit: color.RGBA{246, 249, 255, 255}, litA: 0.4},
}

// The moon's seas as seen from the northern hemisphere, on a disc of radius 1 (x right, y down): the
// dark patches that make it look like itself, from Oceanus Procellarum on the left to Crisium near the
// right edge.
var moonSeas = [][3]float64{
	{-0.5, -0.05, 0.42}, {-0.28, -0.42, 0.34}, {0.14, -0.36, 0.24}, {0.28, -0.04, 0.26},
	{0.62, -0.24, 0.15}, {0.5, 0.2, 0.17}, {-0.12, 0.38, 0.2}, {-0.42, 0.45, 0.16},
}

// moonSpriteKey is what a drawn moon depends on beyond its landscape: its phase, which way it is seen,
// and its height in a few pixels' steps, for the sky color its dark part is mixed from. The phase
// moves a step in a quarter of an hour or so, so the moon is drawn again a few times an hour, not
// each second.
type moonSpriteKey struct {
	lit           int // the lit fraction, in 400ths
	waxing, south bool
	y             int
}

// drawMoon lays the moon into frame where m has it, behind land's mountains. Nothing when it is down,
// or the sky is overcast; through fog it is dimmed, as the sun is.
func drawMoon(frame *image.RGBA, land *artLand, m artMoon) {
	k := land.key
	if k.overcast() && k.cond != artFog {
		return
	}
	w, h := float64(k.w), float64(k.h)
	var cx, cy float64
	switch {
	case m.placed && m.up:
		cx, cy = skyPath(k, m.along, m.south)
	case !m.placed && k.when == artNight:
		cx, cy = w*0.8, h*0.3
	default:
		return
	}
	key := moonSpriteKey{int(math.Round(m.phase.Fraction * 400)), m.phase.Waxing, m.south, int(cy) / 4}
	if land.moon == nil || land.moonKey != key {
		land.moon, land.moonKey = moonSprite(k, m, float64(key.y*4)), key
	}
	blendSprite(frame, land.skyline, land.moon, int(math.Round(cx)), int(math.Round(cy)))
}

// moonSprite is the moon in phase m for k's sky at height cy, with its glow, centered on (0, 0).
func moonSprite(k artKey, m artMoon, cy float64) *image.NRGBA {
	w := float64(k.w)
	r := math.Max(w, float64(k.h)) * 0.038
	if k.w == k.h {
		r = w * 0.06 // the Spot's panel is small; the moon would be a dot
	}
	l := moonLights[k.when]
	sky := k.skyAt(cy)
	dark := lerp(sky, l.earthshine, 0.2)
	if k.cond == artFog {
		l.lit = lerp(l.lit, sky, 0.3)
		l.litA, l.dkA, l.glowA = l.litA*0.75, l.dkA*0.5, l.glowA*0.6
	}
	f := m.phase.Fraction
	glowR := r * 3.2
	glow := l.glowA * (0.25 + 0.75*f) // a crescent lights the sky around it less than a full moon

	// Which side is lit: the right while waxing, seen from the north; the other way round from the
	// south, where the moon is seen upside down, its seas too.
	dir := 1.0
	if m.phase.Waxing == m.south {
		dir = -1
	}
	face := 1.0
	if m.south {
		face = -1
	}
	reach := int(r) + 2
	if glow > 0 {
		reach = int(glowR) + 1
	}
	spr := image.NewNRGBA(image.Rect(-reach, -reach, reach+1, reach+1))
	for py := -reach; py <= reach; py++ {
		for px := -reach; px <= reach; px++ {
			u, v := float64(px)/r, float64(py)/r
			rho := math.Hypot(u, v)
			var ga float64
			if glow > 0 {
				g := clamp01(1 - rho*r/glowR)
				ga = glow * g * g
			}
			c, da := l.lit, 0.0
			if disc := clamp01((1-rho)*r + 0.5); disc > 0 {
				// The terminator is half an ellipse: across each row of the disc, the lit part starts
				// at (1-2f) of the row's half width, from the unlit side.
				half := math.Sqrt(math.Max(0, 1-v*v))
				lit := clamp01((u*dir-(1-2*f)*half)*r + 0.5)
				shade := 0.86 + 0.14*math.Sqrt(math.Max(0, 1-rho*rho)) // a little darker toward the edge
				seas := 0.0
				for _, s := range moonSeas {
					d := math.Hypot(u*face-s[0], v*face-s[1]) / s[2]
					if d < 1 {
						e := 1 - d*d
						seas += e * e // soft all the way out: hard edges read as craters
					}
				}
				shade *= 1 - 0.12*math.Min(seas, 1)
				bright := color.RGBA{uint8(float64(l.lit.R) * shade), uint8(float64(l.lit.G) * shade), uint8(float64(l.lit.B) * shade), 255}
				// The lit and the dark part each with its own strength, as one color.
				if a := l.litA*lit + l.dkA*(1-lit); a > 0 {
					c, da = lerp(dark, bright, l.litA*lit/a), a*disc
				}
			}
			// The disc over its glow.
			a := da + ga*(1-da)
			if a <= 0 {
				continue
			}
			c = lerp(l.lit, c, da/a)
			spr.SetNRGBA(px, py, color.NRGBA{c.R, c.G, c.B, uint8(math.Round(a * 255))})
		}
	}
	return spr
}

// blendSprite lays spr into img with its (0, 0) at x, y, where it is above skyline: behind the land.
func blendSprite(img *image.RGBA, skyline []float64, spr *image.NRGBA, x, y int) {
	r := spr.Rect.Add(image.Pt(x, y)).Intersect(img.Rect)
	for py := r.Min.Y; py < r.Max.Y; py++ {
		si := spr.PixOffset(r.Min.X-x, py-y)
		di := img.PixOffset(r.Min.X, py)
		for px := r.Min.X; px < r.Max.X; px, si, di = px+1, si+4, di+4 {
			a := uint32(spr.Pix[si+3])
			if a == 0 {
				continue
			}
			// The skyline is where the land starts in this column; a pixel it cuts shows in part.
			if behind := skyline[px] - float64(py); behind < 1 {
				if behind <= 0 {
					continue
				}
				a = uint32(float64(a) * behind)
			}
			d := img.Pix[di : di+3 : di+3]
			d[0] = uint8((uint32(d[0])*(255-a) + uint32(spr.Pix[si])*a) / 255)
			d[1] = uint8((uint32(d[1])*(255-a) + uint32(spr.Pix[si+1])*a) / 255)
			d[2] = uint8((uint32(d[2])*(255-a) + uint32(spr.Pix[si+2])*a) / 255)
		}
	}
}
