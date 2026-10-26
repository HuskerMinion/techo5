//go:build !dot

package display

import (
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
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
		title: tr(categoryTitleKeys[catAlarms]), blurb: tr(categoryBlurbKeys[catAlarms]),
		actions: []headerAction{{id: "add", label: tr("ui_add_alarm"), style: btnPrimary}},
	}
	for _, row := range alarmRows(sv.alarms) {
		switch {
		case row.snoozed != nil:
			v.rows = append(v.rows, settingRow{id: "snoozed", label: fmt.Sprintf(tr("ui_snoozed_until_s"), clockText(row.snoozed.At)),
				sub: cmpOr(row.snoozed.Label, tr("alarm")), bold: true, kind: ctlButton, button: tr("cancel")})
		case row.local != nil:
			a := row.local
			sub := capitalize(a.When())
			if a.Label != "" {
				sub += " · " + a.Label
			}
			v.rows = append(v.rows, settingRow{id: "alarm:" + a.ID, label: clockTime(a.Hour, a.Minute), sub: sub,
				bold: true, kind: ctlToggle, on: a.On, rowTap: true})
		case row.followed != nil:
			f := row.followed
			label, when := tr("ui_not_set"), tr("ui_in_home_assistant")
			if !f.At.IsZero() {
				label, when = clockText(f.At), dayWord(f.At, sv.now)
			}
			if !f.Armed {
				when = tr("ui_off")
			}
			v.rows = append(v.rows, settingRow{label: label, sub: fmt.Sprintf(tr("ui_from_home_assistant_s"), cmpOr(f.Label, f.Entity)),
				bold: true, kind: ctlValue, value: when})
		}
	}
	if len(v.rows) == 0 {
		v.rows = append(v.rows, settingRow{label: tr("ui_no_alarms_yet"), sub: tr("ui_add_one_here_or_from_home_assistant"), kind: ctlValue})
	}
	v.rows = append(v.rows, timerRows(sv)...)
	rows := []settingRow{
		{id: "sunrise", label: tr("wake_with_light"), sub: sunriseSub(), kind: ctlChoice, value: sunriseValue()},
	}
	if anySunrise() {
		rows = append(rows, settingRow{id: "sunface", label: tr("ui_sun_with_a_face"),
			sub: tr("ui_a_face_on_it_for_whoever_has_to_look_at_it"), kind: ctlToggle, on: config.Get().Alarms.SunriseFace})
	}
	v.rows = append(v.rows, rows...)
	return v.withRows(
		settingRow{id: "snooze", label: tr("ui_snooze_length"), kind: ctlStepper, value: fmt.Sprintf(tr("ui_d_min"), sv.snooze)},
		ringVolumeRow(),
		settingRow{id: "alarmsound", label: tr("alarm_sound"), sub: tr("ui_plays_once_when_you_choose_it"), kind: ctlChoice, value: alarm.Get().Sound()},
		settingRow{id: "alarmwords", label: tr("said_as_it_rings"), sub: tr("ui_spoken_by_home_assistant"), kind: ctlChoice, value: alarm.Get().Say().Label()},
		settingRow{id: "alarmrise", label: tr("ui_gets_louder"), sub: tr("ui_from_quiet_up_to_the_ring_volume"), kind: ctlChoice, value: alarm.Get().Rise().Label()},
	)
}

// ringVolumeRow is how loud alarms and timers ring. At zero it says what that means in words, since a
// silent alarm is the one setting on this card that can make somebody late.
func ringVolumeRow() settingRow {
	n := ring.Level()
	if n == 0 {
		return settingRow{id: "ringvol", label: tr("ui_ring_volume"), sub: tr("ui_alarms_and_timers_will_make_no_sound"), kind: ctlStepper, value: tr("ui_silent")}
	}
	return settingRow{id: "ringvol", label: tr("ui_ring_volume"), sub: tr("ui_alarms_and_timers_not_the_music"), kind: ctlStepper,
		value: fmt.Sprintf(tr("level_of"), n, sheetVolumeSteps)}
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
		where := tr("ui_from_home_assistant")
		if c.Local {
			where = "Set here"
		}
		if c.Name != "" && c.Name != "Timer" {
			where += " · " + c.Name
		}
		row := settingRow{id: "timer:" + c.ID, label: timerLeft(c.Left), sub: where, bold: true, kind: ctlValue, value: "Running"}
		if c.Local {
			row.kind, row.button, row.value = ctlButton, tr("stop"), ""
		}
		rows = append(rows, row)
	}
	// The row that sets one goes under whatever is already counting down.
	return append(rows, settingRow{id: "newtimer", label: tr("new_timer"),
		sub: tr("ui_rings_from_this_device_itself"), kind: ctlButton, button: tr("ui_set")})
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
		title = tr("ui_new_alarm")
	}
	when := clockTime(a.Hour, a.Minute)
	does := "Rings"
	if a.LightOnly {
		does = "Lights the room, no sound,"
	}
	blurb := does + " at " + when + ", " + config.DaysLabel(a.Days)
	if a.Days&config.DaysEvery == config.DaysOnce {
		blurb = does + " once, at " + when
		if t, ok := a.OnDate(); ok {
			blurb += ", " + t.Format("Monday, January 2")
		}
	}
	v := cardView{
		title: title, blurb: blurb,
		actions: []headerAction{{id: "save", label: tr("ui_save"), style: btnPrimary}, {id: "cancel", label: tr("cancel"), style: btnSecondary}},
		rows: []settingRow{
			{id: "e.hour", label: tr("ui_hour"), kind: ctlStepper, value: hourText(a.Hour)},
			{id: "e.minute", label: tr("ui_minute"), kind: ctlStepper, value: fmt.Sprintf(":%02d", a.Minute)},
			{id: "e.repeat", label: tr("repeat"), kind: ctlChoice, value: repeatName(a.Days)},
			{id: "e.days", label: tr("ui_days"), kind: ctlDays, days: a.Days},
		},
	}
	// A reminder is nobody waking up, so it has no light of its own to choose.
	if !a.Remind {
		v.rows = append(v.rows, settingRow{id: "e.sunrise", label: tr("wake_with_light"), kind: ctlChoice, value: alarmSunriseValue(a)})
		// A silent alarm is Home Assistant's, set there; it has no light to be only.
		if !a.Silent {
			v.rows = append(v.rows, settingRow{id: "e.lightonly", label: tr("ui_light_only"),
				sub: tr("ui_no_sound_the_light_then_the_night_ends"), kind: ctlToggle, on: a.LightOnly})
		}
	}
	if !d.isNew {
		del := settingRow{id: "e.delete", label: tr("ui_delete_alarm"), sub: tr("asks_twice"), kind: ctlDanger, button: tr("ui_delete")}
		if !d.deleteArm.IsZero() && sv.now.Sub(d.deleteArm) < restartWindow {
			del.sub, del.button = tr("ui_tap_again_to_delete_it"), tr("ui_confirm")
		}
		v.rows = append(v.rows, del)
	}
	return v
}

// repeatName is a set of days as the Repeat list names it; any other set is Custom.
func repeatName(days uint8) string {
	for i, r := range repeats {
		if r == days {
			return repeatLabels()[i]
		}
	}
	return tr("ui_custom")
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	// By letter, not byte: a name like élodie starts with two bytes.
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

// dayWord is when t falls, from now: Today, Tomorrow, or the weekday within the week.
func dayWord(t, now time.Time) string {
	y1, m1, d1 := now.Date()
	y2, m2, d2 := t.In(now.Location()).Date()
	days := int(time.Date(y2, m2, d2, 0, 0, 0, 0, time.UTC).Sub(time.Date(y1, m1, d1, 0, 0, 0, 0, time.UTC)).Hours() / 24)
	switch {
	case days == 0:
		return tr("today")
	case days == 1:
		return tr("tomorrow")
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
			var al config.Alarm
			if al, err = alarm.Get().Set(a.Hour, a.Minute, a.Days, a.Label); err == nil && (al.Sunrise != a.Sunrise || al.LightOnly != a.LightOnly) {
				al.Sunrise, al.LightOnly = a.Sunrise, a.LightOnly
				err = alarm.Get().Put(al)
			}
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
	case "ringvol":
		n := ring.Level()
		switch p {
		case partMinus:
			alarm.Get().SetRingVolume(n-1, true)
		case partPlus:
			alarm.Get().SetRingVolume(n+1, true)
		}
	case "alarmsound", "alarmwords", "alarmrise", "e.repeat", "e.sunrise":
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
	case "e.lightonly":
		d.editDraft(func(dr *alarmDraft) { dr.alarm.LightOnly = !dr.alarm.LightOnly })
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
