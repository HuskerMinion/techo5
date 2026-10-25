package rainbow

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"

	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
)

// The Show 8's screen.
const showW, showH = 1280, 800

// page is a stand-in for the clock page under a turn: a dark blue ground with a few lines across it,
// the time and the date.
func page(t testing.TB, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			f := (float64(x)/float64(w) + float64(y)/float64(h)) / 2
			o := img.PixOffset(x, y)
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = uint8(20+18*f), uint8(26+16*f), uint8(51+26*f), 255
		}
	}
	for n := range 4 {
		for x := range w {
			u := float64(x) / float64(w)
			y := float64(h) * (0.3 + 0.2*float64(n) + 0.12*math.Sin(u*5+float64(n)))
			for d := -2; d <= 2; d++ {
				if yy := int(y) + d; yy >= 0 && yy < h {
					o := img.PixOffset(x, yy)
					img.Pix[o], img.Pix[o+1], img.Pix[o+2] = 60, 66, 110
				}
			}
		}
	}
	big, small := face(t, 230), face(t, 44)
	text(img, big, "10:42", w/2, h*42/100, color.RGBA{255, 255, 255, 255})
	text(img, small, "Tuesday, October 21", w/2, h*52/100, color.RGBA{204, 204, 204, 255})
	text(img, small, "63°  Partly cloudy", w/2, h*62/100, color.RGBA{204, 204, 204, 255})
	return img
}

func face(t testing.TB, size float64) font.Face {
	f, err := fonts.Face("", false, size)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// text draws s centered on x.
func text(img *image.RGBA, f font.Face, s string, x, y int, c color.RGBA) {
	w := font.MeasureString(f, s).Ceil()
	d := font.Drawer{Dst: img, Src: image.NewUniform(c), Face: f, Dot: fixed.P(x-w/2, y)}
	d.DrawString(s)
}

// shots is where the pictures go: RAINBOW_SHOTS when it is set, to look at them, or a directory thrown
// away after the test.
func shots(t *testing.T) string {
	if d := os.Getenv("RAINBOW_SHOTS"); d != "" {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		return d
	}
	return t.TempDir()
}

func save(t *testing.T, img *image.RGBA, dir, name string) {
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// script is a turn as the display sees it, by the second.
func script(at float64) Turn {
	const heard, reply = "turn on the living room lights and set the heating to twenty-one degrees when I get home", "Done, the living room lights are on. They're at 80% brightness."
	speech := func(t float64) float64 {
		return clamp01(math.Sin(t*9.3)*0.6 + math.Sin(t*3.1+1)*0.5 + math.Sin(t*17)*0.2)
	}
	switch {
	case at < 0:
		return Turn{}
	case at < 3.2:
		tr := Turn{Phase: "listening", Level: speech(at)}
		if at > 1.6 {
			tr.Heard = heard
		}
		return tr
	case at < 4.8:
		return Turn{Phase: "thinking", Heard: heard}
	case at < 8.2:
		return Turn{Phase: "replying", Level: speech(at * 0.85), Heard: heard, Reply: reply}
	case at < 10:
		return Turn{Phase: "lingering", Heard: heard, Reply: reply}
	}
	return Turn{}
}

// TestEachPhase plays a turn at the Show's pace, drawing every frame over a fresh page as the display
// does, and keeps a picture of each part of it: the entry, listening, thinking, answering, the answer
// lingering and the end.
func TestEachPhase(t *testing.T) {
	dir := shots(t)
	bg := page(t, showW, showH)
	save(t, bg, dir, "00-page.png")
	f := Faces{Heard: face(t, 35), Reply: face(t, 45)}
	var s Screen
	start := time.Date(2026, 10, 21, 10, 42, 0, 0, time.UTC)
	keep := map[int]string{
		2: "01-entry-0.13s", 5: "02-entry-0.33s", 8: "03-entry-0.53s", 12: "04-entry-0.80s", 18: "05-entry-1.20s",
		34: "06-listening", 45: "07-listening-heard", 62: "08-thinking", 90: "09-replying",
		105: "10-replying-later", 135: "11-lingering", 160: "12-after",
	}
	dst := image.NewRGBA(bg.Rect)
	// A sheet of them all, a third of the size, in the order they come.
	sheet := image.NewRGBA(image.Rect(0, 0, 4*showW/3, 3*showH/3))
	placed := 0
	defer func() { save(t, sheet, dir, "sheet.png") }()
	for n := 0; n <= 160; n++ {
		at := float64(n) * 0.066
		copy(dst.Pix, bg.Pix)
		tr := script(at)
		cardAt := s.Draw(dst, start.Add(time.Duration(at*float64(time.Second))), tr, f)
		name, ok := keep[n]
		if !ok {
			continue
		}
		save(t, dst, dir, name+".png")
		third(sheet, dst, placed%4*showW/3, placed/4*showH/3)
		placed++
		switch {
		case tr.Phase == "listening" && n >= 34:
			// The light is on along the top and bottom.
			if lum(dst, showW/2, 3) < lum(bg, showW/2, 3)+40 {
				t.Errorf("%s: the top edge is not lit", name)
			}
		case tr.Phase == "thinking":
			// Along the top and bottom alone: the middle of the sides all but dark.
			if lum(dst, showW/2, 3) < lum(bg, showW/2, 3)+40 {
				t.Errorf("%s: the top edge is not lit", name)
			}
			if lum(dst, 30, showH/2)-lum(bg, 30, showH/2) > 25 {
				t.Errorf("%s: the side is lit: %.0f over %.0f", name, lum(dst, 30, showH/2), lum(bg, 30, showH/2))
			}
		case n == 160:
			if !same(dst, bg) || s.Busy() || !cardAt.Empty() {
				t.Errorf("%s: something is left on the page", name)
			}
		}
		if (tr.Phase == "replying" || tr.Phase == "lingering") && cardAt.Empty() {
			t.Errorf("%s: no card", name)
		}
	}
}

// TestCornersHaveNoSeam walks round a rounded corner just inside the edge and checks the light goes
// round it smoothly: no step between neighbors bigger than along the straight.
func TestCornersHaveNoSeam(t *testing.T) {
	g := newGeometry(showW, showH)
	r := cornerR
	prev := -1.0
	for i := 0; i <= 90; i++ {
		a := math.Pi + float64(i)/90*math.Pi/2 // the top left corner, from the left side to the top
		x, y := r+(r-3)*math.Cos(a), r+(r-3)*math.Sin(a)
		u, _, _ := g.per.at(x, y)
		if prev >= 0 {
			du := math.Abs(u - prev)
			if du > 0.5 {
				du = 1 - du
			}
			if du > 0.002 {
				t.Errorf("a step of %.4f round the corner at %d°", du, i)
			}
		}
		prev = u
	}
}

// third draws src at a third of its size into dst at x, y.
func third(dst, src *image.RGBA, x, y int) {
	for j := range src.Rect.Dy() / 3 {
		for i := range src.Rect.Dx() / 3 {
			var sum [3]int
			for dy := range 3 {
				for dx := range 3 {
					o := src.PixOffset(3*i+dx, 3*j+dy)
					sum[0], sum[1], sum[2] = sum[0]+int(src.Pix[o]), sum[1]+int(src.Pix[o+1]), sum[2]+int(src.Pix[o+2])
				}
			}
			o := dst.PixOffset(x+i, y+j)
			dst.Pix[o], dst.Pix[o+1], dst.Pix[o+2], dst.Pix[o+3] = uint8(sum[0]/9), uint8(sum[1]/9), uint8(sum[2]/9), 255
		}
	}
}

func lum(img *image.RGBA, x, y int) float64 {
	o := img.PixOffset(x, y)
	return 0.299*float64(img.Pix[o]) + 0.587*float64(img.Pix[o+1]) + 0.114*float64(img.Pix[o+2])
}

func same(a, b *image.RGBA) bool {
	for i := range a.Pix {
		if a.Pix[i] != b.Pix[i] {
			return false
		}
	}
	return true
}

// The frames' costs on the Show 8's screen, over a page drawn again for each, as the display does
// (the page's copy not counted): a whole frame in each phase, and its parts alone. The number in each
// name is how many goroutines the work is split over. Run on the device itself (go test -c for
// GOARCH=arm): this Mac is over a hundred times faster than the Show's processor, one core to one.
func benchTurn(b *testing.B, at float64, workers int, part func(s *Screen, dst *image.RGBA, g *geometry, when time.Time)) {
	was := Workers
	Workers = workers
	defer func() { Workers = was }()
	bg := page(b, showW, showH)
	f := Faces{Heard: face(b, 35), Reply: face(b, 45)}
	var s Screen
	start := time.Now()
	dst := image.NewRGBA(bg.Rect)
	// Into the turn as far as at, at the Show's pace.
	for n := 0; float64(n)*0.066 < at; n++ {
		copy(dst.Pix, bg.Pix)
		s.Draw(dst, start.Add(time.Duration(float64(n)*0.066*float64(time.Second))), script(float64(n)*0.066), f)
	}
	when := start.Add(time.Duration(at * float64(time.Second)))
	tr := script(at)
	g := s.geometryFor(showW, showH)
	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		copy(dst.Pix, bg.Pix)
		// Each frame the same moment again: the entry is not over before the benchmark is.
		s.last = when.Add(-66 * time.Millisecond)
		b.StartTimer()
		if part != nil {
			part(&s, dst, g, when)
		} else {
			s.Draw(dst, when, tr, f)
		}
	}
}

func BenchmarkListening1(b *testing.B) { benchTurn(b, 2.5, 1, nil) }
func BenchmarkListening3(b *testing.B) { benchTurn(b, 2.5, 3, nil) }
func BenchmarkThinking3(b *testing.B)  { benchTurn(b, 4.0, 3, nil) }
func BenchmarkEntry3(b *testing.B)     { benchTurn(b, 0.5, 3, nil) }
func BenchmarkReplying3(b *testing.B)  { benchTurn(b, 6.0, 3, nil) }

// The parts: the light alone, its last step alone (adding it onto the screen), the entry alone, and
// the card painted again as it is while it moves.
func BenchmarkGlow3(b *testing.B) {
	benchTurn(b, 2.5, 3, func(s *Screen, dst *image.RGBA, g *geometry, when time.Time) { s.glow(dst, g, when, false) })
}

func BenchmarkGlowAdd3(b *testing.B) {
	benchTurn(b, 2.5, 3, func(s *Screen, dst *image.RGBA, g *geometry, _ time.Time) {
		addUp2(dst, s.glowBufs.half, g.hw, g.hh, g.inner, g.soft2)
	})
}

func BenchmarkEntryAlone3(b *testing.B) {
	benchTurn(b, 0.5, 3, func(s *Screen, dst *image.RGBA, g *geometry, _ time.Time) { s.entry(dst, g, 0.5) })
}

func BenchmarkCardPaint3(b *testing.B) {
	benchTurn(b, 6.0, 3, func(s *Screen, dst *image.RGBA, g *geometry, when time.Time) {
		s.card.look = cardLook{}
		s.card.draw(dst, g, when)
	})
}

func TestCapital(t *testing.T) {
	for in, want := range map[string]string{
		"enciende la luz": "Enciende la luz", "¿qué hora es?": "¿Qué hora es?", "árbol": "Árbol",
		"": "", "  hola": "  Hola", "3 minutos": "3 minutos", "Ya": "Ya",
	} {
		if got := capital(in); got != want {
			t.Errorf("capital(%q) = %q, want %q", in, got, want)
		}
	}
}
