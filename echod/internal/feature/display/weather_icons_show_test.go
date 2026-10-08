//go:build !dot && !spot

package display

import (
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The weather icons that have a sun in them by day, beside their night forms, at the sizes the clock
// and the weather page draw them. With SHOW_PREVIEW set, written there as weather-icons-night.png:
// the top row by day, the bottom by night.
func TestShowNightIconsPreview(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
	draw.Draw(img, img.Bounds(), image.NewUniform(walnut), image.Point{}, draw.Src)
	r := newRenderer(img)
	for i, pair := range [][2]string{{"sunny", "clear-night"}, {"partlycloudy", home.PartlyCloudyNight}} {
		x := 140 + i*420
		r.weatherIcon(pair[0], x, 120, 150)
		r.weatherIcon(pair[0], x+170, 120, weatherMark)
		r.weatherIcon(pair[1], x, 340, 150)
		r.weatherIcon(pair[1], x+170, 340, weatherMark)
	}
	dir := os.Getenv("SHOW_PREVIEW")
	if dir == "" {
		return
	}
	f, err := os.Create(filepath.Join(dir, "weather-icons-night.png"))
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
