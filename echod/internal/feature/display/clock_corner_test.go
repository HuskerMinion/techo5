//go:build !dot && !spot

package display

import (
	"image"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// At the foot, the date under a style stays on the screen and on its own side of it, or in the middle
// at Bottom center (beside the dial, for Analog), even with the next alarm after it making it as long
// as it gets.
func TestACornerDateStaysInItsCorner(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	t.Cleanup(func() {
		_ = config.Set().Screen().ClockStyle("")
		_ = config.Set().Screen().ClockPosition("")
	})
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local)
	s := scene{now: at, phase: "idle", weather: home.Weather{Condition: "partlycloudy", Temp: "72°"},
		style:  styleFacts{rise: at.Add(-7 * time.Hour), set: at.Add(5 * time.Hour), sunOK: true},
		alarms: alarm.View{Next: &alarm.Upcoming{At: at.Add(16 * time.Hour)}}}
	for _, st := range clockStyles {
		if !cornerStyle(st.value) {
			continue
		}
		if err := config.Set().Screen().ClockStyle(st.value); err != nil {
			t.Fatal(err)
		}
		for _, pos := range clockPositions {
			if pos.value == "" {
				continue
			}
			if err := config.Set().Screen().ClockPosition(pos.value); err != nil {
				t.Fatal(err)
			}
			for _, panel := range []struct {
				name       string
				wide, high int
			}{{"show5", showWide, showHigh}, {"show8", show8Wide, show8High}, {"skylight", skylightWide, skylightHigh}} {
				r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
				r.draw(s)
				if r.dateAt.Empty() {
					continue
				}
				date := r.dateAt.Inset(r.s(16))
				if date.Min.X < 0 || date.Max.X > panel.wide {
					t.Errorf("%s %s %s: the date runs off the screen, x %d to %d of %d",
						panel.name, st.label, pos.label, date.Min.X, date.Max.X, panel.wide)
				}
				if pos.value == "bottom-left" && date.Max.X > panel.wide*55/100 ||
					pos.value == "bottom-right" && date.Min.X < panel.wide*45/100 {
					t.Errorf("%s %s %s: the date runs over the middle, x %d to %d of %d",
						panel.name, st.label, pos.label, date.Min.X, date.Max.X, panel.wide)
				}
				if pos.value == "bottom-center" && (date.Min.X < panel.wide*20/100 ||
					date.Max.X > panel.wide*80/100 || date.Max.Y < panel.high/2) {
					t.Errorf("%s %s %s: the date is not in the middle at the foot, %v of %dx%d",
						panel.name, st.label, pos.label, date, panel.wide, panel.high)
				}
			}
		}
	}
}
