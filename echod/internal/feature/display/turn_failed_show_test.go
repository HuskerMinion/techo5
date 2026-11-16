//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
)

// inkIn counts the pixels of c in area.
func inkIn(img *image.RGBA, area image.Rectangle, c color.RGBA) int {
	n := 0
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			if img.RGBAAt(x, y) == c {
				n++
			}
		}
	}
	return n
}

// The Classic words page says why a request failed where the answer would be, in the accent, on every
// panel; an answer is in its own ink and none of the accent is there.
func TestClassicWordsSayARequestFailed(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 30, 0, 0, time.Local)
	for _, panel := range showPanels() {
		frame := func(s scene) (*image.RGBA, image.Rectangle) {
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			r := newRenderer(img)
			r.draw(s)
			// The words' part of the page: under the corner clock, over the footer.
			return img, image.Rect(0, r.s(70), panel.wide, panel.high-r.s(70))
		}
		failed, words := frame(scene{now: at, phase: "lingering", heard: "Turn off the porch light", failed: voice.FailedNoHome})
		if inkIn(failed, words, amber) == 0 {
			t.Errorf("%s: a failed request has no line in the accent", panel.name)
		}
		alone, _ := frame(scene{now: at, phase: "lingering", failed: voice.FailedNoAssistant})
		if inkIn(alone, words, amber) == 0 {
			t.Errorf("%s: a request that could not open has no line in the accent", panel.name)
		}
		answered, _ := frame(scene{now: at, phase: "lingering", heard: "Turn off the porch light", reply: "The porch light is off."})
		if inkIn(answered, words, amber) != 0 {
			t.Errorf("%s: an answer is drawn in the accent", panel.name)
		}
	}
}

// The pictured turn screens put the reason in the answer's place: one more line under what was heard,
// in the accent by day and the night's own at night.
func TestTurnPicturesSayARequestFailed(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 30, 0, 0, time.Local)
	img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
	r := newRenderer(img)
	quiet := func(night bool) *eqView {
		return &eqView{level: make([]float64, eqBands), peak: make([]float64, eqBands), quiet: true, night: night}
	}
	s := scene{now: at, phase: "lingering", heard: "Turn off the porch light", failed: voice.FailedTimeout, eq: quiet(false)}
	lines := r.eqWords(s, showHigh, showWide-2*r.margin)
	if len(lines) != 2 {
		t.Fatalf("%d lines under the picture, want what was heard and the reason", len(lines))
	}
	if last := lines[1]; last.text != failedLine(voice.FailedTimeout) || last.c != amber {
		t.Errorf("the reason's line is %q in %v, want %q in the accent %v", last.text, last.c, failedLine(voice.FailedTimeout), amber)
	}
	s.eq = quiet(true)
	night := r.eqWords(s, showHigh, showWide-2*r.margin)
	if last := night[len(night)-1]; last.c == amber || last.c.R <= last.c.B {
		t.Errorf("at night the reason's line is %v, want the night's red", last.c)
	}
	for _, style := range []string{"bars", "wave", "vu", "orb", "hal", "face"} {
		s.eq = quiet(false)
		s.eq.wave, s.eq.style, s.eq.phase = style == "wave", style, s.phase
		faceState.set = false
		newRenderer(img).draw(s)
		if inkIn(img, img.Rect, amber) == 0 {
			t.Errorf("%s: the page has no line in the accent", style)
		}
	}
}

// Under a cue the reason is the captions, in the accent, and a tap on them is theirs; over a page that
// takes no captions nothing is laid.
func TestCaptionsSayARequestFailed(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 30, 0, 0, time.Local)
	for _, panel := range showPanels() {
		img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		r := newRenderer(img)
		r.draw(scene{now: at, phase: "idle", cue: "lingering", failed: voice.FailedTimeout})
		foot := image.Rect(0, panel.high-r.s(70), panel.wide, panel.high)
		if inkIn(img, foot, amber) == 0 {
			t.Errorf("%s: the captions have no line in the accent", panel.name)
		}
		if !r.captionsTapped(image.Pt(panel.wide/2, panel.high-r.s(20))) {
			t.Errorf("%s: a tap on the failure's captions is not theirs", panel.name)
		}
		r.draw(scene{now: at, phase: "idle", cue: "lingering", failed: voice.FailedTimeout, showSheet: true})
		if r.captionsTapped(image.Pt(panel.wide/2, panel.high-r.s(20))) {
			t.Errorf("%s: the failure's captions are over the settings", panel.name)
		}
	}
}
