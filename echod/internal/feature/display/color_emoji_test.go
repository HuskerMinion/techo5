//go:build !dot

package display

import (
	"image"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
)

// colorEmojiFace is the screen's face with lib/fonts' test color font in the fonts folder: 🔴 drawn as
// a red square with a green one in it.
func colorEmojiFace(t *testing.T) font.Face {
	t.Helper()
	old := fonts.Dir
	fonts.Dir = filepath.Join(t.TempDir(), "fonts")
	t.Cleanup(func() { fonts.Dir = old; fonts.Rescan() })
	f, err := os.Open(filepath.Join("..", "..", "lib", "fonts", "testdata", "color-test.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := fonts.AddOwn("color-test.ttf", f); err != nil {
		t.Fatal(err)
	}
	face, err := fonts.Face(fonts.Go, false, 40)
	if err != nil {
		t.Fatal(err)
	}
	return face
}

// hues counts the pixels of img that are mostly green, and those with any green or blue in them over a
// tenth of their red: the colors a one-tint red cannot make.
func hues(img *image.RGBA) (green, offTint int) {
	for i := 0; i < len(img.Pix); i += 4 {
		r, g, b := int(img.Pix[i]), int(img.Pix[i+1]), int(img.Pix[i+2])
		if g > 100 && g > 2*r {
			green++
		}
		if b > 0 || g*10 > r+10 {
			offTint++
		}
	}
	return green, offTint
}

// Text drawn as by day shows an emoji in its colors; drawn for the night clock, all in its one tint.
func TestAnEmojiIsInColorByDayAndOneTintAtNight(t *testing.T) {
	face := colorEmojiFace(t)
	for _, oneTint := range []bool{false, true} {
		img := image.NewRGBA(image.Rect(0, 0, 200, 60))
		p := &paint{dst: img, w: 200, h: 60, oneTint: oneTint}
		p.text(face, "Up 🔴", 4, 44, nightRed)
		green, off := hues(img)
		switch {
		case !oneTint && green < 50:
			t.Errorf("by day: %d green pixels, so the emoji is not in its colors", green)
		case oneTint && off > 0:
			t.Errorf("at night: %d pixels not in the night's red", off)
		}
	}
	if (&paint{}).oneTint {
		t.Error("a paint draws in one tint before it is asked to")
	}
}
