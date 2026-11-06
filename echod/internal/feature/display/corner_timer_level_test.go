//go:build !dot && !spot

package display

import (
	"image"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// The Corner timer sits on the weather's line when the weather is at the top left, rather than on the
// badges' row above it, which read as the timer being higher than the weather across the clock. With
// no weather to level with it keeps the badges' row.
func TestCornerTimerLevelsWithTheWeather(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	t.Cleanup(func() {
		_ = config.Set().Screen().TimerStyle("")
		_ = config.Set().Screen().WeatherPosition("")
	})
	if err := config.Set().Screen().TimerStyle(timerCorner); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Screen().WeatherPosition(""); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local)
	run := []timer.Countdown{{Name: "pasta", Left: 272 * time.Second, Total: 10 * time.Minute, Active: true}}
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"show5", showWide, showHigh}, {"show8", show8Wide, show8High}} {
		t.Run(panel.name, func(t *testing.T) {
			draw := func(withWeather bool) (int, *renderer) {
				img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
				r := newRenderer(img)
				s := scene{now: at, phase: "idle", timers: run}
				if withWeather {
					s.weather = home.Weather{Condition: "partlycloudy", Temp: "72°"}
				}
				r.draw(s)
				return firstRowDrawnIn(img, panel.wide/2, panel.wide), r
			}
			if got, r := draw(true); got != r.weatherMid()-r.s(40)/2 {
				t.Errorf("with the weather at the top left the Corner timer starts at row %d, the weather's line puts it at %d", got, r.weatherMid()-r.s(40)/2)
			}
			if got, r := draw(false); got != r.badgeTop() {
				t.Errorf("with no weather the Corner timer starts at row %d, the badges' row is %d", got, r.badgeTop())
			}
		})
	}
}

// firstRowDrawnIn is the topmost row between x0 and x1 that is not the ground: what the first thing
// in that part of the screen starts at.
func firstRowDrawnIn(img *image.RGBA, x0, x1 int) int {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := x0; x < x1; x++ {
			if img.RGBAAt(x, y) != walnut {
				return y
			}
		}
	}
	return -1
}
