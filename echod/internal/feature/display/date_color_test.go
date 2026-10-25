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

// Over a photo, small text turns gold to stay readable, but a date color somebody chose is kept, and the
// AM/PM goes with it (#53).
func TestAChosenDateColorIsKeptOverAPhoto(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	photo := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
	draw.Draw(photo, photo.Rect, image.NewUniform(color.RGBA{200, 200, 200, 255}), image.Point{}, draw.Src)

	draw1 := func() (*image.RGBA, image.Rectangle) {
		img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
		r := newRenderer(img)
		var at image.Rectangle
		r.readableOver(photo, walnut, slideshowWash, func() {
			at = r.timeAndDateAt(time.Date(2026, 9, 28, 14, 5, 0, 0, time.Local), r.h/2, "", 0, 0, false)
		})
		return img, at
	}

	img, at := draw1()
	if !has(img, at, photoGold) {
		t.Error("with no date color chosen, the date over a photo is not gold")
	}

	rose := dateColors[len(dateColors)-1]
	if err := config.Set().Screen().DateColor(rose.value); err != nil {
		t.Fatal(err)
	}
	img, at = draw1()
	if !has(img, at, rose.c) {
		t.Errorf("the chosen date color %s did not survive over a photo", rose.label)
	}
	if has(img, at, photoGold) {
		t.Error("the date still turned gold with a color chosen")
	}
}
