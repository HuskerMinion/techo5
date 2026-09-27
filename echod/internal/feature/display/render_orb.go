//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The orb is what a Show draws while a turn is on, in place of the "Listening…" bar: a holographic
// HUD of thin rings, segmented arcs and a tick scale turning at their own speeds round a white-hot
// core, and the core answers the voice. While somebody speaks it swells with the room's level, while
// the reply is worked out the arcs speed up and a sweep goes round, and while the reply plays a ring of
// bars stands up with what the speaker is sending.
//
// Every frame is drawn from scratch, twenty a second while a turn is on, on a core that is also
// hearing the wake word, so everything here is chosen for its cost. A ring's pixels, their coverage
// and their angle are worked out once per size and replayed after that: a frame is a walk down a list
// with integer blending, no square roots or arctangents. The glow and the core, the soft shapes, are
// pictures made once per size and copied.

// orbFrame is how often the screen is redrawn while the orb is up.
const orbFrame = 50 * time.Millisecond

// orbPalette is the orb's tones: the rings, their bright parts, the core, and a warm accent for the
// readouts.
type orbPalette struct{ accent, light, core, warm color.RGBA }

var (
	// orbGround is the page under the orb: Ocean's navy (lib/palette), which the blues need; a warm
	// ground turns them muddy. On the Ocean theme the orb and the clock share one ground.
	orbGround = color.RGBA{0x0a, 0x16, 0x22, 0xff}
	orbNight  = orbPalette{color.RGBA{0xb0, 0x24, 0x1c, 0xff}, color.RGBA{0xe0, 0x50, 0x46, 0xff}, color.RGBA{0xff, 0x9a, 0x90, 0xff}, color.RGBA{0xb0, 0x5a, 0x20, 0xff}}
)

// orbDayPalette is the orb in the theme's accent, its bright parts and core lighter toward white, so
// the orb is the same color as everything else on the screen: cyan on Ocean, amber on Walnut. The
// readouts stay warm whatever the theme. Night has its own palette, orbNight.
func orbDayPalette() orbPalette {
	white := color.RGBA{0xff, 0xff, 0xff, 0xff}
	return orbPalette{amber, lerp(amber, white, 0.45), lerp(amber, white, 0.85), color.RGBA{0xff, 0xb3, 0x47, 0xff}}
}

// orbPhase reports whether a phase draws the orb.
func orbPhase(phase string) bool {
	return phase == "listening" || phase == "thinking" || phase == "replying" || phase == "lingering"
}

// orb draws the whole turn page: the orb, its readouts, and the words beside it.
func (r *renderer) orb(s scene) {
	pal := orbDayPalette()
	if inNight(config.Get().Screen.Night, s.now) {
		pal = orbNight
	}
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(orbGround), image.Point{}, draw.Src)
	t := s.now.Sub(s.since).Seconds()
	if s.phase == "lingering" {
		t = 0 // redrawn once a second now: a still orb rather than one that jumps
	}

	rad := math.Round(math.Min(float64(r.h)*0.345, float64(r.w)*0.25))
	cx, cy := float64(r.w)/2, float64(r.h)/2-float64(r.s(8))
	if s.phase != "listening" {
		// The words take the left, the orb the right.
		cx = float64(r.w) - float64(r.margin) - rad*1.12
	}
	cx, cy = math.Round(cx), math.Round(cy)

	level := 0.0
	switch s.phase {
	case "listening":
		level = s.micLevel
	case "replying":
		// Speech peaks well under full scale at a comfortable volume; the square root lifts the
		// quiet syllables so the pulse follows the words rather than only their loudest parts.
		level = math.Sqrt(clamp01(s.outLevel * 1.6))
	}

	r.orbDraw(int(cx), int(cy), rad, t, s.phase, level, pal)

	// Readouts, the HUD's small print: the phase under the orb, a level to one side.
	label := map[string]string{"listening": "LISTENING", "thinking": "PROCESSING", "replying": "SPEAKING"}[s.phase]
	if label != "" {
		r.spaced(label, cx, cy+rad+float64(r.s(48)), pal.light)
	}
	if s.phase == "listening" || s.phase == "replying" {
		read := fmt.Sprintf("VOX %03d", int(level*100))
		x, y := int(cx-rad*1.0)-r.width(r.tiny, read)/2, int(cy-rad*0.86)
		if s.phase != "listening" {
			// The words have the left: under the corner clock instead, clear of the orb's rim.
			x, y = r.w-r.margin-r.width(r.tiny, read), r.margin+r.s(64)
		}
		r.text(r.tiny, read, x, y, pal.warm)
	}

	r.cornerClock(s)
	if s.phase != "listening" {
		maxW := int(cx-rad*1.2) - r.margin
		if s.phase == "thinking" {
			r.wordsW(s.heard, "", 200, maxW)
		} else {
			r.wordsW(s.heard, s.reply, 70, maxW)
		}
	}
}

// orbDraw lays down the orb itself, centered on (cx, cy) with outer radius rad.
func (r *renderer) orbDraw(cx, cy int, rad, t float64, phase string, level float64, pal orbPalette) {
	speed := 1.0
	if phase == "thinking" {
		speed = 3
	}
	strength := 0.5 + 0.25*math.Sin(2*math.Pi*t/2.8)
	if phase == "listening" || phase == "replying" {
		strength = 0.5 + 0.5*level
	}
	if phase == "lingering" {
		strength = 0.35
	}
	r.orbHalo(cx, cy, int(rad*1.45), strength, pal.accent, orbGround)

	turn := func(period float64) float64 { return 2 * math.Pi * t * speed / period }
	px := func(n int) float64 { return float64(r.s(n)) }

	// Outside in. Each ring turns at its own rate, directions alternating, as a HUD's do.
	r.hudRing(cx, cy, rad, px(1), pal.light, 0.35, "solid", 0)
	r.hudRing(cx, cy, rad*0.965, px(5), pal.accent, 0.85, "segments", turn(40))
	r.hudRing(cx, cy, rad*0.90, px(4), pal.light, 0.5, "scale", -turn(90))
	r.hudRing(cx, cy, rad*0.90, px(12), pal.light, 0.7, "majors", -turn(90))
	r.hudRing(cx, cy, rad*0.80, px(2), pal.accent, 0.3, "solid", 0)
	r.hudRing(cx, cy, rad*0.80, px(3), pal.light, 0.95, "arcs", -turn(7))
	r.hudRing(cx, cy, rad*0.71, px(2), pal.accent, 0.6, "dashes", turn(12))
	r.hudRing(cx, cy, rad*0.60, px(6), pal.accent, 0.55, "brackets", -turn(20))
	r.hudRing(cx, cy, rad*0.52, px(2), pal.light, 0.5, "reticle", 0)
	r.hudRing(cx, cy, rad*0.46, px(2), pal.light, 0.55, "dots", turn(9))

	switch phase {
	case "thinking":
		// A sweep: a bright arc with a fading tail, going round once a second.
		r.hudRing(cx, cy, rad*0.87, px(10), pal.light, 0.75, "sweep", 2*math.Pi*t)
	case "listening":
		// A ring that leaves the core and fades, once every 1.4 s.
		f := math.Mod(t, 1.4) / 1.4
		r.hudRing(cx, cy, math.Round(rad*(0.32+0.55*f)), px(2), pal.light, 0.7*(1-f), "solid", 0)
	case "replying":
		r.hudBars(cx, cy, rad*0.62, rad*0.2, t, level, pal.light)
	}

	core := rad * 0.30 * (1 + 0.04*math.Sin(2*math.Pi*t/2.6))
	if phase == "listening" || phase == "replying" {
		core = rad * 0.30 * (0.9 + 0.35*level)
	}
	r.orbCore(cx, cy, int(core), pal)
}

// A ring's pattern: its coverage at each angle, from 0 at three o'clock round clockwise. Worked out
// once into a table of lutSize steps, fine enough that a turn of one step moves a mark well under a
// pixel at the orb's size.
const lutSize = 4096

func hudPattern(name string, rad float64, a float64) float64 {
	switch name {
	case "segments":
		// Three long arcs of different lengths with short ones between, like a gauge's bands.
		deg := a * 180 / math.Pi
		for _, s := range [][2]float64{{0, 112}, {120, 128}, {134, 196}, {204, 208}, {214, 300}, {308, 318}, {326, 352}} {
			if deg >= s[0] && deg < s[1] {
				return clamp01(math.Min(deg-s[0], s[1]-deg)*math.Pi/180*rad + 0.5)
			}
		}
		return 0
	case "scale":
		return hudMarks(a, rad, 180, 1)
	case "majors":
		return hudMarks(a, rad, 18, 1.3)
	case "arcs":
		f := a / math.Pi
		f -= math.Floor(f)
		if f > 0.4 {
			return 0
		}
		return math.Sin(math.Pi * f / 0.4)
	case "dashes":
		return hudDash(a, rad, 90, 0.5)
	case "brackets":
		// Four heavy arcs, a quarter turn apart, with wide gaps: the ring that reads as machinery.
		f := a / (math.Pi / 2)
		f -= math.Floor(f)
		if f > 0.28 {
			return 0
		}
		return clamp01(math.Min(f, 0.28-f)*(math.Pi/2)*rad + 0.5)
	case "reticle":
		// A full ring broken at the four compass points.
		for q := 0.0; q <= 4; q++ {
			if math.Abs(a-q*math.Pi/2) < 0.12 {
				return 0
			}
		}
		return 1
	case "dots":
		return hudDash(a, rad, 72, 0.25)
	case "sweep":
		// Bright at the leading edge, fading over a third of a turn behind it.
		if a < 2*math.Pi/3 {
			return math.Pow(1-a/(2*math.Pi/3), 2)
		}
		return 0
	}
	return 1
}

// marks is n thin marks round a ring of radius rad, each about w pixels wide.
func hudMarks(a, rad float64, n int, w float64) float64 {
	slot := 2 * math.Pi * rad / float64(n)
	f := a / (2 * math.Pi) * float64(n)
	f -= math.Floor(f)
	return clamp01(w/2 + 0.5 - math.Min(f, 1-f)*slot)
}

// dash is n dashes, each duty of its slot, their ends softened by a pixel so they do not crawl.
func hudDash(a, rad float64, n int, duty float64) float64 {
	slot := 2 * math.Pi * rad / float64(n)
	f := a / (2 * math.Pi) * float64(n)
	f -= math.Floor(f)
	if f > duty {
		return 0
	}
	return clamp01(math.Min(f, duty-f)*slot + 0.5)
}

// ringPix is one pixel of a ring: where it is from the center, how much of it the ring covers, where it
// is round the ring (a step of the pattern table) and, for the bars, how far out it is.
type ringPix struct {
	dx, dy int16
	cov    uint8
	step   uint16
	dist   uint16 // tenths of a pixel from the center
}

type ringKey struct {
	name   string
	rad, w int // tenths of a pixel
}

var (
	ringMu    sync.Mutex
	ringCache = map[ringKey][]ringPix{}
)

// ringShape is the pixels of a ring of radius rad and width w, worked out the first time and kept. The
// pattern is not folded in: the ring turns, so every pixel within its width is listed.
func ringShape(rad, w float64) []ringPix {
	key := ringKey{"", int(rad * 10), int(w * 10)}
	ringMu.Lock()
	defer ringMu.Unlock()
	if s, ok := ringCache[key]; ok {
		return s
	}
	if len(ringCache) > 64 {
		clear(ringCache) // a size no longer in use: only ever a few dozen rings
	}
	var s []ringPix
	outer := rad + w/2 + 1
	for y := -int(outer) - 1; y <= int(outer)+1; y++ {
		for x := -int(outer) - 1; x <= int(outer)+1; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			d := math.Sqrt(fx*fx + fy*fy)
			cov := clamp01(0.5 - (math.Abs(d-rad) - w/2))
			if cov <= 0 {
				continue
			}
			a := math.Atan2(fy, fx)
			if a < 0 {
				a += 2 * math.Pi
			}
			s = append(s, ringPix{int16(x), int16(y), uint8(cov*255 + 0.5), uint16(a/(2*math.Pi)*lutSize) & (lutSize - 1), uint16(d * 10)})
		}
	}
	ringCache[key] = s
	return s
}

var (
	lutMu    sync.Mutex
	lutCache = map[ringKey]*[lutSize]uint8{}
)

// lutFor is the named pattern tabulated for a ring of radius rad.
func lutFor(name string, rad float64) *[lutSize]uint8 {
	key := ringKey{name, int(rad * 10), 0}
	lutMu.Lock()
	defer lutMu.Unlock()
	if l := lutCache[key]; l != nil {
		return l
	}
	if len(lutCache) > 64 {
		clear(lutCache)
	}
	l := new([lutSize]uint8)
	for i := range l {
		l[i] = uint8(clamp01(hudPattern(name, rad, 2*math.Pi*(float64(i)+0.5)/lutSize))*255 + 0.5)
	}
	lutCache[key] = l
	return l
}

// ring draws the named pattern as a ring of radius rad and width w in c at opacity alpha, turned by rot.
func (r *renderer) hudRing(cx, cy int, rad, w float64, c color.RGBA, alpha float64, name string, rot float64) {
	if alpha <= 0 || rad <= 1 {
		return
	}
	shape := ringShape(rad, w)
	var lut *[lutSize]uint8
	if name != "solid" {
		lut = lutFor(name, rad)
	}
	shift := int(math.Floor(rot / (2 * math.Pi) * lutSize))
	al := uint32(clamp01(alpha)*256 + 0.5)
	for _, p := range shape {
		cov := uint32(p.cov)
		if lut != nil {
			cov = cov * uint32(lut[(int(p.step)-shift)&(lutSize-1)]) / 255
		}
		r.blendFast(cx+int(p.dx), cy+int(p.dy), c, cov*al>>8)
	}
}

// bars is a ring of 48 bars standing out from r0, each up to h long, with the reply's level. Drawn
// from a ring shape h wide: a pixel is lit when it is on a bar and within that bar's height.
func (r *renderer) hudBars(cx, cy int, r0, h, t, level float64, c color.RGBA) {
	const n = 48
	shape := ringShape(r0+h/2, h)
	var heights [n]uint16
	for i := range heights {
		sway := 0.55 + 0.45*math.Sin(t*9+float64(i)*1.7)
		heights[i] = uint16((r0 + h*(0.12+0.88*level*sway)) * 10)
	}
	per := lutSize / n
	half := int(float64(per) * 0.21) // a bar is 42% of its slot
	lo := uint16(r0 * 10)
	al := uint32((0.35 + 0.6*level) * 256)
	for _, p := range shape {
		if p.dist < lo {
			continue
		}
		k := int(p.step) + per/2
		i, off := (k/per)%n, k%per-per/2
		if off > half || off < -half || p.dist > heights[i] {
			continue
		}
		r.blendFast(cx+int(p.dx), cy+int(p.dy), c, uint32(p.cov)*al>>8)
	}
}

// blendFast lays c over the pixel at (x, y) with coverage a of 256, in integers.
func (r *renderer) blendFast(x, y int, c color.RGBA, a uint32) {
	if a == 0 || x < 0 || y < 0 || x >= r.dst.Rect.Max.X || y >= r.dst.Rect.Max.Y {
		return
	}
	a = min(a, 256)
	i := y*r.dst.Stride + x*4
	p := r.dst.Pix[i : i+3 : i+3]
	b := 256 - a
	p[0] = uint8((uint32(p[0])*b + uint32(c.R)*a) >> 8)
	p[1] = uint8((uint32(p[1])*b + uint32(c.G)*a) >> 8)
	p[2] = uint8((uint32(p[2])*b + uint32(c.B)*a) >> 8)
}

// The soft shapes, the glow and the core, are pictures made once per size and kept: the glow over the
// page's plain ground, copied whole (it replaces the ground under the orb), and the core premultiplied,
// laid over the rings.
type softKey struct {
	kind      string
	rad       int
	c, ground color.RGBA
	step      int
}

var (
	softMu    sync.Mutex
	softCache = map[softKey]*image.RGBA{}
)

const orbHaloSteps = 8

func softPicture(key softKey, build func() *image.RGBA) *image.RGBA {
	softMu.Lock()
	defer softMu.Unlock()
	if img := softCache[key]; img != nil {
		return img
	}
	if len(softCache) > 96 {
		clear(softCache)
	}
	img := build()
	softCache[key] = img
	return img
}

// orbHalo lays the glow over a plain ground of color ground: it is copied whole, replacing what is under it.
func (r *renderer) orbHalo(cx, cy, rad int, strength float64, c, ground color.RGBA) {
	step := int(math.Round(clamp01(strength) * (orbHaloSteps - 1)))
	img := softPicture(softKey{"halo", rad, c, ground, step}, func() *image.RGBA {
		return haloImage(rad, c, ground, float64(step)/(orbHaloSteps-1))
	})
	draw.Draw(r.dst, img.Rect.Add(image.Pt(cx-rad, cy-rad)), img, image.Point{}, draw.Src)
}

func haloImage(rad int, c, ground color.RGBA, strength float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 2*rad, 2*rad))
	peak := 0.16 + 0.24*strength
	for y := 0; y < 2*rad; y++ {
		for x := 0; x < 2*rad; x++ {
			dx, dy := float64(x-rad)+0.5, float64(y-rad)+0.5
			u := math.Sqrt(dx*dx+dy*dy) / float64(rad)
			a := 0.0
			if u < 1 {
				a = peak * (1 - u) * (1 - u)
			}
			i := img.PixOffset(x, y)
			img.Pix[i+0] = uint8(float64(ground.R)*(1-a) + float64(c.R)*a + 0.5)
			img.Pix[i+1] = uint8(float64(ground.G)*(1-a) + float64(c.G)*a + 0.5)
			img.Pix[i+2] = uint8(float64(ground.B)*(1-a) + float64(c.B)*a + 0.5)
			img.Pix[i+3] = 255
		}
	}
	return img
}

// orbCore is the white-hot center: near white in the middle, the core tone, then the accent fading
// out. Its size moves with the voice, so it is kept per radius in two-pixel steps.
func (r *renderer) orbCore(cx, cy, rad int, pal orbPalette) {
	rad &^= 1
	if rad < 4 {
		return
	}
	img := softPicture(softKey{"core", rad, pal.accent, pal.core, 0}, func() *image.RGBA { return coreImage(rad, pal) })
	draw.Draw(r.dst, img.Rect.Add(image.Pt(cx-rad, cy-rad)), img, image.Point{}, draw.Over)
}

func coreImage(rad int, pal orbPalette) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 2*rad, 2*rad))
	white := color.RGBA{0xf6, 0xfd, 0xff, 0xff}
	for y := 0; y < 2*rad; y++ {
		for x := 0; x < 2*rad; x++ {
			dx, dy := float64(x-rad)+0.5, float64(y-rad)+0.5
			u := math.Sqrt(dx*dx+dy*dy) / float64(rad)
			if u >= 1 {
				continue
			}
			var c color.RGBA
			var a float64
			switch {
			case u < 0.25:
				c, a = lerp(white, pal.core, u/0.25), 1
			case u < 0.55:
				k := (u - 0.25) / 0.3
				c, a = lerp(pal.core, pal.light, k), 1-0.35*k
			default:
				k := (u - 0.55) / 0.45
				c, a = lerp(pal.light, pal.accent, k), 0.65*(1-k)*(1-k)
			}
			i := img.PixOffset(x, y)
			img.Pix[i+0] = uint8(float64(c.R)*a + 0.5)
			img.Pix[i+1] = uint8(float64(c.G)*a + 0.5)
			img.Pix[i+2] = uint8(float64(c.B)*a + 0.5)
			img.Pix[i+3] = uint8(255*a + 0.5)
		}
	}
	return img
}

// softCore is a glow for the idle orb: the light tone fading through the accent to nothing, no white
// center. Kept per radius in two-pixel steps, like the core.
func (r *renderer) softCore(cx, cy, rad int, pal orbPalette) {
	rad &^= 1
	if rad < 4 {
		return
	}
	img := softPicture(softKey{"softcore", rad, pal.accent, pal.light, 0}, func() *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, 2*rad, 2*rad))
		for y := 0; y < 2*rad; y++ {
			for x := 0; x < 2*rad; x++ {
				dx, dy := float64(x-rad)+0.5, float64(y-rad)+0.5
				u := math.Sqrt(dx*dx+dy*dy) / float64(rad)
				if u >= 1 {
					continue
				}
				c := lerp(pal.light, pal.accent, u)
				a := 0.75 * (1 - u) * (1 - u)
				i := img.PixOffset(x, y)
				img.Pix[i+0] = uint8(float64(c.R)*a + 0.5)
				img.Pix[i+1] = uint8(float64(c.G)*a + 0.5)
				img.Pix[i+2] = uint8(float64(c.B)*a + 0.5)
				img.Pix[i+3] = uint8(255*a + 0.5)
			}
		}
		return img
	})
	draw.Draw(r.dst, img.Rect.Add(image.Pt(cx-rad, cy-rad)), img, image.Point{}, draw.Over)
}

// spaced draws s in small capitals spread out, centered on cx with its baseline at y: the orb's label.
func (r *renderer) spaced(s string, cx, y float64, c color.RGBA) {
	r.spacedFace(r.small, s, cx, y, r.s(10), c)
}

// spacedFace is spaced in any face, with gap pixels between letters.
func (r *renderer) spacedFace(face font.Face, s string, cx, y float64, gap int, c color.RGBA) {
	w := 0
	for _, ch := range s {
		w += r.width(face, string(ch)) + gap
	}
	x := int(cx) - (w-gap)/2
	for _, ch := range strings.Split(s, "") {
		r.text(face, ch, x, int(y), c)
		x += r.width(face, ch) + gap
	}
}

// idleOrbFrame is how often the clock page is redrawn while the idle orb turns on it: slow enough to
// cost little, quick enough that the orb moves rather than ticks.
const idleOrbFrame = 200 * time.Millisecond

// idleOrbLabel is the word under the idle orb; empty draws none.
var idleOrbLabel = ""

// setTurnStyle is the settings screen's choice, saved and shown in Home Assistant.
func (d *Display) setTurnStyle(i int) { saveTurnStyle(d.turnStyle, i) }

// idleOrbForced draws the idle orb whatever the setting says, for the preview tests.
var idleOrbForced bool

func idleOrbOn() bool { return idleOrbForced || config.Get().Screen.TurnOrb }

// idleBackdrop is the orb at rest while Turn screen is Orb: its rings, large and faint and turning
// slowly, centered on the time and date, with a soft glow at their heart and idleOrbLabel small at the
// top right of the page. It is drawn before the time and date, so they sit over it. A tap anywhere free on the
// clock page starts a turn, as it already did.
func (r *renderer) idleBackdrop(s scene, cx, cy int) {
	pal := orbDayPalette()
	if inNight(config.Get().Screen.Night, s.now) {
		pal = orbNight
	}
	rad := math.Round(float64(r.h) * 0.44) // the whole circle on the panel
	t := float64(s.now.UnixMilli()%3600000) / 1000
	turn := func(period float64) float64 { return 2 * math.Pi * t / period }
	px := func(n int) float64 { return float64(r.s(n)) }
	breathe := 0.5 + 0.5*math.Sin(2*math.Pi*t/5)
	const f = 0.5 // the rings' strength: present, still under the time

	r.idleOrbDrawn = true
	if s.slideshow == nil {
		r.orbHalo(cx, cy, int(rad*1.1), 0.1+0.15*breathe, pal.accent, walnut)
	}
	r.hudRing(cx, cy, rad, px(1), pal.light, 0.35*f, "solid", 0)
	r.hudRing(cx, cy, rad*0.965, px(4), pal.accent, 0.9*f, "segments", turn(60))
	r.hudRing(cx, cy, rad*0.9, px(3), pal.light, 0.6*f, "scale", -turn(120))
	r.hudRing(cx, cy, rad*0.9, px(10), pal.light, 0.8*f, "majors", -turn(120))
	r.hudRing(cx, cy, rad*0.8, px(2), pal.light, 0.9*f, "arcs", -turn(18))
	r.hudRing(cx, cy, rad*0.71, px(2), pal.accent, 0.7*f, "dashes", turn(30))
	r.hudRing(cx, cy, rad*0.6, px(5), pal.accent, 0.6*f, "brackets", -turn(40))
	if idleOrbLabel != "" {
		// Top right, level with the weather on the left: centered, it ran into a long weather line.
		gap := r.s(4)
		w := -gap
		for _, ch := range idleOrbLabel {
			w += r.width(r.tiny, string(ch)) + gap
		}
		r.spacedFace(r.tiny, idleOrbLabel, float64(r.w-r.margin-w/2), float64(r.margin+r.s(26)), gap, pal.light)
	}
}
