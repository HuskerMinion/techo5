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
)

// Mockups of every timer style on the Classic, Big and Analog clocks, with one timer and with three
// (MOCK_OUT).
func TestMockTimerStyles(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	out := os.Getenv("MOCK_OUT")
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local)
	one := []timer.Countdown{{Name: "Pasta", Left: 272 * time.Second, Total: 600 * time.Second, Active: true}}
	three := append(one,
		timer.Countdown{Name: "Laundry", Left: 31*time.Minute + 5*time.Second, Total: 45 * time.Minute, Active: true},
		timer.Countdown{Name: "Bread", Left: 52 * time.Minute, Total: time.Hour, Active: true})
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"show5", showWide, showHigh}, {"show8", show8Wide, show8High}} {
		var tiles []mockTile
		for _, ts := range timerStyles {
			if err := config.Set().Screen().TimerStyle(ts.value); err != nil {
				t.Fatal(err)
			}
			for _, cs := range []string{styleClassic, styleBig, styleAnalog} {
				if err := config.Set().Screen().ClockStyle(cs); err != nil {
					t.Fatal(err)
				}
				for _, run := range [][]timer.Countdown{one, three} {
					img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
					s := scene{now: at, phase: "idle", weather: home.Weather{Condition: "partlycloudy", Temp: "72°"}, timers: run}
					newRenderer(img).draw(s)
					label := ts.label + " / " + clockStyles[clockStyleIndex()].label
					if len(run) > 1 {
						label += " / 3 timers"
					}
					mockSave(t, out, panel.name+"-"+ts.label+"-"+clockStyles[clockStyleIndex()].label+"-"+itoa(len(run)), img)
					tiles = append(tiles, mockTile{label, img})
				}
			}
		}
		mockSheet(t, out, panel.name+"-timers-sheet", tiles, 6)
	}
}
