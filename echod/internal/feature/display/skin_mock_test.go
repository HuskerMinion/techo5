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

// Mockups of each skin over the clock styles, with a timer running and without, as the panel would be
// sent them: drawn, then through the skin's filter (MOCK_OUT).
func TestMockSkins(t *testing.T) {
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
	for _, sk := range skins[1:] {
		if err := config.Set().Screen().Skin(sk.value); err != nil {
			t.Fatal(err)
		}
		for _, panel := range []struct {
			name       string
			wide, high int
		}{{"show5", showWide, showHigh}, {"show8", show8Wide, show8High}} {
			var tiles []mockTile
			for _, st := range clockStyles {
				if err := config.Set().Screen().ClockStyle(st.value); err != nil {
					t.Fatal(err)
				}
				for _, timers := range []bool{false, true} {
					img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
					s := scene{now: at, phase: "idle", weather: home.Weather{Condition: "partlycloudy", Temp: "72°"}, style: facts}
					label := st.label
					if timers {
						s.timers = []timer.Countdown{{Name: "Pasta", Left: 272 * time.Second, Total: 600 * time.Second, Active: true}}
						label += " / timer"
					}
					applyTheme(current())
					applySkin(nil, 0, 0)
					newRenderer(img).draw(s)
					f := skinFilterFor(panel.wide, panel.high)
					for y := 0; f != nil && y < panel.high; y++ {
						f(img.Pix[y*img.Stride:y*img.Stride+panel.wide*4], y)
					}
					if !timers {
						mockSave(t, out, sk.value+"-"+panel.name+"-"+st.label, img)
					}
					tiles = append(tiles, mockTile{label, img})
				}
			}
			mockSheet(t, out, sk.value+"-"+panel.name+"-sheet", tiles, 4)
		}
	}
}
