//go:build spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The weather icons that have a sun in them by day, beside their night forms, at the sizes the
// weather face, the clock and the forecast row draw them. With SPOT_PREVIEW set, written there as
// weather-icons-night.png: the top row by day, the bottom by night.
func TestSpotNightIconsPreview(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	r := newRoundRenderer(img)
	r.clear()
	for i, pair := range [][2]string{{"sunny", "clear-night"}, {"partlycloudy", home.PartlyCloudyNight}} {
		x := 150.0 + float64(i)*180
		r.weatherIcon(pair[0], x, 150, 44)
		r.weatherIcon(pair[0], x-40, 235, 11)
		r.weatherIcon(pair[0], x+40, 235, 16)
		r.weatherIcon(pair[1], x, 330, 44)
		r.weatherIcon(pair[1], x-40, 405, 11)
		r.weatherIcon(pair[1], x+40, 405, 16)
	}
	dir := os.Getenv("SPOT_PREVIEW")
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
