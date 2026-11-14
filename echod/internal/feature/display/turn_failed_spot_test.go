//go:build spot

package display

import (
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
)

// The Spot's turn faces say why a request failed where the answer would be, in the timer's amber, with
// the rim in the mute's red; an answer has neither.
func TestTheSpotSaysARequestFailed(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 30, 0, 0, time.Local)
	face := func(s roundScene) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		faceState.set = false
		newRoundRenderer(img).draw(s)
		return img
	}
	rim := func(img *image.RGBA) bool { return img.RGBAAt(center, center-(rimIn+rimOut)/2) == colMuted }
	quiet := func(style string) *eqView {
		return &eqView{level: make([]float64, eqBands), peak: make([]float64, eqBands), quiet: true, style: style, wave: style == "wave", phase: "lingering"}
	}

	answered := face(roundScene{now: at, phase: "lingering", heard: "Turn off the porch light", reply: "The porch light is off."})
	if rim(answered) || len(inkRows(answered, colTimer)) != 0 {
		t.Fatal("an answer has the failure's rim or its amber")
	}
	for name, s := range map[string]roundScene{
		"words":       {heard: "Turn off the porch light", failed: voice.FailedNoHome},
		"words alone": {failed: voice.FailedNoAssistant},
		"bars":        {heard: "Turn off the porch light", failed: voice.FailedTimeout, eq: quiet("bars")},
		"wave":        {failed: voice.FailedError, eq: quiet("wave")},
		"orb":         {failed: voice.FailedTimeout, eq: quiet("orb")},
	} {
		s.now, s.phase = at, "lingering"
		img := face(s)
		if !rim(img) {
			t.Errorf("%s: the rim is %v, want the mute's red", name, img.RGBAAt(center, center-(rimIn+rimOut)/2))
		}
		if len(inkRows(img, colTimer)) == 0 {
			t.Errorf("%s: no line says why", name)
		}
	}
}

// Under a cue the reason is the captions over the face, and the rim says it too.
func TestTheSpotsCaptionsSayARequestFailed(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 30, 0, 0, time.Local)
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	r := newRoundRenderer(img)
	r.draw(roundScene{now: at, phase: "idle", cue: "lingering", failed: voice.FailedTimeout})
	if len(inkRows(img, colTimer)) == 0 {
		t.Error("the captions have no line that says why")
	}
	if got := img.RGBAAt(center, center-(rimIn+rimOut)/2); got != colMuted {
		t.Errorf("the rim is %v, want the mute's red", got)
	}
	if !r.captionsTapped(image.Pt(center, captionTop)) {
		t.Error("a tap on the failure's captions is not theirs")
	}
}
