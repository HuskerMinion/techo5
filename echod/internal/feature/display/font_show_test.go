//go:build !dot && !spot

package display

import (
	"bytes"
	"image"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
)

// A new font is drawn from the next frame on, with no restart: the renderer makes its faces again,
// the settings screen's too on a panel that scales them, and so does a font of one's own added to the
// folder.
func TestTheShowTakesANewFontAtOnce(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	ownFontDir(t)
	fonts.Rescan()
	s := scene{now: time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local), phase: "idle", weather: home.Weather{Condition: "sunny", Temp: "72°"}}
	for _, size := range []image.Point{{showWide, showHigh}, {show8Wide, show8High}, {skylightWide, skylightHigh}} {
		img := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
		r := newRenderer(img)
		r.draw(s)
		before := bytes.Clone(img.Pix)
		clock, sheet := r.clock, r.faces().label

		if err := config.Set().Screen().Font("saira"); err != nil {
			t.Fatal(err)
		}
		r.draw(s)
		if r.font != "saira" || r.clock == clock || bytes.Equal(before, img.Pix) {
			t.Errorf("%v: the frame after the change is not in the new font", size)
		}
		if r.faces().label == sheet {
			t.Errorf("%v: the settings screen kept the old font", size)
		}
		if _, ok := r.clock.(*fonts.Stack); !ok {
			t.Errorf("%v: the clock's face is a %T, without Go behind it", size, r.clock)
		}

		gen := r.fontGen
		if err := fonts.RemoveOwn("extra.ttf"); err != nil {
			t.Fatal(err)
		}
		r.draw(s)
		if r.fontGen == gen {
			t.Errorf("%v: removing a font of one's own did not make the faces again", size)
		}
		_ = config.Set().Screen().Font("")
		ownFontDir(t)
		fonts.Rescan()
	}
}
