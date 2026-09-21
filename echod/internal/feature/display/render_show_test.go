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
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The panel's own size, so a preview is what the device draws rather than something like it.
const (
	showWide = 960
	showHigh = 480
)

// Every scene draws without panicking; with SHOW_PREVIEW set to a directory, each is written there
// as a PNG to look at. The Spot has had this since its screen was built (render_spot_test.go); the
// Show never did, so a change to its layout was argued about in words.
func TestShowScenesDraw(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	sky := home.Weather{Condition: "partlycloudy", Temp: "72°"}
	var week []hass.Day
	for i, c := range []string{"partlycloudy", "rainy", "lightning-rainy", "sunny", "snowy", "cloudy"} {
		week = append(week, hass.Day{When: at.AddDate(0, 0, i), Condition: c, High: float64(78 - 3*i), Low: float64(55 - 2*i), Rain: 10 * i})
	}

	scenes := map[string]scene{
		"clock":            {now: at, phase: "idle", weather: sky},
		"clock-sunny":      {now: at, phase: "idle", weather: home.Weather{Condition: "sunny", Temp: "88°"}},
		"clock-rainy":      {now: at, phase: "idle", weather: home.Weather{Condition: "rainy", Temp: "54°"}},
		"clock-night":      {now: at, phase: "idle", weather: home.Weather{Condition: "clear-night", Temp: "58°"}},
		"clock-no-weather": {now: at, phase: "idle"},
		"nowplaying": {now: at, phase: "idle", nowPlaying: true, playing: true, weather: sky,
			radio: home.Radio{Now: "KXYZ 101.1", Title: "Take It Easy", Artist: "Eagles"}},
		"weather": {now: at, phase: "idle", weather: sky, forecast: week, showWeather: true},
		"settings-sound": {now: at, phase: "idle", showSheet: true,
			sheet: settings{cat: catSound, volume: 15, wakeWord: "Okay Nabu"}},
		"settings-sound-tone": {now: at, phase: "idle", showSheet: true,
			sheet: settings{cat: catSound, volume: 15, wakeWord: "Okay Nabu", cardScroll: 210}},
	}

	dir := os.Getenv("SHOW_PREVIEW")
	for name, s := range scenes {
		img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
		newRenderer(img).draw(s)
		if dir == "" {
			continue
		}
		f, err := os.Create(filepath.Join(dir, name+".png"))
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
}
