//go:build !dot && !spot

package display

import (
	"image"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// Weather position Top right puts the weather in the top right corner of every style that has the
// weather corner, on every Show panel, and the alert badge goes beside it, clear of it and on the
// right half. Top left keeps the weather where it has always been.
func TestWeatherPositionTopRight(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	t.Cleanup(func() {
		_ = config.Set().Screen().ClockStyle("")
		_ = config.Set().Screen().WeatherPosition("")
	})
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local)
	wind := home.Alert{ID: "a", Event: "Wind Advisory", Severity: "Moderate", Color: home.AlertColor("Wind Advisory", ""), Here: true, Ends: at.Add(time.Hour)}
	for _, pos := range weatherPositions {
		if err := config.Set().Screen().WeatherPosition(pos.value); err != nil {
			t.Fatal(err)
		}
		for _, st := range []string{styleClassic, styleBig, styleFlip} {
			if err := config.Set().Screen().ClockStyle(st); err != nil {
				t.Fatal(err)
			}
			for _, panel := range []struct {
				name       string
				wide, high int
			}{{"show5", showWide, showHigh}, {"show8", show8Wide, show8High}, {"skylight", skylightWide, skylightHigh}} {
				r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
				r.draw(scene{now: at, phase: "idle", weather: home.Weather{Condition: "partlycloudy", Temp: "72°"},
					alerts: home.AlertView{Here: []home.Alert{wind}}})
				w, badge := r.weatherAt, r.badgeAt
				if w.Empty() || badge.Empty() {
					t.Fatalf("%s %s %s: weather %v, badge %v: both should be drawn", panel.name, st, pos.label, w, badge)
				}
				right := pos.value == "top-right"
				if right && w.Min.X < panel.wide/2 || !right && w.Max.X > panel.wide/2 {
					t.Errorf("%s %s %s: the weather is at %v on a %d-wide screen", panel.name, st, pos.label, w, panel.wide)
				}
				if w.Max.X > panel.wide || w.Min.X < 0 {
					t.Errorf("%s %s %s: the weather runs off the screen: %v", panel.name, st, pos.label, w)
				}
				if badge.Min.X < panel.wide/2 || right && badge.Overlaps(w.Inset(r.s(14))) {
					t.Errorf("%s %s %s: the alert badge is at %v, the weather at %v", panel.name, st, pos.label, badge, w)
				}
			}
		}
	}
}

// Muted with no alert, the header's "microphone off" pill shares the top row with the weather at the
// top right: the weather shortens to the temperature rather than run under it.
func TestWeatherTopRightClearsTheMicrophonePill(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	t.Cleanup(func() {
		_ = config.Set().Screen().ClockStyle("")
		_ = config.Set().Screen().WeatherPosition("")
	})
	if err := config.Set().Screen().WeatherPosition("top-right"); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local)
	for _, st := range []string{styleClassic, styleBig, styleFlip} {
		if err := config.Set().Screen().ClockStyle(st); err != nil {
			t.Fatal(err)
		}
		for _, panel := range []struct {
			name       string
			wide, high int
		}{{"show5", showWide, showHigh}, {"show8", show8Wide, show8High}, {"skylight", skylightWide, skylightHigh}} {
			r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
			r.draw(scene{now: at, phase: "idle", muted: true, weather: home.Weather{Condition: "partlycloudy", Temp: "72°"}})
			if w := r.weatherAt.Inset(r.s(14)); w.Empty() || w.Min.X < r.micPillRight() {
				t.Errorf("%s %s: the weather at %v runs under the pill, which ends at %d", panel.name, st, w, r.micPillRight())
			}
		}
	}
}
