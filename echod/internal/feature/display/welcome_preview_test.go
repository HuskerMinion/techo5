//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// The welcome card at every panel size, with and without a microphone; with SHOW_PREVIEW set, each is
// written there to look at. Its lines are spaced in the Show 5's pixels and scaled like its type:
// unscaled, the Skylight's lines ran into each other.
func TestWelcomeDrawsAtEverySize(t *testing.T) {
	previewFont(t)
	restore := hasMicrophone
	t.Cleanup(func() { hasMicrophone = restore })
	dir := os.Getenv("SHOW_PREVIEW")
	for _, mic := range []bool{true, false} {
		hasMicrophone = func() bool { return mic }
		for _, panel := range []struct {
			name       string
			wide, high int
		}{
			{"", showWide, showHigh},
			{"-show8", show8Wide, show8High},
			{"-skylight", skylightWide, skylightHigh},
		} {
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			newRenderer(img).welcome(scene{})
			if dir == "" {
				continue
			}
			name := "welcome" + panel.name
			if !mic {
				name += "-nomic"
			}
			f, err := os.Create(filepath.Join(dir, name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, img); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}
