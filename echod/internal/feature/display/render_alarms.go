//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/draw"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// tallerBy is how much taller the panel is than the Show 5's shape at its width, in its own pixels:
// nothing on a Show 5, 160 on a Show 8, 120 on the Skylight. Pages composed for the Show 5's 2:1 use
// it to sit in the middle of a taller panel instead of at its top.
func (r *renderer) tallerBy() int { return max(r.h-r.s(480), 0) }

// ringClockBase is the baseline of the ringing page's time, in the Show 5's pixels.
const ringClockBase = 284

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
	// The page is laid out for the Show 5's shape, its words from the top and its buttons from the foot.
	// A panel taller for its width (the Show 8, a tablet) left the height it has over as a gap between
	// the two: half of it goes above the words instead, so they sit in the middle of what is theirs.
	down := r.tallerBy() / 2
	r.text(r.title, title, (r.w-r.width(r.title, title))/2, down+r.s(80), amber)

	hour := clockHM(s.now)
	ampm := clockSuffix(s.now)
	gap := r.s(18)
	if ampm == "" {
		gap = 0
	}
	hw, aw := r.width(r.ringClock, hour), r.width(r.ringAmpm, ampm)
	x := (r.w - hw - gap - aw) / 2
	// The digits stand a little nearer the title than the buttons: the gap under them is where a
	// silenced ring says so, and on a Show 5 that line needs all of it.
	r.text(r.ringClock, hour, x, down+r.s(ringClockBase), cream)
	r.text(r.ringAmpm, ampm, x+hw+gap, down+r.s(ringClockBase), amber)

	// A silenced ring looks exactly like one that stopped, and it is not stopped: it waits a few
	// seconds for an answer. So say both things: that it is silent, and what snoozes it. A press of
	// a volume button or the microphone button quiets a ring, and so does the wake word; only a
	// second press of one of those buttons snoozes it, so the note names them instead of saying
	// "again" to somebody who spoke.
	//
	// It sits in the middle of the gap between the digits and the buttons. Above the clock it
	// touched the tops of the digits on a Show 5, and read as part of the title.
	if st.silenced {
		note := tr("silenced")
		if st.snoozable {
			note = fmt.Sprintf(tr("silenced_press_a_button_to_snooze_d_min"), s.snooze)
		}
		note = clipText(r, r.tiny, note, r.w-2*r.margin)
		top, _ := r.actionBand()
		m := r.tiny.Metrics()
		base := (down+r.s(ringClockBase)+top)/2 + (m.Ascent.Ceil()-m.Descent.Ceil())/2
		r.text(r.tiny, note, (r.w-r.width(r.tiny, note))/2, base, cream)
	}

	left, right := r.actionHalves()
	rad := float64(r.s(actionRadius))
	mid := (left.Min.Y + left.Max.Y) / 2
	stop := image.Rect(left.Min.X, left.Min.Y, right.Max.X, left.Max.Y)
	// A finger shows on the button its tap would be: Snooze only on Snooze, and Stop from anywhere else
	// on the page (ringSnoozeAt).
	snoozing := r.press.on && ringSnoozeAt(r.press.at.X, r.w, st.snoozable, r.actionDecided(r.press.at.Y))
	if st.snoozable {
		stop = left
		label := fmt.Sprintf(tr("snooze_d_min"), s.snooze)
		fg := r.buttonFacePressed(right, rad, btnSecondary, snoozing)
		r.text(r.body, label, right.Min.X+(right.Dx()-r.width(r.body, label))/2, mid+r.s(14), fg)
	}
	fg := r.buttonFacePressed(stop, rad, btnPrimary, r.press.on && !snoozing)
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
