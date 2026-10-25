//go:build mockup && !dot && !spot

package display

import (
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// Mockups of every clock style at each Clock position, over a stand-in photo (MOCK_OUT).
func TestMockClockCorners(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	out := os.Getenv("MOCK_OUT")
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local)
	var week []hass.Day
	for i, c := range []string{"partlycloudy", "rainy", "sunny", "cloudy", "snowy"} {
		week = append(week, hass.Day{When: at.AddDate(0, 0, i), Condition: c, High: float64(72 - 3*i), Low: float64(51 - 2*i)})
	}
	facts := styleFacts{
		rise: time.Date(2026, 9, 16, 6, 48, 0, 0, time.Local), set: time.Date(2026, 9, 16, 19, 5, 0, 0, time.Local), sunOK: true,
		days: week,
		next: []hass.Event{
			{Summary: "Dentist", Start: at.Add(83 * time.Minute), End: at.Add(143 * time.Minute)},
			{Summary: "Soccer practice", Start: at.Add(233 * time.Minute), End: at.Add(293 * time.Minute)},
		},
		places: worldPlaces(nil),
	}
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"show5", showWide, showHigh}, {"show8", show8Wide, show8High}, {"skylight", skylightWide, skylightHigh}} {
		var tiles []mockTile
		for _, st := range clockStyles {
			if err := config.Set().Screen().ClockStyle(st.value); err != nil {
				t.Fatal(err)
			}
			for _, pos := range clockPositions {
				if err := config.Set().Screen().ClockPosition(pos.value); err != nil {
					t.Fatal(err)
				}
				img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
				s := scene{now: at, phase: "idle", weather: home.Weather{Condition: "partlycloudy", Temp: "72°"}, style: facts,
					slideshow: mockPhoto(panel.wide, panel.high)}
				newRenderer(img).draw(s)
				name := panel.name + "-" + st.label + "-" + pos.label
				mockSave(t, out, name, img)
				tiles = append(tiles, mockTile{st.label + " / " + pos.label, img})
				// The same with a timer running, for comparing (not on the sheet).
				img = image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
				s.timers = []timer.Countdown{{Name: "pasta", Left: 272 * time.Second, Total: 600 * time.Second, Active: true}}
				newRenderer(img).draw(s)
				mockSave(t, out, name+"-timer", img)
			}
		}
		mockSheet(t, out, panel.name+"-sheet", tiles, len(clockPositions))
	}
}

// Mockups of the Nixie, Stack and Overlap styles on the plain ground, dark and light, at 12 and 24
// hours, in the middle and in a corner (MOCK_OUT): a sheet each.
func TestMockPlainStyles(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	keepPalette(t)
	t.Cleanup(func() { clock24.Store(false) })
	out := os.Getenv("MOCK_OUT")
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local)
	for _, style := range []string{styleNixie, styleStack, styleOverlap} {
		if err := config.Set().Screen().ClockStyle(style); err != nil {
			t.Fatal(err)
		}
		var tiles []mockTile
		for _, panel := range []struct {
			name       string
			wide, high int
		}{{"show5", showWide, showHigh}, {"show8", show8Wide, show8High}, {"skylight", skylightWide, skylightHigh}} {
			for _, theme := range []string{"", "Paper"} {
				applyTheme(themes[themeIndex(theme)])
				for _, pos := range []string{"", "bottom-right"} {
					if err := config.Set().Screen().ClockPosition(pos); err != nil {
						t.Fatal(err)
					}
					for _, h24 := range []bool{false, true} {
						clock24.Store(h24)
						img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
						s := scene{now: at, phase: "idle", weather: home.Weather{Condition: "partlycloudy", Temp: "72°"}}
						if h24 {
							s.now = at.Add(8 * time.Hour)
							s.timers = []timer.Countdown{{Name: "pasta", Left: 272 * time.Second, Total: 600 * time.Second, Active: true}}
						}
						newRenderer(img).draw(s)
						name := panel.name + "-" + style + map[string]string{"": "", "Paper": "-light"}[theme] + map[string]string{"": "", "bottom-right": "-corner"}[pos]
						if h24 {
							name += "-24h-timer"
						}
						mockSave(t, out, name, img)
						tiles = append(tiles, mockTile{name, img})
					}
				}
			}
		}
		mockSheet(t, out, style+"-sheet", tiles, 4)
	}
}

// Mockups of Weather position (MOCK_OUT): the Classic clock with each corner alone, with an alert and a
// Corner timer at the top right too, under the HAL 9000 skin's eye, and in the LCARS skin's frame; the
// Big style with the alert and timer, on each Show panel: a sheet.
func TestMockWeatherPosition(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	t.Cleanup(func() {
		_ = config.Set().Screen().WeatherPosition("")
		_ = config.Set().Screen().TimerStyle("")
		_ = config.Set().Screen().Skin("")
		_ = config.Set().Screen().ClockStyle("")
	})
	out := os.Getenv("MOCK_OUT")
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local)
	wind := home.Alert{ID: "a", Event: "Wind Advisory", Severity: "Moderate", Color: home.AlertColor("Wind Advisory", ""), Here: true, Ends: at.Add(time.Hour)}
	pasta := []timer.Countdown{{Name: "pasta", Left: 272 * time.Second, Total: 600 * time.Second, Active: true}}
	if err := config.Set().Screen().TimerStyle(timerCorner); err != nil {
		t.Fatal(err)
	}
	var tiles []mockTile
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"show5", showWide, showHigh}, {"show8", show8Wide, show8High}, {"skylight", skylightWide, skylightHigh}} {
		for _, pos := range weatherPositions {
			if err := config.Set().Screen().WeatherPosition(pos.value); err != nil {
				t.Fatal(err)
			}
			for _, v := range []struct {
				name, skin, style string
				alert             bool
			}{{"plain", "", "", false}, {"alert-timer", "", "", true}, {"hal-timer", skinHAL, "", false},
				{"lcars-alert-timer", skinLCARS, "", true}, {"big-alert-timer", "", styleBig, true}} {
				if err := config.Set().Screen().Skin(v.skin); err != nil {
					t.Fatal(err)
				}
				if err := config.Set().Screen().ClockStyle(v.style); err != nil {
					t.Fatal(err)
				}
				s := scene{now: at, phase: "idle", weather: home.Weather{Condition: "partlycloudy", Temp: "72°"}}
				if v.name != "plain" {
					s.timers = pasta
				}
				if v.alert {
					s.alerts = home.AlertView{Here: []home.Alert{wind}}
				}
				img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
				newRenderer(img).draw(s)
				name := panel.name + "-weather-" + pos.label + "-" + v.name
				mockSave(t, out, name, img)
				tiles = append(tiles, mockTile{name, img})
			}
		}
	}
	mockSheet(t, out, "weather-position-sheet", tiles, 5)

	// Every style on the Show 5 with the weather top right, the alert and the timer at the top left.
	if err := config.Set().Screen().WeatherPosition("top-right"); err != nil {
		t.Fatal(err)
	}
	_ = config.Set().Screen().Skin("")
	tiles = nil
	for _, st := range clockStyles {
		if err := config.Set().Screen().ClockStyle(st.value); err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
		newRenderer(img).draw(scene{now: at, phase: "idle", weather: home.Weather{Condition: "partlycloudy", Temp: "72°"},
			timers: pasta, alerts: home.AlertView{Here: []home.Alert{wind}}})
		tiles = append(tiles, mockTile{st.label, img})
	}
	img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
	_ = config.Set().Screen().ClockStyle("")
	newRenderer(img).draw(scene{now: at, phase: "idle", muted: true, weather: home.Weather{Condition: "partlycloudy", Temp: "72°"},
		timers: pasta, alerts: home.AlertView{Here: []home.Alert{wind}}})
	tiles = append(tiles, mockTile{"Classic, muted", img})
	mockSheet(t, out, "weather-right-styles-sheet", tiles, 4)
}
