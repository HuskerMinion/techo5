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

		// With a timer, the weather stays above it: on a Show 8 the row moves up, and on a Show 5, where the
		// row no longer fits under the clock, today's weather on a line takes its place.
		r.draw(scene{now: at, phase: "idle", weather: sky, style: facts, timers: running})
		r.weatherMu.Lock()
		withTimer := r.weatherAt
		r.weatherMu.Unlock()
		if withTimer.Empty() || withTimer.Max.Y >= row.Max.Y {
			t.Errorf("%v: with a timer the weather is at %v, not above it", size, withTimer)
		}
		if isRow := withTimer.Dy() == r.s(150); isRow != (size.X == show8Wide) {
			t.Errorf("%v: with a timer the weather drawn as a row is %v", size, isRow)
		}

		// With the Call button, the row narrows from both sides just enough that Today clears it.
		r.draw(scene{now: at, phase: "idle", weather: sky, style: facts, callButton: true})
		r.weatherMu.Lock()
		withCall := r.weatherAt
		r.weatherMu.Unlock()
		left, right := withCall.Min.X, withCall.Max.X
		if left == row.Min.X || left-row.Min.X != row.Max.X-right {
			t.Errorf("%v: with the Call button the row spans %d-%d, not narrowed evenly from %v", size, left, right, row)
		}
		cw := (right - left) / len(week)
		half := max(r.width(r.tiny, dayTemps(week[0])), r.s(44)) / 2
		b := r.callButtonRect()
		if todayLeft := left + cw/2 - half; todayLeft < b.Max.X+r.s(12) {
			t.Errorf("%v: Today starts at %d, under the Call button ending at %d", size, todayLeft, b.Max.X)
		}
		if todayLeft := left + cw/2 - half; todayLeft > b.Max.X+r.s(12)+len(week) {
			t.Errorf("%v: Today starts at %d, further than it needs from the Call button ending at %d", size, todayLeft, b.Max.X)
		}
	}
}
