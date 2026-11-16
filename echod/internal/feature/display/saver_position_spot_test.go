//go:build spot

package display

import (
	"image"
	"image/png"
	"math"
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

// The round screensaver's clock can be at the top, in the middle or low on the face, and the photo's date
// and place go to the other side from it, all inside the circle (#573). The top is how it always was. With
// SPOT_PREVIEW set, the looks are written there.
func TestTheRoundScreensaverClockPosition(t *testing.T) {
	at := time.Date(2026, 10, 17, 9, 41, 0, 0, time.Local)
	caps := []home.PhotoCaption{{When: time.Date(2019, 6, 15, 14, 30, 0, 0, time.UTC), Place: "Springfield, Ohio"}}
	photo := testPhoto("sky", side, side)
	draw := func(overlay, pos string, weather, caption bool) *image.RGBA {
		s := roundScene{now: at, phase: "idle", slideshowScreensaver: photo, slideshowOverlay: overlay, slideshowClockAt: pos,
			weather: home.Weather{Temp: "72°", Condition: "partlycloudy"}, saverWeather: weather}
		if caption {
			s.slideshowCaptions = caps
		}
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		newRoundRenderer(img).draw(s)
		return img
	}
	// within is whether every pixel in which a and b differ is inside the face's circle.
	within := func(a, b *image.RGBA) bool {
		r := float64(side / 2)
		for y := 0; y < side; y++ {
			for x := 0; x < side; x++ {
				if a.RGBAAt(x, y) != b.RGBAAt(x, y) && math.Hypot(float64(x)-r, float64(y)-r) > r {
					return false
				}
			}
		}
		return true
	}
	dir := os.Getenv("SPOT_PREVIEW")
	for _, overlay := range []string{config.SlideshowOverlaySmall, config.SlideshowOverlayMedium, ""} {
		size := map[string]string{config.SlideshowOverlaySmall: "small", config.SlideshowOverlayMedium: "medium", "": "clock"}[overlay]
		for _, withWeather := range []bool{false, true} {
			top := draw(overlay, "", withWeather, false)
			for _, pos := range []string{config.SlideshowAtCenter, config.SlideshowAtBottom} {
				box := moveBox(top, draw(overlay, pos, withWeather, false))
				if box.Empty() && !(overlay == "" && pos == config.SlideshowAtCenter) {
					t.Errorf("%s %s (weather %v): the clock did not move", size, pos, withWeather)
				}
				if pos == config.SlideshowAtBottom && box.Max.Y <= side*3/4 {
					t.Errorf("%s: the clock at the bottom ends at %d of %d", size, box.Max.Y, side)
				}
			}
		}
		// The date and place are on the far side from the clock, and inside the circle.
		for pos, wantLow := range map[string]bool{"": true, config.SlideshowAtCenter: true, config.SlideshowAtBottom: false} {
			box := moveBox(draw(overlay, pos, false, false), draw(overlay, pos, false, true))
			if box.Empty() {
				t.Errorf("%s %q: no date and place", size, pos)
				continue
			}
			if low := box.Min.Y >= side/2; low != wantLow {
				t.Errorf("%s %q: the date and place are at %v, low is %v want %v", size, pos, box, low, wantLow)
			}
			if !within(draw(overlay, pos, false, false), draw(overlay, pos, false, true)) {
				t.Errorf("%s %q: the date and place at %v reach outside the circle", size, pos, box)
			}
		}
		// The clock, with its weather, is inside the circle in every place.
		// What changes with another time and day, and with the weather taken away, is the clock's own ink.
		for _, pos := range []string{"", config.SlideshowAtCenter, config.SlideshowAtBottom} {
			later := roundScene{now: at.Add(25*time.Hour + 17*time.Minute), phase: "idle", slideshowScreensaver: photo, slideshowOverlay: overlay, slideshowClockAt: pos,
				weather: home.Weather{Temp: "72°", Condition: "partlycloudy"}, saverWeather: true}
			img := image.NewRGBA(image.Rect(0, 0, side, side))
			newRoundRenderer(img).draw(later)
			now, bare := draw(overlay, pos, true, false), draw(overlay, pos, false, false)
			if !within(now, img) || !within(now, bare) {
				t.Errorf("%s %q: the clock reaches outside the circle", size, pos)
			}
		}
	}
	if dir == "" {
		return
	}
	for _, p := range []struct {
		name, overlay, pos string
		weather            bool
	}{
		{"saver-spot-small-bottom", config.SlideshowOverlaySmall, config.SlideshowAtBottom, false},
		{"saver-spot-small-center", config.SlideshowOverlaySmall, config.SlideshowAtCenter, false},
		{"saver-spot-medium-bottom-weather", config.SlideshowOverlayMedium, config.SlideshowAtBottom, true},
		{"saver-spot-medium-center", config.SlideshowOverlayMedium, config.SlideshowAtCenter, false},
		{"saver-spot-clock-bottom-weather", "", config.SlideshowAtBottom, true},
	} {
		f, err := os.Create(filepath.Join(dir, p.name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, draw(p.overlay, p.pos, p.weather, true)); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}
