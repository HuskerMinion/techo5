//go:build !dot

package display

import (
	"math"
	"sync"
)

// The skins' looks (skin.go), on the Show and the Spot alike: each one's palette and the filter it lays
// over the rows on their way to the panel. What a skin draws on the Show's home clock is the Show's
// (skin_show.go, skin_backdrops.go, skin_lcars.go); the Spot takes its palette into its own colors
// (skin_spot.go).

// skinPanel is what the display's panel takes from a skin: the filter over its rows.
type skinPanel interface {
	SetFilter(name string, f func(row []byte, y int))
}

// unskinned takes the skin's filter off the panel for a frame drawn in colors that mean something,
// when plain: the red night clock, red so as not to light the room; on the Show a frame with the header's
// "microphone off" pill or Talk red while live (paint.marked); and on the Spot the red ring that says
// the microphones are off and the mark that says the camera is on. The next frame's applySkin puts the
// filter back.
func unskinned(p skinPanel, plain bool) {
	if plain && p != nil {
		p.SetFilter("", nil)
	}
}

// skinLook is what a skin brings besides its font: its palette and its filter. Either may be left out.
type skinLook struct {
	theme *theme
	mono  *monoSpec
}

// skinLooks are the skins' looks, by the value the config keeps.
var skinLooks = map[string]skinLook{
	skinPipBoy:    {theme: &pipBoyTheme, mono: &pipBoySpec},
	skinAmber:     {theme: &pipBoyTheme, mono: &amberSpec},
	skinLCARS:     {theme: &lcarsTheme},
	skinEInk:      {theme: &eInkTheme, mono: &eInkSpec},
	skinBlueprint: {theme: &blueprintTheme, mono: &blueprintSpec},
	skinGameBoy:   {theme: &gameBoyTheme, mono: &gameBoySpec},
	skinMatrix:    {theme: &matrixTheme, mono: &matrixSpec},
	skinSynthwave: {theme: &synthwaveTheme},
	skinHAL:       {theme: &halTheme},
}

// skinFilterFor is the skin in force's filter for a panel w by h, or nil for none.
func skinFilterFor(w, h int) func(row []byte, y int) {
	sk := skin()
	if spec := skinLooks[sk].mono; spec != nil {
		return monoFilter(sk, spec, w, h)
	}
	return nil
}

// lcarsTheme is the console's palette on black: orange for what the theme marks, peach for the words,
// lavender for what is said quietly and blue for the rules.
var lcarsTheme = preset("LCARS", 0x000000, 0xff9900, 0xffcc99, 0xcc99cc, 0x9999ff)

// The one-color skins are drawn in grays, which their filter turns to their color: a near-black ground,
// bright text, the accent brightest of all so that what the theme marks stands out by being lit
// harder, as on a one-color screen it has to. E-ink is the other way round, dark on light.
var (
	pipBoyTheme    = preset("Pip-Boy", 0x080a08, 0xffffff, 0xcfcfcf, 0x7c7c7c, 0x343434)
	eInkTheme      = preset("E-ink", 0xf2f2f2, 0x000000, 0x1e1e1e, 0x5a5a5a, 0xbdbdbd)
	blueprintTheme = preset("Blueprint", 0x101010, 0xffffff, 0xe6e6e6, 0x9a9a9a, 0x5a5a5a)
	gameBoyTheme   = preset("Game Boy", 0xe8e8e8, 0x000000, 0x202020, 0x707070, 0xa0a0a0)
	matrixTheme    = preset("Matrix", 0x000000, 0xffffff, 0xc8c8c8, 0x6e6e6e, 0x2e2e2e)
	synthwaveTheme = preset("Synthwave", 0x1a0b2e, 0xff2a6d, 0xfde7ff, 0xb967ff, 0x05d9e8)
	halTheme       = preset("HAL 9000", 0x050505, 0xff2a1a, 0xe2e2e2, 0x8c8c8c, 0x3a3a3a)
)

// monoSpec is a one-color skin's filter: the color each brightness, 0 to 255, comes out as, and what is
// laid over it.
type monoSpec struct {
	lut [256][3]uint8
	// scan darkens every third row by this much, 0 for none; vignette darkens the edges and corners by
	// this much, as a curved tube's are.
	scan, vignette float64
	// grid lifts the brightness of every grid'th row and column, in the Show 5's pixels, 0 for none.
	grid int
	// dither turns the brightness to four levels, with a 2 by 2 pattern between them.
	dither bool
}

// ramp is a LUT from dark to light along curve's brightness, each channel its own from-to.
func ramp(dark, light uint32, curve float64) (lut [256][3]uint8) {
	d, l := rgb(dark), rgb(light)
	for i := range lut {
		t := math.Pow(float64(i)/255, curve)
		mix := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t) }
		lut[i] = [3]uint8{mix(d.R, l.R), mix(d.G, l.G), mix(d.B, l.B)}
	}
	return lut
}

// phosphor is a tube's glow: the faint color of an unlit tube at 0, the full color in the middle, and
// past it toward white, as a hard-driven phosphor blooms.
func phosphor(r, g, b float64) (lut [256][3]uint8) {
	for i := range lut {
		t := float64(i) / 255
		k := math.Pow(t, 0.7)
		bloom := 120 * math.Pow(t, 3)
		ch := func(full float64) uint8 { return uint8(math.Min(full*k+0.06*full+bloom*(1-full/255), 255)) }
		lut[i] = [3]uint8{ch(r), ch(g), ch(b)}
	}
	return lut
}

var (
	pipBoySpec    = monoSpec{lut: phosphor(20, 255, 110), scan: 0.28, vignette: 0.28}
	amberSpec     = monoSpec{lut: phosphor(255, 186, 52), scan: 0.28, vignette: 0.28}
	eInkSpec      = monoSpec{lut: ramp(0x1b1b1b, 0xeeeae1, 1)}
	blueprintSpec = monoSpec{lut: ramp(0x0d3d6e, 0xf2f7ff, 0.9), grid: 24, vignette: 0.12}
	gameBoySpec   = monoSpec{lut: gameBoyShades(), dither: true}
	matrixSpec    = monoSpec{lut: ramp(0x000000, 0x9dffb4, 0.8)}
)

// gameBoyShades are the four greens of the original handheld's screen, darkest first, a quarter of
// the range each.
func gameBoyShades() (lut [256][3]uint8) {
	shades := [4][3]uint8{{0x0f, 0x38, 0x0f}, {0x30, 0x62, 0x30}, {0x8b, 0xac, 0x0f}, {0x9b, 0xbc, 0x0f}}
	for i := range lut {
		lut[i] = shades[i/64]
	}
	return lut
}

// monoFilters keeps the filter made for each skin and panel size: its shading is worked out once.
var monoFilters struct {
	mu sync.Mutex
	by map[monoKey]func([]byte, int)
}

type monoKey struct {
	skin string
	w, h int
}

// monoFilter turns a row of RGBA to the skin's color: each pixel's brightness through its LUT, with its
// scanlines, darker edges, grid or dither laid over.
func monoFilter(name string, spec *monoSpec, w, h int) func(row []byte, y int) {
	monoFilters.mu.Lock()
	defer monoFilters.mu.Unlock()
	k := monoKey{name, w, h}
	if f, ok := monoFilters.by[k]; ok {
		return f
	}
	// Shading as 0 to 256: across (by column) and down (by row, the scanlines in it).
	edge := func(i, n int) float64 {
		d := math.Abs(float64(i)+0.5-float64(n)/2) / (float64(n) / 2) // 0 middle, 1 edge
		return 1 - spec.vignette*math.Pow(d, 3)
	}
	across := make([]uint32, w)
	for x := range across {
		across[x] = uint32(256 * edge(x, w))
	}
	down := make([]uint32, h)
	for y := range down {
		v := edge(y, h)
		if spec.scan > 0 && y%3 == 2 {
			v *= 1 - spec.scan
		}
		down[y] = uint32(256 * v)
	}
	grid := 0
	if spec.grid > 0 {
		grid = max(spec.grid*w/960, 4) // in the Show 5's pixels, 960 across
	}
	lut := &spec.lut
	bayer := [2][2]int{{-24, 8}, {24, -8}}
	f := func(row []byte, y int) {
		if y < 0 || y >= h {
			return
		}
		dy := down[y]
		onRow := grid > 0 && y%grid == 0
		for x := 0; x+3 < len(row) && x/4 < w; x += 4 {
			l := int((77*uint32(row[x]) + 150*uint32(row[x+1]) + 29*uint32(row[x+2])) >> 8)
			if grid > 0 && (onRow || (x/4)%grid == 0) {
				l = min(l+34, 255)
			}
			if spec.dither {
				l = min(max(l+bayer[y&1][(x/4)&1], 0), 255)
			}
			c := &lut[l]
			k := dy * across[x/4] // 0 to 65536
			row[x] = uint8(uint32(c[0]) * k >> 16)
			row[x+1] = uint8(uint32(c[1]) * k >> 16)
			row[x+2] = uint8(uint32(c[2]) * k >> 16)
			row[x+3] = 0xff
		}
	}
	if monoFilters.by == nil {
		monoFilters.by = map[monoKey]func([]byte, int){}
	}
	monoFilters.by[k] = f
	return f
}
