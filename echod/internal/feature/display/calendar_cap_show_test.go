//go:build !dot && !spot

package display

import (
	"bytes"
	"image"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Above calendarMost the month and the week are drawn as at calendarMost, where their events keep their
// words, and the size chosen comes back for the next page.
func TestTheMonthAndTheWeekStopGrowingAt125(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Calendar().Sources(lookOrder)
	t.Cleanup(func() { config.Use(filepath.Join(t.TempDir(), "state.json")) })
	render := func(size int, s scene) (*image.RGBA, *renderer) {
		_ = config.Set().Screen().TextSize(size)
		img := image.NewRGBA(image.Rect(0, 0, skylightWide, skylightHigh))
		r := newRenderer(img)
		r.draw(s)
		return img, r
	}
	scenes := calendarScenes()
	for _, name := range []string{"calendar-month", "calendar-week"} {
		s, ok := scenes[name]
		if !ok {
			t.Fatalf("no %s scene", name)
		}
		at125, _ := render(125, s)
		at150, r := render(150, s)
		if !bytes.Equal(at125.Pix, at150.Pix) {
			t.Errorf("%s at 150%% is not drawn as at 125%%", name)
		}
		if r.textSize != 150 {
			t.Errorf("after %s the renderer's text size is %d, not the 150 chosen", name, r.textSize)
		}
	}
	// The agenda keeps the size chosen.
	a125, _ := render(125, scenes["calendar-agenda"])
	a150, _ := render(150, scenes["calendar-agenda"])
	if bytes.Equal(a125.Pix, a150.Pix) {
		t.Error("the agenda at 150% is drawn as at 125%; it should keep the size chosen")
	}
}
