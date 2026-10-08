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
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// Every clock style draws its day, date and weather in each of the screen's languages, on both
// panels, with the longest date line there is (an alarm after it); with SHOW_PREVIEW set, each is
// written there to look at.
func TestClockInTheScreensLanguage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() {
		_ = config.Set().Screen().ClockStyle("")
		_ = config.Set().Screen().Language("")
	})
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local) // a Wednesday: the longest day names
	var week []hass.Day
	for i, c := range []string{"partlycloudy", "lightning-rainy", "sunny", "snowy-rainy", "pouring"} {
		week = append(week, hass.Day{When: at.AddDate(0, 0, i), Condition: c, High: float64(72 - 3*i), Low: float64(51 - 2*i)})
	}
	s := scene{now: at, phase: "idle", weather: home.Weather{Condition: "partlycloudy", Temp: "22°"},
		style: styleFacts{days: week}, alarms: alarm.View{Next: &alarm.Upcoming{At: at.Add(16 * time.Hour)}}}
	dir := os.Getenv("SHOW_PREVIEW")
	for _, lang := range []string{"de", "es", "fr", "it", "nl"} {
		if err := config.Set().Screen().Language(lang); err != nil {
			t.Fatal(err)
		}
		for _, st := range clockStyles {
			if err := config.Set().Screen().ClockStyle(st.value); err != nil {
				t.Fatal(err)
			}
			for _, panel := range []struct {
				name       string
				wide, high int
			}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}} {
				img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
				newRenderer(img).draw(s)
				if dir == "" || (lang != "it" && lang != "de") {
					continue
				}
				f, err := os.Create(filepath.Join(dir, "lang-"+lang+"-"+st.label+panel.name+".png"))
				if err != nil {
					t.Fatal(err)
				}
				png.Encode(f, img)
				f.Close()
			}
		}
	}
}
