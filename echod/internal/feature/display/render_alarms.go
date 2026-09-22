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

// countdown is a timer's time left: 4:05, or 1:02:05 past an hour.
func countdown(left time.Duration) string {
	secs := int(left.Round(time.Second).Seconds())
	if secs >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", secs/3600, secs/60%60, secs%60)
	}
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

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
	r.text(r.title, title, (r.w-r.width(r.title, title))/2, r.s(80), amber)

	hour := clockHM(s.now)
	ampm := clockSuffix(s.now)
	gap := r.s(18)
	if ampm == "" {
		gap = 0
	}
	hw, aw := r.width(r.clock, hour), r.width(r.ampm, ampm)
	x := (r.w - hw - gap - aw) / 2
	r.text(r.clock, hour, x, r.s(290), cream)
	r.text(r.ampm, ampm, x+hw+gap, r.s(290), amber)

	left, right := r.actionHalves()
	rad := float64(r.s(actionRadius))
	mid := (left.Min.Y + left.Max.Y) / 2
	stop := image.Rect(left.Min.X, left.Min.Y, right.Max.X, left.Max.Y)
	if st.snoozable {
		stop = left
		label := fmt.Sprintf("Snooze %d min", s.snooze)
		fg := r.buttonFace(right, rad, btnSecondary)
		r.text(r.body, label, right.Min.X+(right.Dx()-r.width(r.body, label))/2, mid+r.s(14), fg)
	}
	fg := r.buttonFace(stop, rad, btnPrimary)
	r.text(r.title, "Stop", stop.Min.X+(stop.Dx()-r.width(r.title, "Stop"))/2, mid+r.s(16), fg)
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
