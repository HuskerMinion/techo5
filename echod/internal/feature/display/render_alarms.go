//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/draw"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// clockTime is an hour and minute the way the clock shows the time.
func clockTime(hour, minute int, time24h bool) string {
	if time24h {
		return fmt.Sprintf("%02d:%02d", hour, minute)
	}
	return time.Date(2000, 1, 1, hour, minute, 0, 0, time.UTC).Format("3:04 PM")
}

// countdown is a timer's time left: 4:05, or 1:02:05 past an hour.
func countdown(left time.Duration) string {
	secs := int(left.Round(time.Second).Seconds())
	if secs >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", secs/3600, secs/60%60, secs%60)
	}
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

// alarmsTab is the list of alarms, or the editor while one is open.
func (r *renderer) alarmsTab(s scene) {
	if s.draft != nil {
		r.alarmEditor(s, *s.draft)
		return
	}
	alarmFmt := "3:04 PM"
	if s.time24h {
		alarmFmt = "15:04"
	}
	rows := alarmRows(s.alarms)
	start, end, more := pageOf(len(rows), s.sheet.page)
	for i, row := range rows[start:end] {
		switch {
		case row.snoozed != nil:
			top := r.row(i, "Snoozed until "+row.snoozed.At.Format(alarmFmt), amber)
			r.value(top, row.snoozed.Label, 1)
			r.button(top, 2, "Cancel", false)
		case row.local != nil:
			a := row.local
			label := clockTime(a.Hour, a.Minute, s.time24h)
			if a.Label != "" {
				label += "  " + a.Label
			}
			c := cream
			if !a.On {
				c = dim
			}
			top := r.row(i, label, c)
			r.value(top, config.DaysLabel(a.Days), 2)
			r.button(top, 1, "Edit", false)
			r.button(top, 2, onOff(a.On), a.On)
		case row.followed != nil:
			f := row.followed
			label := cmpOr(f.Label, f.Entity)
			when := "not set in Home Assistant"
			if !f.At.IsZero() {
				when = f.At.Format(alarmFmt) + " · from Home Assistant"
			}
			if !f.Armed {
				when = "off in Home Assistant"
			}
			c := cream
			if !f.Armed || f.At.IsZero() {
				c = dim
			}
			top := r.row(i, label, c)
			r.value(top, when, 0)
		case row.add:
			top := r.row(i, "Add an alarm", cream)
			r.value(top, "rings without Home Assistant", 1)
			r.button(top, 2, "Add", false)
		}
	}
	if more {
		r.moreRow(len(rows), s.sheet.page)
	}
	if n := end - start; !more && n < sheetRows-1 {
		r.note(n, "Home Assistant can set them too: alarm_set, alarm_delete, and alarms_follow for its own helpers.")
	}
}

// alarmEditor is one alarm being set: hour, minute, days, then save, delete and back.
func (r *renderer) alarmEditor(s scene, d alarmDraft) {
	a := d.alarm
	top := r.row(editRowHour, "Hour", cream)
	r.value(top, clockTime(a.Hour, a.Minute, s.time24h), 2)
	r.button(top, 1, "−", false)
	r.button(top, 2, "+", false)

	top = r.row(editRowMinute, "Minute", cream)
	r.value(top, fmt.Sprintf(":%02d", a.Minute), 2)
	r.button(top, 1, "−", false)
	r.button(top, 2, "+", false)

	top = r.row(editRowDays, "Days", cream)
	x0, w := r.dayChips()
	for i, name := range []string{"S", "M", "T", "W", "T", "F", "S"} {
		on := a.Days&(1<<i) != 0
		rect := image.Rect(x0+i*w+4, top+(sheetRowHeight-buttonH)/2, x0+(i+1)*w-4, top+(sheetRowHeight+buttonH)/2)
		fill, ink := shift(ember, 12), cream
		if on {
			fill, ink = amber, walnut
		}
		r.bevel(rect, fill, true)
		r.text(r.tiny, name, rect.Min.X+(rect.Dx()-r.width(r.tiny, name))/2, rect.Min.Y+29, ink)
	}

	top = r.row(editRowRepeat, "Repeat", cream)
	r.value(top, config.DaysLabel(a.Days), 1)
	r.button(top, 2, "Next", false)

	top = r.row(editRowSave, "Save", cream)
	if d.isNew {
		r.value(top, "a new alarm, on", 1)
	} else {
		r.value(top, "keeps it on", 1)
	}
	r.button(top, 2, "Save", true)

	top = r.row(editRowDelete, map[bool]string{true: "Cancel", false: "Delete"}[d.isNew], cream)
	switch {
	case d.isNew:
		r.value(top, "leave without saving", 1)
		r.button(top, 2, "Cancel", false)
	case !d.deleteArm.IsZero() && s.now.Sub(d.deleteArm) < restartWindow:
		r.value(top, "tap again to delete it", 1)
		r.button(top, 2, "Confirm", true)
	default:
		r.value(top, "asks twice", 1)
		r.button(top, 2, "Delete", false)
	}

	if !d.isNew {
		top = r.row(editRowBack, "Back", cream)
		r.value(top, "leave without saving", 1)
		r.button(top, 2, "Back", false)
	}
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

	hour := s.now.Format("3:04")
	ampm := s.now.Format("PM")
	gap := 18
	if s.time24h {
		hour = s.now.Format("15:04")
		ampm = ""
		gap = 0
	}
	hw, aw := r.width(r.clock, hour), r.width(r.ampm, ampm)
	x := (r.w - hw - gap - aw) / 2
	r.text(r.clock, hour, x, 290, cream)
	if ampm != "" {
		r.text(r.ampm, ampm, x+hw+gap, 290, amber)
	}

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
