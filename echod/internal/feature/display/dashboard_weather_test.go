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
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The Dashboard's coming days sit at the foot of the screen, clear of the footer and above a timer, and
// leave the middle to a tap that starts Assist.
func TestDashboardWeatherAtTheFoot(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	at := time.Date(2026, 10, 6, 9, 57, 0, 0, time.Local)
	var week []hass.Day
	for i := range 5 {
		week = append(week, hass.Day{When: at.AddDate(0, 0, i), Condition: "sunny", High: 86, Low: 64})
	}
	facts := styleFacts{kind: styleDashboard, chosen: true, days: week}
	sky := home.Weather{Condition: "sunny", Temp: "72°"}
	running := []timer.Countdown{{Name: "Pasta", Left: 4 * time.Minute, Total: 10 * time.Minute, Active: true}}
	for _, size := range []image.Point{{showWide, showHigh}, {show8Wide, show8High}} {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, size.X, size.Y)))
		r.draw(scene{now: at, phase: "idle", weather: sky, style: facts})
		r.weatherMu.Lock()
		row := r.weatherAt
		r.weatherMu.Unlock()
		if foot := r.h - r.s(50); row.Max.Y != foot {
			t.Errorf("%v: the coming days end at %d, not the foot at %d", size, row.Max.Y, foot)
		}
		middle := image.Pt(r.w/2, row.Min.Y-r.s(20))
		if r.weatherTapped(middle) || r.dateTapped(middle) {
			t.Errorf("%v: a tap at %v above the coming days was taken", size, middle)
		}

		r.draw(scene{now: at, phase: "idle", weather: sky, style: facts, timers: running})
		r.weatherMu.Lock()
		withTimer := r.weatherAt
		r.weatherMu.Unlock()
		if withTimer.Empty() || withTimer.Max.Y >= row.Max.Y {
			t.Errorf("%v: with a timer the coming days are at %v, not above it", size, withTimer)
		}
	}
}
