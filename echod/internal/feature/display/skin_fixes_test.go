//go:build !dot && !spot

package display

import (
	"image"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// wearSkin puts the skin v on for the test, and takes it off after.
func wearSkin(t *testing.T, v string) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Screen().Skin(v); err != nil {
		t.Fatal(err)
	}
	applyTheme(current())
	t.Cleanup(func() {
		_ = config.Set().Screen().Skin("")
		applyTheme(current())
	})
}

// filterLog is a panel that keeps the filters it was given.
type filterLog struct{ names []string }

func (f *filterLog) SetFilter(name string, _ func([]byte, int)) { f.names = append(f.names, name) }

// The red night clock, and only it, has the skin's filter taken off.
func TestRedClockUnskinned(t *testing.T) {
	wearSkin(t, skinPipBoy)
	var p filterLog
	applySkin(&p, showWide, showHigh)
	unskinned(&p, scene{phase: "idle"}.redClockFace())
	if got := p.names[len(p.names)-1]; got != skinPipBoy {
		t.Fatalf("the clock by day sent filter %q, want the skin's", got)
	}
	applySkin(&p, showWide, showHigh)
	unskinned(&p, scene{phase: "idle", redClock: true}.redClockFace())
	if got := p.names[len(p.names)-1]; got != "" {
		t.Fatalf("the red night clock sent filter %q, want none", got)
	}
}

// The badges at the top right start under LCARS's top bar, and the Corner timer's under HAL's eye.
func TestBadgesClearOfTheSkins(t *testing.T) {
	timers := []timer.Countdown{{Name: "Pasta", Left: 4 * time.Minute, Total: 10 * time.Minute, Active: true}}
	for _, sk := range []string{skinLCARS, skinHAL} {
		t.Run(sk, func(t *testing.T) {
			wearSkin(t, sk)
			img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
			r := newRenderer(img)
			s := scene{now: time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local), phase: "idle", timers: timers}
			clear := r.s(lcarsEdge + lcarsBarH) // the LCARS bar's foot
			if eye := r.halEyeAt(s); sk == skinHAL {
				if eye.Empty() {
					t.Fatal("no eye under the HAL 9000 skin")
				}
				clear = eye.Max.Y
			}
			r.timerCorner(s, true)
			for y := 0; y < clear; y++ {
				for x := r.w / 2; x < r.w; x++ {
					if img.Pix[img.PixOffset(x, y)+3] != 0 {
						t.Fatalf("the timer badge reaches row %d, above %d", y, clear)
					}
				}
			}
		})
	}
}

// Three long names on a Show 5 are cut so the Ring style's row stays inside the margins.
func TestRingTimersFitAShow5(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
	r := newRenderer(img)
	long := []string{"Sourdough second rise", "Roast potatoes in the oven", "Call back about the delivery"}
	var s scene
	for i, n := range long {
		s.timers = append(s.timers, timer.Countdown{Name: n, Left: time.Duration(10+i) * time.Minute, Total: time.Hour, Active: true})
	}
	r.timerRings(s, r.h-r.s(60))
	for y := range r.h {
		for x := range r.w {
			if (x < r.margin || x >= r.w-r.margin) && img.Pix[img.PixOffset(x, y)+3] != 0 {
				t.Fatalf("the rings' row reaches x %d, outside the margin %d", x, r.margin)
			}
		}
	}
}

// A band a few pixels shorter, as a streaming answer makes it, keeps the meters and the eye as drawn:
// they are drawn again only each keptStep pixels.
func TestTurnPicturesKeptWhileTheBandShrinks(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
	v := &eqView{level: make([]float64, 16)}
	drawVU(img, image.Rect(40, 60, 920, 310), v, eqGround)
	kept := vuKept.img
	drawVU(img, image.Rect(40, 60, 920, 307), v, eqGround)
	if vuKept.img != kept {
		t.Error("three pixels off the band drew the meters' faces again")
	}
	drawHAL(img, image.Rect(40, 60, 920, 310), v, "replying", time.Now())
	side := halEyes.side
	drawHAL(img, image.Rect(40, 60, 920, 307), v, "replying", time.Now())
	if halEyes.side != side {
		t.Error("three pixels off the band drew the eye again")
	}
	for _, h := range []int{5, 16, 31, 240} {
		b := keptBand(image.Rect(0, 10, 100, 10+h))
		if b.Dy() > h || b.Dy() <= 0 || (h >= keptStep && b.Dy()%keptStep != 0) {
			t.Errorf("a band %d tall is kept %d tall", h, b.Dy())
		}
	}
}

// The Nixie tubes over a photo: the first pass notes where they go and draws nothing.
func TestNixieRecordPassOnlyNotes(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
	r := newRenderer(img)
	r.over.record = true
	at := r.nixieClock(&r.nixie, [4]int{1, 2, 3, 4}, "PM", image.Rect(40, 80, 920, 380), r.small, cream, walnut)
	if len(r.over.boxes) != 1 || r.over.boxes[0] != at || at.Empty() {
		t.Fatalf("noted %v, want the tubes' place %v", r.over.boxes, at)
	}
	for _, b := range img.Pix {
		if b != 0 {
			t.Fatal("the first pass drew")
		}
	}
	r.over.record = false
	if drawn := r.nixieClock(&r.nixie, [4]int{1, 2, 3, 4}, "PM", image.Rect(40, 80, 920, 380), r.small, cream, walnut); drawn != at {
		t.Errorf("drawn at %v, noted at %v", drawn, at)
	}
}

// Under a light skin the turn page is light, and its words dark enough to read on it.
func TestTurnPageUnderALightSkin(t *testing.T) {
	wearSkin(t, skinEInk)
	s := scene{phase: "replying", heard: "what time is it", reply: "It's 2:07.", eq: &eqView{}}
	g := turnGround(s)
	if luma(g.R, g.G, g.B) < 128 {
		t.Fatalf("the turn page under E-ink is %v, want the skin's light ground", g)
	}
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	for _, l := range r.eqWords(s, r.h, r.w-2*r.margin) {
		if luma(l.c.R, l.c.G, l.c.B) > 128 {
			t.Errorf("%q is %v on a light ground", l.text, l.c)
		}
	}
	s.eq.night = true
	if g := turnGround(s); g != eqGround {
		t.Errorf("at night the turn page is %v, want it dark", g)
	}
}
