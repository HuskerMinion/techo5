//go:build mockup && spot

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

// Mockups of every clock style on the Spot at each Clock position, over a stand-in photo (MOCK_OUT).
func TestMockSpotClockPlaces(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	out := os.Getenv("MOCK_OUT")
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local)
	var week []hass.Day
	for i, c := range []string{"partlycloudy", "rainy", "sunny"} {
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
	var tiles []mockTile
	for _, st := range clockStyles {
		if err := config.Set().Screen().ClockStyle(st.value); err != nil {
			t.Fatal(err)
		}
		for _, p := range []struct{ label, value string }{{"Center", ""}, {"Bottom", "bottom"}} {
			if err := config.Set().Screen().ClockPosition(p.value); err != nil {
				t.Fatal(err)
			}
			img := image.NewRGBA(image.Rect(0, 0, side, side))
			s := roundScene{now: at, phase: "idle", weather: home.Weather{Condition: "partlycloudy", Temp: "72°"}, style: facts,
				slideshow: mockPhoto(side, side)}
			newRoundRenderer(img).draw(s)
			mockSave(t, out, "spot-"+st.label+"-"+p.label, img)
			tiles = append(tiles, mockTile{st.label + " / " + p.label, img})
			img = image.NewRGBA(image.Rect(0, 0, side, side))
			s.timers = []timer.Countdown{{Name: "pasta", Left: 272 * time.Second, Total: 600 * time.Second, Active: true}}
			newRoundRenderer(img).draw(s)
			mockSave(t, out, "spot-"+st.label+"-"+p.label+"-timer", img)
		}
	}
	mockSheet(t, out, "spot-sheet", tiles, 2)
}

// Mockups of the Nixie, Stack and Overlap styles on the Spot's plain ground, at 12 and 24 hours, the
// 24-hour one with a timer running and music playing (MOCK_OUT).
func TestMockSpotPlainStyles(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	t.Cleanup(func() { clock24.Store(false) })
	out := os.Getenv("MOCK_OUT")
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local)
	var tiles []mockTile
	for _, style := range []string{styleNixie, styleStack, styleOverlap} {
		if err := config.Set().Screen().ClockStyle(style); err != nil {
			t.Fatal(err)
		}
		for _, h24 := range []bool{false, true} {
			clock24.Store(h24)
			img := image.NewRGBA(image.Rect(0, 0, side, side))
			s := roundScene{now: at, phase: "idle", weather: home.Weather{Condition: "partlycloudy", Temp: "72°"}}
			name := "spot-" + style
			if h24 {
				s.now = at.Add(8 * time.Hour)
				s.timers = []timer.Countdown{{Name: "pasta", Left: 272 * time.Second, Total: 600 * time.Second, Active: true}}
				s.playing = true
				name += "-24h-timer"
			}
			newRoundRenderer(img).draw(s)
			mockSave(t, out, name, img)
			tiles = append(tiles, mockTile{name, img})
		}
	}
	mockSheet(t, out, "spot-plain-sheet", tiles, 2)
}
