//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/draw"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// ringingPage is over everything while a timer or an alarm sounds: what it is, the time, and buttons
// big enough to hit half awake.
func (r *renderer) ringingPage(s scene) {
	st := s.ring
	title := tr("alarm")
	switch {
	case st.timer != "" && st.alarm != nil:
		title = tr("timer_and_alarm")
	case st.timer != "":
		title = st.timer
		if !strings.Contains(strings.ToLower(title), "timer") {
			title += tr("timer_suffix")
		}
		title += tr("is_done")
	case st.alarm != nil && st.alarm.Label != "":
		title = st.alarm.Label
	}
	r.text(r.title, title, (r.w-r.width(r.title, title))/2, r.s(80), amber)

	// A silenced ring looks exactly like one that stopped, and it is not stopped: it comes back in a
	// moment unless it is answered. So say both things — that it is silent, and what the next press
	// will do with it.
	if st.silenced {
		note := tr("silenced")
		if st.snoozable {
			note = fmt.Sprintf(tr("silenced_press_again_to_snooze_d_min"), s.snooze)
		}
		// In the tiny face, on the one band that is clear: the title's descenders end around 92 and
		// the clock's digits begin around 122, which a 34pt line does not fit inside and this does.
		r.text(r.tiny, note, (r.w-r.width(r.tiny, note))/2, r.s(112), dim)
	}

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
		label := fmt.Sprintf(tr("snooze_d_min"), s.snooze)
		fg := r.buttonFace(right, rad, btnSecondary)
		r.text(r.body, label, right.Min.X+(right.Dx()-r.width(r.body, label))/2, mid+r.s(14), fg)
	}
	fg := r.buttonFace(stop, rad, btnPrimary)
	stopLabel := tr("stop")
	r.text(r.title, stopLabel, stop.Min.X+(stop.Dx()-r.width(r.title, stopLabel))/2, mid+r.s(16), fg)
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
	line := timer.LeftText(first.Left)
	if first.Name != "" {
		line = first.Name + "  " + line
	} else {
		line = tr("timer") + "  " + line
	}
	if n := len(running) - 1; n > 0 {
		line += fmt.Sprintf(tr("d_more"), n)
	}
	w := r.width(r.body, line)
	x := (r.w - w) / 2
	// A thin bar under it: how much of the soonest timer is left.
	r.text(r.body, line, x, y, amber)
	if first.Total > 0 {
		frac := float64(first.Left) / float64(first.Total)
		full := w
		y0, y1 := y+r.s(10), y+r.s(14)
		draw.Draw(r.dst, image.Rect(x, y0, x+full, y1), image.NewUniform(ember), image.Point{}, draw.Src)
		draw.Draw(r.dst, image.Rect(x, y0, x+int(float64(full)*frac), y1), image.NewUniform(amber), image.Point{}, draw.Src)
	}
}
