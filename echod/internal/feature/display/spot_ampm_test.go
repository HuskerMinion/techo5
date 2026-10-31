//go:build spot

package display

import (
	"image"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// At the largest text size a 12-hour clock's AM/PM stays on the round face: nothing is drawn in the
// last ampmEdge pixels at the right of the clock's line.
func TestTheSpotsAMPMStaysOnTheFace(t *testing.T) {
	previewFont(t)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Screen().TextSize(textSteps[len(textSteps)-1]); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	r := newRoundRenderer(img)
	r.timeLine(time.Date(2026, 9, 16, 12, 37, 0, 0, time.Local), center)
	for y := center - 60; y <= center+10; y++ {
		for x := side - ampmEdge; x < side; x++ {
			if img.RGBAAt(x, y).A != 0 {
				t.Fatalf("drawn at %d,%d, past the AM/PM's edge", x, y)
			}
		}
	}
}
