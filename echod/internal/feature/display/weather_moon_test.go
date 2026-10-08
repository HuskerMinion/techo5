//go:build !dot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/moon"
)

// The phases the previews and tests draw, from the 2026 dates the U.S. Naval Observatory gives for them
// (lib/moon's tests), and two days between.
var moonPhases = []struct {
	name string
	at   time.Time
}{
	{"waxing-crescent", time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)},
	{"first-quarter", time.Date(2026, 10, 18, 16, 12, 0, 0, time.UTC)},
	{"full", time.Date(2026, 10, 26, 4, 12, 0, 0, time.UTC)},
	{"waning-gibbous", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
	{"last-quarter", time.Date(2026, 10, 3, 13, 25, 0, 0, time.UTC)},
	{"waning-crescent", time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)},
}

// moonScene is one moon to look at: its sky, its phase, and how far along its pass.
type moonScene struct {
	name  string
	cond  artCond
	when  artTime
	phase moon.Phase
	along float64
	south bool
	sun   float64 // by day, the sun's place through its day
}

// low is where a moon in phase p is low in the night sky: a waxing moon sets in the evening, a waning
// one rises after midnight.
func low(p moon.Phase) float64 {
	if p.Waxing && p.Fraction < 0.97 {
		return 0.88
	}
	return 0.12
}

// moonScenes are the previews: each phase at night, low and high; clear, partly cloudy and fog; a
// southern sky; the pale moon by day, a waxing gibbous in the afternoon and a waning crescent in the
// morning.
func moonScenes() []moonScene {
	var out []moonScene
	for _, p := range moonPhases {
		ph := moon.PhaseAt(p.at)
		out = append(out,
			moonScene{"night-" + p.name + "-high", artClear, artNight, ph, 0.5, false, 0},
			moonScene{"night-" + p.name + "-low", artClear, artNight, ph, low(ph), false, 0})
	}
	gib, cres := moon.PhaseAt(time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)), moon.PhaseAt(moonPhases[5].at)
	quarter := moon.PhaseAt(moonPhases[1].at)
	return append(out,
		moonScene{"night-first-quarter-partly", artPartly, artNight, quarter, 0.5, false, 0},
		moonScene{"night-full-partly", artPartly, artNight, moon.PhaseAt(moonPhases[2].at), 0.35, false, 0},
		moonScene{"night-full-fog", artFog, artNight, moon.PhaseAt(moonPhases[2].at), 0.4, false, 0},
		moonScene{"night-full-cloudy", artCloudy, artNight, moon.PhaseAt(moonPhases[2].at), 0.5, false, 0},
		moonScene{"night-first-quarter-south", artClear, artNight, quarter, 0.3, true, 0},
		moonScene{"day-waxing-gibbous-afternoon", artClear, artDay, gib, 0.15, false, 0.75},
		moonScene{"day-waning-crescent-morning", artClear, artDay, cres, 0.6, false, 0.25},
		moonScene{"day-waxing-gibbous-partly", artPartly, artDay, gib, 0.2, false, 0.7},
		moonScene{"dusk-waxing-gibbous", artClear, artDusk, gib, 0.1, false, 0},
	)
}

// writeMoonPreviews draws every moon scene on a panel w by h, through show (which lays the clock over
// the art), and writes each to dir as name-prefix.png.
func writeMoonPreviews(t *testing.T, dir, prefix string, w, h int, show func(art *image.RGBA, cond string) *image.RGBA) {
	conds := map[artCond]string{artClear: "sunny", artPartly: "partlycloudy", artCloudy: "cloudy", artFog: "fog"}
	at := time.Date(2026, 10, 13, 21, 14, 0, 0, time.UTC)
	for _, s := range moonScenes() {
		land := paintLandscape(artKey{s.cond, s.when, w, h, 42})
		frame := image.NewRGBA(land.base.Rect)
		composeArt(frame, land, artBodies{s.sun, true, artMoon{phase: s.phase, up: true, along: s.along, south: s.south, placed: true}}, at.Unix())
		img := show(frame, conds[s.cond])
		if dir == "" {
			continue
		}
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%s-%s.png", prefix, s.name)))
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(f, img)
		f.Close()
	}
}

// moonAtPx is the art composed with m over landscape k, without its clouds, and the same art with
// no moon.
func moonAtPx(k artKey, m artMoon) (frame, base *image.RGBA) {
	land := paintLandscape(k)
	land.clouds = nil
	frame, base = image.NewRGBA(land.base.Rect), image.NewRGBA(land.base.Rect)
	composeArt(frame, land, artBodies{moon: m}, 0)
	composeArt(base, land, artBodies{moon: artMoon{placed: true}}, 0)
	return frame, base
}

func pxLuma(c color.RGBA) float64 { return luma(c.R, c.G, c.B) }

// The moon is where its pass has it, lit on the side its phase says, from either hemisphere; it is
// gone when it is down or the sky is overcast, and hidden by the mountains on the horizon.
func TestArtMoonDraws(t *testing.T) {
	k := artKey{artClear, artNight, 800, 480, 42}
	quarter := moon.PhaseAt(moonPhases[1].at) // first quarter: waxing, half lit
	if !quarter.Waxing {
		t.Fatal("the first quarter is not waxing")
	}
	high := artMoon{phase: quarter, up: true, along: 0.5, placed: true}
	frame, base := moonAtPx(k, high)
	cx, cy := skyPath(k, 0.5, false)
	r := 800 * 0.038
	right, left := frame.RGBAAt(int(cx+r/2), int(cy)), frame.RGBAAt(int(cx-r/2), int(cy))
	if pxLuma(right) < 150 || pxLuma(left) > 90 {
		t.Errorf("first quarter from the north: right %v, left %v; want the right lit", right, left)
	}
	if pxLuma(left) <= pxLuma(base.RGBAAt(int(cx-r/2), int(cy)))+3 {
		t.Errorf("the dark part does not show against the sky: %v", left)
	}
	south := high
	south.south = true
	frame, _ = moonAtPx(k, south)
	if pxLuma(frame.RGBAAt(int(cx-r/2), int(cy))) < 150 || pxLuma(frame.RGBAAt(int(cx+r/2), int(cy))) > 90 {
		t.Error("first quarter from the south: want the left lit")
	}

	// A waxing gibbous, lit on both sides of the middle; a waning crescent only at its left edge.
	gib := artMoon{phase: moon.PhaseAt(time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)), up: true, along: 0.5, placed: true}
	frame, _ = moonAtPx(k, gib)
	if pxLuma(frame.RGBAAt(int(cx-r/4), int(cy))) < 150 {
		t.Error("a waxing gibbous is dark left of its middle")
	}
	cres := artMoon{phase: moon.PhaseAt(moonPhases[5].at), up: true, along: 0.5, placed: true}
	frame, _ = moonAtPx(k, cres)
	if pxLuma(frame.RGBAAt(int(cx-r*0.85), int(cy))) < 130 || pxLuma(frame.RGBAAt(int(cx), int(cy))) > 90 {
		t.Error("a waning crescent: want only its left edge lit")
	}

	for _, c := range []struct {
		name string
		k    artKey
		m    artMoon
	}{
		{"down", k, artMoon{phase: quarter, up: false, along: 0.5, placed: true}},
		{"overcast", artKey{artCloudy, artNight, 800, 480, 42}, high},
		{"rain", artKey{artRain, artNight, 800, 480, 42}, high},
		{"not yet placed, by day", artKey{artClear, artDay, 800, 480, 42}, artMoon{phase: quarter}},
	} {
		frame, base := moonAtPx(c.k, c.m)
		for i := range frame.Pix {
			if frame.Pix[i] != base.Pix[i] {
				t.Errorf("%s: a moon is drawn", c.name)
				break
			}
		}
	}

	// Just risen, its middle is still behind the mountains.
	risen := artMoon{phase: quarter, up: true, along: 0, placed: true}
	frame, base = moonAtPx(k, risen)
	x, y := skyPath(k, 0, false)
	if frame.RGBAAt(int(x), int(y)) != base.RGBAAt(int(x), int(y)) {
		t.Error("the moon shows in front of the mountains")
	}

	// By day it is there, but pale: far less against the sky than at night.
	dayK := artKey{artClear, artDay, 800, 480, 42}
	frame, base = moonAtPx(dayK, artMoon{phase: moon.PhaseAt(moonPhases[2].at), up: true, along: 0.5, placed: true})
	d := pxLuma(frame.RGBAAt(int(cx), int(cy))) - pxLuma(base.RGBAAt(int(cx), int(cy)))
	if d < 8 || d > 60 {
		t.Errorf("the full moon by day is %.0f brighter than the sky; want pale but there", d)
	}
}

// Rising on the left and setting on the right from the north, the other way from the south, and
// highest halfway.
func TestMoonPath(t *testing.T) {
	k := artKey{artClear, artNight, 800, 480, 42}
	x0, y0 := skyPath(k, 0.1, false)
	x1, y1 := skyPath(k, 0.5, false)
	x2, _ := skyPath(k, 0.9, false)
	if x0 >= x1 || x1 >= x2 || y1 >= y0 {
		t.Errorf("north: %.0f,%.0f then %.0f,%.0f then %.0f", x0, y0, x1, y1, x2)
	}
	if s0, _ := skyPath(k, 0.1, true); s0 <= x1 {
		t.Errorf("south: rises at %.0f, left of the middle", s0)
	}
}

// The moon laid over the art each second, at night with its glow: what it costs beyond the landscape.
func BenchmarkArtMoon(b *testing.B) {
	land := paintLandscape(artKey{artClear, artNight, 1280, 800, 42})
	frame := image.NewRGBA(land.base.Rect)
	m := artMoon{phase: moon.PhaseAt(moonPhases[2].at), up: true, along: 0.4, placed: true}
	for b.Loop() {
		drawMoon(frame, land, m)
	}
}

// By day the sun is on its arc: in the east in the morning, which is the left seen from the north and
// the right from the south; under the mountains at sunrise; gone from an overcast sky and at night.
func TestArtSunArc(t *testing.T) {
	day := artKey{artClear, artDay, 800, 480, 42}
	land := paintLandscape(day)
	land.clouds = nil
	bright := func(k artKey, b artBodies) (x, y int) {
		l := land
		if k != day {
			l = paintLandscape(k)
			l.clouds = nil
		}
		frame := image.NewRGBA(l.base.Rect)
		composeArt(frame, l, b, 0)
		best := -1.0
		for py := 0; py < k.h; py++ {
			for px := 0; px < k.w; px++ {
				if v := pxLuma(frame.RGBAAt(px, py)) - pxLuma(l.base.RGBAAt(px, py)); v > best {
					best, x, y = v, px, py
				}
			}
		}
		if best < 20 {
			return -1, -1
		}
		return x, y
	}
	if x, _ := bright(day, artBodies{sunAlong: 0.25, sunOK: true}); x < 0 || x >= 400 {
		t.Errorf("morning sun from the north at x %d; want the left half", x)
	}
	if x, _ := bright(day, artBodies{sunAlong: 0.25, sunOK: true, moon: artMoon{south: true}}); x < 400 {
		t.Errorf("morning sun from the south at x %d; want the right half", x)
	}
	if x, y := bright(day, artBodies{sunAlong: 0.5, sunOK: true}); x < 380 || x > 420 || y > 120 {
		t.Errorf("noon sun at %d,%d; want high in the middle", x, y)
	}
	if x, _ := bright(day, artBodies{sunAlong: 0, sunOK: true}); x >= 0 {
		t.Error("the sun shows in front of the mountains at sunrise")
	}
	if x, _ := bright(artKey{artCloudy, artDay, 800, 480, 42}, artBodies{sunAlong: 0.5, sunOK: true}); x >= 0 {
		t.Error("the sun shows through an overcast sky")
	}
	if x, _ := bright(artKey{artClear, artNight, 800, 480, 42}, artBodies{sunAlong: 0.5, sunOK: true, moon: artMoon{placed: true}}); x >= 0 {
		t.Error("a sun at night")
	}
}
