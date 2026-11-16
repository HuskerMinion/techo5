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

// moveBox is the box that holds every pixel in which a and b differ; empty when they are the same.
func moveBox(a, b *image.RGBA) image.Rectangle {
	var box image.Rectangle
	for y := a.Rect.Min.Y; y < a.Rect.Max.Y; y++ {
		for x := a.Rect.Min.X; x < a.Rect.Max.X; x++ {
			if a.RGBAAt(x, y) != b.RGBAAt(x, y) {
				box = box.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return box
}

// The screensaver's clock can be put at the top, in the middle or along the foot, and the photo's date and
// place go to the other side from it (#573). The top is how it always was. With SHOW_PREVIEW set, the
// looks are written there.
func TestTheScreensaverClockPosition(t *testing.T) {
	at := time.Date(2026, 10, 17, 9, 41, 0, 0, time.Local)
	caps := []home.PhotoCaption{{When: time.Date(2019, 6, 15, 14, 30, 0, 0, time.UTC), Place: "Springfield, Ohio"}}
	dir := os.Getenv("SHOW_PREVIEW")
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}} {
		photo := testPhoto("sky", panel.wide, panel.high)
		draw := func(overlay, pos string, weather, caption bool) *image.RGBA {
			s := scene{now: at, phase: "idle", slideshowScreensaver: photo, slideshowOverlay: overlay, slideshowClockAt: pos,
				weather: home.Weather{Temp: "72°", Condition: "partlycloudy"}, saverWeather: weather}
			if caption {
				s.slideshowCaptions = caps
			}
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			newRenderer(img).draw(s)
			return img
		}
		for _, overlay := range []string{config.SlideshowOverlaySmall, config.SlideshowOverlayMedium, ""} {
			size := map[string]string{config.SlideshowOverlaySmall: "small", config.SlideshowOverlayMedium: "medium", "": "clock"}[overlay]
			for _, withWeather := range []bool{false, true} {
				top := draw(overlay, "", withWeather, false)
				for _, pos := range []string{config.SlideshowAtCenter, config.SlideshowAtBottom} {
					got := draw(overlay, pos, withWeather, false)
					box := moveBox(top, got)
					if box.Empty() && !(overlay == "" && pos == config.SlideshowAtCenter) {
						t.Errorf("%s%s %s (weather %v): the clock did not move", size, panel.name, pos, withWeather)
					}
					if pos == config.SlideshowAtBottom && box.Max.Y <= panel.high*3/4 {
						// The clock is along the foot: what moved reaches well down the screen.
						t.Errorf("%s%s: the clock at the bottom ends at %d of %d", size, panel.name, box.Max.Y, panel.high)
					}
				}
			}
			// The date and place are on the far side from the clock.
			for pos, wantLow := range map[string]bool{"": true, config.SlideshowAtCenter: true, config.SlideshowAtBottom: false} {
				box := moveBox(draw(overlay, pos, false, false), draw(overlay, pos, false, true))
				if box.Empty() {
					t.Errorf("%s%s %q: no date and place", size, panel.name, pos)
					continue
				}
				if low := box.Min.Y >= panel.high/2; low != wantLow {
					t.Errorf("%s%s %q: the date and place are at %v, low is %v want %v", size, panel.name, pos, box, low, wantLow)
				}
			}
		}
		if dir == "" {
			continue
		}
		for _, p := range []struct {
			name, overlay, pos string
			weather            bool
		}{
			{"saver-small-bottom", config.SlideshowOverlaySmall, config.SlideshowAtBottom, false},
			{"saver-small-center", config.SlideshowOverlaySmall, config.SlideshowAtCenter, false},
			{"saver-medium-bottom-weather", config.SlideshowOverlayMedium, config.SlideshowAtBottom, true},
			{"saver-medium-center", config.SlideshowOverlayMedium, config.SlideshowAtCenter, false},
			{"saver-clock-bottom-weather", "", config.SlideshowAtBottom, true},
		} {
			f, err := os.Create(filepath.Join(dir, p.name+panel.name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, draw(p.overlay, p.pos, p.weather, true)); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
}
