//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The Sun clock draws its arc from the sensors' day; with SHOW_PREVIEW set, it is written there to look
// at beside the worked-out day's style-Sun.png.
func TestShowSunClockFromSensors(t *testing.T) {
	withSunSensors(t)
	if err := setStyleForTest(t, styleSun); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local)
	s := scene{now: at, phase: "idle", weather: home.Weather{Condition: "partlycloudy", Temp: "72°"}, style: styleFactsFor(styleSun, at)}
	img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
	newRenderer(img).draw(s)
	if dir := os.Getenv("SHOW_PREVIEW"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "style-Sun-sensors.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}
}
