//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// has reports whether any pixel in r of img is within a few steps of c.
func has(img *image.RGBA, r image.Rectangle, c color.RGBA) bool {
	near := func(a, b uint8) bool { d := int(a) - int(b); return d > -12 && d < 12 }
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			p := img.RGBAAt(x, y)
			if near(p.R, c.R) && near(p.G, c.G) && near(p.B, c.B) {
				return true
			}
		}
	}
	return false
}

// Over a photo, the theme's dim text takes its text color to stay readable, but a date color somebody
// chose is kept, and the AM/PM goes with it (#53).
func TestAChosenDateColorIsKeptOverAPhoto(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	photo := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
	draw.Draw(photo, photo.Rect, image.NewUniform(color.RGBA{200, 200, 200, 255}), image.Point{}, draw.Src)

	draw1 := func() (*image.RGBA, image.Rectangle) {
		img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
		r := newRenderer(img)
		var at image.Rectangle
		r.readableOver(photo, walnut, slideshowWash, func() {
			at = r.timeAndDateAt(time.Date(2026, 9, 28, 14, 5, 0, 0, time.Local), r.h/2, "", 0, 0, false, 0)
		})
		return img, at
	}

	img, at := draw1()
	if has(img, at, color.RGBA{0xE8, 0xC0, 0x4E, 0xff}) {
		t.Error("with no date color chosen, the date over a photo is still the old gold (#258)")
	}

	rose := dateColors[len(dateColors)-1]
	if err := config.Set().Screen().DateColor(rose.value); err != nil {
		t.Fatal(err)
	}
	img, at = draw1()
	if !has(img, at, rose.c) {
		t.Errorf("the chosen date color %s did not survive over a photo", rose.label)
	}
}

// Over a photo a line keeps its theme color, but the theme's dim, which sinks into a picture, takes
// the theme's text color (#258).
func TestOverAPhotoTheThemesColorsStay(t *testing.T) {
	if overPhotoColor(dim) != cream {
		t.Error("dim text was not lifted to the text color")
	}
	for _, c := range []color.RGBA{cream, amber, ember} {
		if c != dim && overPhotoColor(c) != c {
			t.Errorf("%v changed over a photo", c)
		}
	}
}
