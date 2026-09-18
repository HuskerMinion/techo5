//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/draw"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// clockTime is an hour and minute the way the clock shows the time.
func clockTime(hour, minute int) string {
	return clockText(time.Date(2000, 1, 1, hour, minute, 0, 0, time.UTC))
}

// countdown is a timer's time left: 4:05, or 1:02:05 past an hour.
func countdown(left time.Duration) string {
	secs := int(left.Round(time.Second).Seconds())
	if secs >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", secs/3600, secs/60%60, secs%60)
	}
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

// ringButtonsTop is where the ringing page's buttons start.
const ringButtonsTop = 330

// ringingPage is over everything while a timer or an alarm sounds: what it is, the time, and buttons
// big enough to hit half awake.
func (r *renderer) ringingPage(s scene) {
	st := s.ring
	title := "Alarm"
	switch {
	case st.timer != "" && st.alarm != nil:
		title = "Timer and alarm"
	case st.timer != "":
		title = st.timer
		if !strings.Contains(strings.ToLower(title), "timer") {
			title += " timer"
		}
		title += " is done"
	case st.alarm != nil && st.alarm.Label != "":
		title = st.alarm.Label
	}
	r.text(r.title, title, (r.w-r.width(r.title, title))/2, 80, amber)

	hour := clockHM(s.now)
	ampm := clockSuffix(s.now)
	gap := 18
	if ampm == "" {
		gap = 0
	}
	hw, aw := r.width(r.clock, hour), r.width(r.ampm, ampm)
	x := (r.w - hw - gap - aw) / 2
	r.text(r.clock, hour, x, 290, cream)
	r.text(r.ampm, ampm, x+hw+gap, 290, amber)

	y0, y1 := ringButtonsTop, r.h-30
	stop := image.Rect(r.margin, y0, r.w-r.margin, y1)
	if st.snoozable {
		stop.Max.X = r.w/2 - 12
		snooze := image.Rect(r.w/2+12, y0, r.w-r.margin, y1)
		r.bevel(snooze, shift(ember, 16), true)
		label := fmt.Sprintf("Snooze %d min", s.snooze)
		r.text(r.body, label, snooze.Min.X+(snooze.Dx()-r.width(r.body, label))/2, y0+72, cream)
	}
	r.bevel(stop, amber, true)
	r.text(r.title, "Stop", stop.Min.X+(stop.Dx()-r.width(r.title, "Stop"))/2, y0+76, walnut)
}

// timersLine is under the date on the clock while timers run: the soonest, and how many more.
func (r *renderer) timersLine(s scene, y int) {
	var running []timer.Countdown
	for _, t := range s.timers {
		if t.Active {
			running = append(running, t)
		}
	}
	if len(running) == 0 {
		return
	}
	first := running[0]
	line := countdown(first.Left)
	if first.Name != "" {
		line = first.Name + "  " + line
	} else {
		line = "Timer  " + line
	}
	if n := len(running) - 1; n > 0 {
		line += fmt.Sprintf("   +%d more", n)
	}
	w := r.width(r.body, line)
	x := (r.w - w) / 2
	// A thin bar under it: how much of the soonest timer is left.
	r.text(r.body, line, x, y, amber)
	if first.Total > 0 {
		frac := float64(first.Left) / float64(first.Total)
		full := w
		draw.Draw(r.dst, image.Rect(x, y+10, x+full, y+14), image.NewUniform(ember), image.Point{}, draw.Src)
		draw.Draw(r.dst, image.Rect(x, y+10, x+int(float64(full)*frac), y+14), image.NewUniform(amber), image.Point{}, draw.Src)
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
