//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func streamerDeck(connected bool) deckView {
	v := deckView{cols: 4, rows: 3, pages: 2, connected: connected, obsSet: true}
	if !connected {
		v.problem = "OBS refused the password"
	}
	buttons := []deckButtonView{
		{label: "Starting", icon: "monitor", lit: false, known: true},
		{label: "Main", icon: "monitor", lit: true, known: true},
		{label: "BRB", icon: "monitor", known: true},
		{label: "Ending", icon: "monitor", known: true},
		{label: "Stream", icon: "access-point", color: "red", lit: true, known: true},
		{label: "Record", icon: "record", color: "red", known: true},
		{label: "Mic", icon: "microphone-off", color: "orange", lit: true, known: true},
		{label: "Desktop", icon: "microphone", color: "orange", known: true, pressed: true},
		{label: "Webcam", icon: "eye", color: "blue", lit: true, known: true},
		{label: "Chat", icon: "eye-off", color: "purple", known: true, failed: true},
		{empty: true},
		{label: "A label far too long to fit on one button", icon: "star", color: "green", known: true},
	}
	for i := range buttons {
		if !connected {
			buttons[i].known = false
		}
	}
	v.buttons = buttons
	return v
}

// The deck page draws a button where each was asked for, in a grid that fills the page, and a tap
// finds the button it landed on. SHOW_PREVIEW=<dir> writes the pages out to look at.
func TestDeckPageDrawsTheGrid(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	at := time.Date(2026, 10, 5, 14, 7, 0, 0, time.Local)
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}} {
		for _, connected := range []bool{true, false} {
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			r := newRenderer(img)
			r.draw(scene{now: at, phase: "idle", showDeck: true, deck: streamerDeck(connected)})
			r.zmu.Lock()
			zones := append([]image.Rectangle(nil), r.deckZones...)
			r.zmu.Unlock()
			if len(zones) != 12 {
				t.Fatalf("%s: %d buttons, want 12", panel.name, len(zones))
			}
			// Row by row, and the grid uses most of the page.
			if zones[1].Min.X <= zones[0].Max.X || zones[4].Min.Y <= zones[0].Max.Y || zones[4].Min.X != zones[0].Min.X {
				t.Errorf("%s: not row by row: %v %v %v", panel.name, zones[0], zones[1], zones[4])
			}
			if area := zones[11].Max.Sub(zones[0].Min); area.X*area.Y*10 < panel.wide*panel.high*7 {
				t.Errorf("%s: the grid %v is small for a %dx%d page", panel.name, area, panel.wide, panel.high)
			}
			for i, z := range zones {
				if got, ok := r.deckHit(z.Min.Add(z.Size().Div(2))); !ok || got != i {
					t.Errorf("%s: a tap on button %d found %d, %v", panel.name, i, got, ok)
				}
			}
			if _, ok := r.deckHit(image.Pt(zones[0].Max.X+1, zones[0].Min.Y+5)); ok {
				t.Errorf("%s: a tap in the gap found a button", panel.name)
			}
			if dir != "" {
				name := "deck" + panel.name + map[bool]string{true: "", false: "-away"}[connected] + ".png"
				f, err := os.Create(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
				png.Encode(f, img)
				f.Close()
			}
		}
	}
}

// BenchmarkDeckFrame is one deck frame on a Show 5, the backdrop already made: what a press costs to
// redraw.
func BenchmarkDeckFrame(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
	r := newRenderer(img)
	s := scene{now: time.Now(), phase: "idle", showDeck: true, deck: streamerDeck(true)}
	r.draw(s)
	b.ResetTimer()
	for range b.N {
		r.draw(s)
	}
}

// A frame of the deck alone has a key, and the same deck the same key; anything over it or a press
// changing has none or another.
func TestDeckFrameKey(t *testing.T) {
	s := scene{showDeck: true, deck: streamerDeck(true)}
	k := deckFrameKey(s, false, false)
	if k == "" || k != deckFrameKey(scene{showDeck: true, deck: streamerDeck(true)}, false, false) {
		t.Fatal("the same deck has no key, or two")
	}
	pressed := streamerDeck(true)
	pressed.buttons[0].pressed = true
	if deckFrameKey(scene{showDeck: true, deck: pressed}, false, false) == k {
		t.Error("a press didn't change the key")
	}
	if deckFrameKey(s, true, false) != "" || deckFrameKey(scene{showDeck: true, showVolume: true, deck: s.deck}, false, false) != "" {
		t.Error("a ring or the volume bar over the deck still had a key")
	}
}
