//go:build spot

package display

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The round screensaver's clock in each of its sizes, Medium among them, with the weather and without
// (#502). With SPOT_PREVIEW set, each is written there.
func TestTheRoundScreensaverClockSizesAndWeather(t *testing.T) {
	at := time.Date(2026, 10, 17, 9, 41, 0, 0, time.Local)
	weather := home.Weather{Temp: "72°", Condition: "partlycloudy"}
	photo := testPhoto("sky", side, side)
	looks := map[string][]byte{}
	for _, overlay := range []string{config.SlideshowOverlaySmall, config.SlideshowOverlayMedium, ""} {
		draw := func(withWeather bool) *image.RGBA {
			img := image.NewRGBA(image.Rect(0, 0, side, side))
			newRoundRenderer(img).draw(roundScene{now: at, phase: "idle", slideshowScreensaver: photo, slideshowOverlay: overlay,
				weather: weather, saverWeather: withWeather})
			return img
		}
		size := map[string]string{config.SlideshowOverlaySmall: "small", config.SlideshowOverlayMedium: "medium", "": "clock"}[overlay]
		plain, with := draw(false), draw(true)
		if bytes.Equal(plain.Pix, with.Pix) {
			t.Errorf("%s: the weather changed nothing", size)
		}
		for other, pix := range looks {
			if bytes.Equal(pix, plain.Pix) {
				t.Errorf("%s looks the same as %s", size, other)
			}
		}
		looks[size] = plain.Pix
		dir := os.Getenv("SPOT_PREVIEW")
		if dir == "" {
			continue
		}
		for name, img := range map[string]*image.RGBA{"saver-spot-" + size: plain, "saver-spot-" + size + "-weather": with} {
			f, err := os.Create(filepath.Join(dir, name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, img); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
}
