//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The screensaver's clock in each of its sizes, Medium among them, with the weather and without (#502).
// The weather adds the sky and the temperature and nothing else; the three sizes are three looks. With
// SHOW_PREVIEW set, each is written there.
func TestTheScreensaverClockSizesAndWeather(t *testing.T) {
	at := time.Date(2026, 10, 17, 9, 41, 0, 0, time.Local)
	weather := home.Weather{Temp: "72°", Condition: "partlycloudy"}
	dir := os.Getenv("SHOW_PREVIEW")
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}} {
		photo := testPhoto("sky", panel.wide, panel.high)
		looks := map[string][]byte{}
		for _, overlay := range []string{config.SlideshowOverlaySmall, config.SlideshowOverlayMedium, ""} {
			draw := func(withWeather bool) *image.RGBA {
				img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
				newRenderer(img).draw(scene{now: at, phase: "idle", slideshowScreensaver: photo, slideshowOverlay: overlay,
					weather: weather, saverWeather: withWeather})
				return img
			}
			size := map[string]string{config.SlideshowOverlaySmall: "small", config.SlideshowOverlayMedium: "medium", "": "clock"}[overlay]
			plain, with := draw(false), draw(true)
			if same(plain.Pix, with.Pix) {
				t.Errorf("%s%s: the weather changed nothing", size, panel.name)
			}
			for other, pix := range looks {
				if same(pix, plain.Pix) {
					t.Errorf("%s%s looks the same as %s", size, panel.name, other)
				}
			}
			looks[size] = plain.Pix
			if dir == "" {
				continue
			}
			for name, img := range map[string]*image.RGBA{"saver-" + size + panel.name: plain, "saver-" + size + "-weather" + panel.name: with} {
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

	// Off is the photo alone, the weather too: it goes with the clock.
	photo := testPhoto("sky", show8Wide, show8High)
	off := func(withWeather bool) []byte {
		img := image.NewRGBA(image.Rect(0, 0, show8Wide, show8High))
		newRenderer(img).draw(scene{now: at, phase: "idle", slideshowScreensaver: photo, slideshowOverlay: config.SlideshowOverlayOff,
			weather: weather, saverWeather: withWeather})
		return img.Pix
	}
	if !same(off(false), off(true)) {
		t.Error("with the clock off the weather still showed")
	}
}
