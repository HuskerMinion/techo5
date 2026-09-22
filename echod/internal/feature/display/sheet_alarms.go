//go:build !dot

package display

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// The settings screen's Alarms & Timers card: the alarms, then how they ring. A tap on an alarm opens
// it in the editor, which takes the card over; its switch turns it on and off where it stands.

// repeatNames are the Repeat list's choices, in the order of repeats.
var repeatNames = []string{"Once", "Every day", "Weekdays", "Weekends"}

func alarmsCard(sv sheetView) cardView {
	if sv.draft != nil {
		return alarmEditorCard(sv, *sv.draft)
	}
	v := cardView{
		title: categoryTitles[catAlarms], blurb: categoryBlurbs[catAlarms],
		actions: []headerAction{{id: "add", label: "+ Add alarm", style: btnPrimary}},
	}
	for _, row := range alarmRows(sv.alarms) {
		switch {
		case row.snoozed != nil:
			v.rows = append(v.rows, settingRow{id: "snoozed", label: "Snoozed until " + clockText(row.snoozed.At),
				sub: cmpOr(row.snoozed.Label, "Alarm"), bold: true, kind: ctlButton, button: "Cancel"})
		case row.local != nil:
			a := row.local
			sub := capitalize(config.DaysLabel(a.Days))
			if a.Label != "" {
				sub += " · " + a.Label
			}
			v.rows = append(v.rows, settingRow{id: "alarm:" + a.ID, label: clockTime(a.Hour, a.Minute), sub: sub,
				bold: true, kind: ctlToggle, on: a.On, rowTap: true})
		case row.followed != nil:
			f := row.followed
			label, when := "Not set", "In Home Assistant"
			if !f.At.IsZero() {
				label, when = clockText(f.At), dayWord(f.At, sv.now)
			}
			if !f.Armed {
				when = "Off"
			}
			v.rows = append(v.rows, settingRow{label: label, sub: "From Home Assistant · " + cmpOr(f.Label, f.Entity),
				bold: true, kind: ctlValue, value: when})
		}
	}
	if len(v.rows) == 0 {
		v.rows = append(v.rows, settingRow{label: "No alarms yet", sub: "Add one here, or from Home Assistant", kind: ctlValue})
	}
	v.rows = append(v.rows, timerRows(sv)...)
	rows := []settingRow{
		{id: "sunrise", label: "Wake with light", sub: sunriseSub(), kind: ctlChoice, value: sunriseValue()},
	}
	if config.Get().Alarms.SunriseMinutes > 0 {
		rows = append(rows, settingRow{id: "sunface", label: "Sun with a face",
			sub: "A face on it, for whoever has to look at it", kind: ctlToggle, on: config.Get().Alarms.SunriseFace})
	}
	v.rows = append(v.rows, rows...)
	return v.withRows(
		settingRow{id: "snooze", label: "Snooze length", kind: ctlStepper, value: fmt.Sprintf("%d min", sv.snooze)},
		settingRow{id: "alarmsound", label: "Alarm sound", sub: "Plays once when you choose it", kind: ctlChoice, value: alarm.Get().Sound()},
	)
}

func (v cardView) withRows(rows ...settingRow) cardView {
	v.rows = append(v.rows, rows...)
	return v
}

// timerRows are the Alarms & Timers card's timers: what is counting down now, then a row that sets
// one. A timer set here can be stopped here; one from Home Assistant is stopped where it was set, so
// it is shown and left alone.
func timerRows(sv sheetView) []settingRow {
	var rows []settingRow
	for _, c := range sv.timers {
		if !c.Active {
			continue
		}
		where := "From Home Assistant"
		if c.Local {
			where = "Set here"
		}
		if c.Name != "" && c.Name != "Timer" {
			where += " · " + c.Name
		}
		row := settingRow{id: "timer:" + c.ID, label: timerLeft(c.Left), sub: where, bold: true, kind: ctlValue, value: "Running"}
		if c.Local {
			row.kind, row.button, row.value = ctlButton, "Stop", ""
		}
		rows = append(rows, row)
	}
	// The row that sets one goes under whatever is already counting down.
	return append(rows, settingRow{id: "newtimer", label: "New timer",
		sub: "Rings from this device itself", kind: ctlButton, button: "Set"})
}

// timerLeft is how long a timer has to run, as the row says it: minutes and seconds under an hour,
// hours and minutes over it.
func timerLeft(d time.Duration) string {
	d = d.Round(time.Second)
	if d >= time.Hour {
		return fmt.Sprintf("%d:%02d:%02d", int(d/time.Hour), int(d/time.Minute)%60, int(d/time.Second)%60)
	}
	return fmt.Sprintf("%d:%02d", int(d/time.Minute), int(d/time.Second)%60)
}

// The New timer row's choices.
var (
	timerLengths = []time.Duration{time.Minute, 2 * time.Minute, 3 * time.Minute, 5 * time.Minute,
		10 * time.Minute, 15 * time.Minute, 20 * time.Minute, 30 * time.Minute, 45 * time.Minute, time.Hour}
	timerLabels = []string{"1 minute", "2 minutes", "3 minutes", "5 minutes", "10 minutes", "15 minutes",
		"20 minutes", "30 minutes", "45 minutes", "1 hour"}
)

// alarmEditorCard is one alarm being set. Its heading says when it will ring, so a change reads
// back at once.
func alarmEditorCard(sv sheetView, d alarmDraft) cardView {
	a := d.alarm
	title := "Edit alarm"
	if d.isNew {
		title = "New alarm"
	}
	when := clockTime(a.Hour, a.Minute)
	blurb := "Rings at " + when + ", " + config.DaysLabel(a.Days)
	if a.Days&config.DaysEvery == config.DaysOnce {
		blurb = "Rings once, at " + when
	}
	v := cardView{
		title: title, blurb: blurb,
		actions: []headerAction{{id: "save", label: "Save", style: btnPrimary}, {id: "cancel", label: "Cancel", style: btnSecondary}},
		rows: []settingRow{
			{id: "e.hour", label: "Hour", kind: ctlStepper, value: hourText(a.Hour)},
			{id: "e.minute", label: "Minute", kind: ctlStepper, value: fmt.Sprintf(":%02d", a.Minute)},
			{id: "e.repeat", label: "Repeat", kind: ctlChoice, value: repeatName(a.Days)},
			{id: "e.days", label: "Days", kind: ctlDays, days: a.Days},
		},
	}
	if !d.isNew {
		del := settingRow{id: "e.delete", label: "Delete alarm", sub: "Asks twice", kind: ctlDanger, button: "Delete"}
		if !d.deleteArm.IsZero() && sv.now.Sub(d.deleteArm) < restartWindow {
			del.sub, del.button = "Tap again to delete it", "Confirm"
		}
		v.rows = append(v.rows, del)
	}
	return v
}

// repeatName is a set of days as the Repeat list names it; any other set is Custom.
func repeatName(days uint8) string {
	for i, r := range repeats {
		if r == days {
			return repeatNames[i]
		}
	}
	return "Custom"
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// dayWord is when t falls, from now: Today, Tomorrow, or the weekday within the week.
func dayWord(t, now time.Time) string {
	y1, m1, d1 := now.Date()
	y2, m2, d2 := t.In(now.Location()).Date()
	days := int(time.Date(y2, m2, d2, 0, 0, 0, 0, time.UTC).Sub(time.Date(y1, m1, d1, 0, 0, 0, 0, time.UTC)).Hours() / 24)
	switch {
	case days == 0:
		return "Today"
	case days == 1:
		return "Tomorrow"
	case days > 1 && days < 7:
		return t.Weekday().String()
	}
	return t.Format("Jan 2")
}

// actionTap is a button in the card's header.
func (d *Display) actionTap(id string) {
	switch id {
	case "back":
		d.sheetBack()
	case "colorsdone":
		d.mu.Lock()
		d.colors, d.cardScroll = false, 0
		d.mu.Unlock()
	case "add":
		d.EditNewAlarm()
	case "cancel":
		d.closeDraft()
	case "save":
		d.mu.Lock()
		dr := d.draft
		d.mu.Unlock()
		if dr == nil {
			return
		}
		a := dr.alarm
		a.On = true
		var err error
		if dr.isNew {
			_, err = alarm.Get().Set(a.Hour, a.Minute, a.Days, a.Label)
		} else {
			err = alarm.Get().Put(a)
		}
		if err != nil {
			slog.Warn("saving an alarm failed", "err", err)
			return
		}
		d.closeDraft()
	}
}

func (d *Display) closeDraft() {
	d.mu.Lock()
	d.draft, d.picker, d.cardScroll = nil, "", 0
	d.mu.Unlock()
}

// editDraft changes the alarm open in the editor, if one is.
func (d *Display) editDraft(f func(*alarmDraft)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.draft != nil {
		c := *d.draft
		f(&c)
		d.draft = &c
	}
}

// alarmRowTap is a tap on one of the Alarms card's rows, or the editor's; it reports whether the row
// was one of theirs.
func (d *Display) alarmRowTap(id string, p part, opt int) bool {
	if key, ok := strings.CutPrefix(id, "timer:"); ok {
		// Only the device's own stop here; Home Assistant's row has no button to tap.
		timer.Get().Cancel(key)
		return true
	}
	if key, ok := strings.CutPrefix(id, "alarm:"); ok {
		for _, a := range config.Get().Alarms.List {
			if a.ID != key {
				continue
			}
			if p == partRow {
				d.mu.Lock()
				d.draft, d.cardScroll = &alarmDraft{alarm: a}, 0
				d.mu.Unlock()
				return true
			}
			a.On = !a.On
			if err := alarm.Get().Put(a); err != nil {
				slog.Warn("saving an alarm failed", "err", err)
			}
		}
		return true
	}
	switch id {
	case "snoozed":
		alarm.Get().CancelSnoozes()
	case "snooze":
		n := config.Get().Alarms.Snooze()
		switch p {
		case partMinus:
			alarm.Get().SetSnooze(n - 1)
		case partPlus:
			alarm.Get().SetSnooze(n + 1)
		}
	case "alarmsound", "e.repeat":
		d.openPicker(id)
	case "e.hour":
		d.editDraft(func(dr *alarmDraft) {
			switch p {
			case partMinus:
				dr.alarm.Hour = (dr.alarm.Hour + 23) % 24
			case partPlus:
				dr.alarm.Hour = (dr.alarm.Hour + 1) % 24
			}
		})
	case "e.minute":
		d.editDraft(func(dr *alarmDraft) {
			switch p {
			case partMinus:
				dr.alarm.Minute = ((dr.alarm.Minute+4)/5*5 + 55) % 60
			case partPlus:
				dr.alarm.Minute = (dr.alarm.Minute/5*5 + 5) % 60
			}
		})
	case "e.days":
		if p == partDay && opt >= 0 && opt < 7 {
			d.editDraft(func(dr *alarmDraft) { dr.alarm.Days ^= 1 << opt })
		}
	case "e.delete":
		d.mu.Lock()
		dr := d.draft
		d.mu.Unlock()
		if dr == nil || dr.isNew {
			return true
		}
		if !dr.deleteArm.IsZero() && time.Since(dr.deleteArm) < restartWindow {
			if err := alarm.Get().Delete(dr.alarm.ID); err != nil {
				slog.Warn("deleting an alarm failed", "err", err)
			}
			d.closeDraft()
			return true
		}
		d.editDraft(func(dr *alarmDraft) { dr.deleteArm = time.Now() })
	default:
		return false
	}
	return true
}
