//go:build !dot && !spot

package display

import (
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// alarmDraft is an alarm being set in the Alarms card's editor: a new one, or a copy of one being changed.
type alarmDraft struct {
	alarm     config.Alarm
	isNew     bool
	deleteArm time.Time // the first of the two taps Delete wants
}

// ringState is what is ringing, for the ringing page.
type ringState struct {
	timer     string // name of a ringing timer
	alarm     *alarm.Ring
	preview   bool
	snoozable bool
}

func (r ringState) any() bool { return r.timer != "" || r.alarm != nil || r.preview }

// ringing reads what is sounding now.
func (d *Display) ringing(now time.Time) ringState {
	var st ringState
	if name, ok := timer.Get().RingingName(); ok {
		st.timer = name
	}
	st.alarm = alarm.Get().View(now).Ringing
	st.snoozable = st.alarm != nil
	d.mu.Lock()
	if now.Before(d.ringPreview) && !st.any() {
		st.preview, st.snoozable = true, true
		st.alarm = &alarm.Ring{Label: "Wake up", At: now}
	}
	d.mu.Unlock()
	return st
}

// PreviewRing shows the ringing page for a while with nothing sounding, to look at it.
func (d *Display) PreviewRing(for_ time.Duration) {
	d.mu.Lock()
	d.ringPreview = time.Now().Add(for_)
	d.mu.Unlock()
	d.wake()
}

// ringTap is a finger on the ringing page: Stop on the left half of the buttons, Snooze on the right
// when there is one to snooze.
func (d *Display) ringTap(x, y int, st ringState) {
	if d.r == nil || y < ringButtonsTop-20 {
		return
	}
	d.mu.Lock()
	d.ringPreview = time.Time{}
	d.mu.Unlock()
	if st.snoozable && x >= d.r.w/2 {
		if !alarm.Get().Snooze() {
			slog.Debug("snooze with nothing ringing")
		}
		timer.Get().Stop()
		return
	}
	timer.Get().Stop()
	alarm.Get().Stop()
}

// EditNewAlarm opens the alarm editor on a new alarm at the next whole hour.
func (d *Display) EditNewAlarm() {
	next := time.Now().Add(time.Hour)
	d.mu.Lock()
	d.draft = &alarmDraft{isNew: true, alarm: config.Alarm{Hour: next.Hour(), Minute: 0, Days: config.DaysOnce, On: true}}
	d.cat, d.picker, d.cardScroll = catAlarms, "", 0
	d.mu.Unlock()
	d.wake()
}

// alarmRows is the list the Alarms card shows, in order: the device's alarms, the helpers followed, and
// the row that adds one.
type alarmRow struct {
	snoozed  *alarm.Upcoming
	local    *config.Alarm
	followed *alarm.Followed
	add      bool
}

func alarmRows(v alarm.View) []alarmRow {
	var rows []alarmRow
	for i := range v.Snoozed {
		rows = append(rows, alarmRow{snoozed: &v.Snoozed[i]})
	}
	for i := range v.Local {
		rows = append(rows, alarmRow{local: &v.Local[i]})
	}
	for i := range v.Followed {
		rows = append(rows, alarmRow{followed: &v.Followed[i]})
	}
	return append(rows, alarmRow{add: true})
}

// repeats are the choices the Repeat row walks through.
var repeats = []uint8{config.DaysOnce, config.DaysEvery, config.DaysWeekdays, config.DaysWeekends}
