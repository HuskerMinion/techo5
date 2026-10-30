package alarm

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Alarms on turns every alarm off at once and back on (D#350): while off, no alarm is a source and the
// snoozes are gone, each alarm keeps its own switch so on brings back the ones that were on, and a
// reminder still goes off.
func TestAlarmsOnTurnsEveryAlarmOffAndBack(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	a := build()
	wake := config.Alarm{ID: "wake", Hour: 6, Minute: 30, Days: config.DaysEvery, On: true}
	spare := config.Alarm{ID: "spare", Hour: 9, Minute: 0, Days: config.DaysEvery}
	bins := config.Alarm{ID: "bins", Hour: 19, Minute: 0, Days: config.DaysEvery, Label: "Bins out", Remind: true, On: true}
	for _, al := range []config.Alarm{wake, spare, bins} {
		if err := a.Put(al); err != nil {
			t.Fatal(err)
		}
	}
	a.mu.Lock()
	a.snoozed = []source{{key: "snooze:wake", once: time.Now().Add(9 * time.Minute)}}
	a.mu.Unlock()
	keys := func() map[string]bool {
		out := map[string]bool{}
		for _, s := range a.sources(time.Now()) {
			out[s.key] = true
		}
		return out
	}

	a.SetAllOn(false)
	if got := keys(); got["wake"] || got["snooze:wake"] || !got["bins"] || len(got) != 1 {
		t.Errorf("all off: sources %v, want the reminder alone", got)
	}
	if !config.Get().Alarms.AllOff || a.allOn.Get() {
		t.Error("all off is not saved, or the switch still says on")
	}

	a.SetAllOn(true)
	if got := keys(); !got["wake"] || got["spare"] || !got["bins"] || len(got) != 2 {
		t.Errorf("back on: sources %v, want the alarm that was on and the reminder", got)
	}
}

// The Next scheduled alarm sensor says when the soonest alarm is set for, leaving out a snooze, which
// Next alarm follows, and reminders; nothing when no alarm is coming.
func TestTheScheduledAlarmLeavesOutSnoozes(t *testing.T) {
	loc := time.Local
	now := time.Date(2026, 10, 30, 6, 35, 0, 0, loc)
	daily := source{key: "wake", hour: 6, min: 30, days: config.DaysEvery, local: true}
	snooze := source{key: "snooze:wake", once: now.Add(5 * time.Minute)}
	remind := source{key: "bins", hour: 6, min: 50, days: config.DaysEvery, remind: true}

	want := time.Date(2026, 10, 31, 6, 30, 0, 0, loc).UTC().Format(time.RFC3339)
	if got := scheduledText([]source{daily, snooze, remind}, now); got != want {
		t.Errorf("scheduled = %q, want tomorrow's 6:30, %q", got, want)
	}
	if got := scheduledText([]source{snooze, remind}, now); got != "" {
		t.Errorf("only a snooze and a reminder: scheduled = %q, want nothing", got)
	}
}

// An alarm asked to ring while every alarm is off turns Alarms on back on: one set by voice or Home
// Assistant, even one already there, and one switched on. A reminder, and an edit of an alarm that was
// on already, leave it off.
func TestAnAlarmSetWhileAllAreOffTurnsThemBackOn(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	a := build()
	allOff := func() {
		t.Helper()
		a.SetAllOn(false)
	}
	on := func() bool { return !config.Get().Alarms.AllOff && a.allOn.Get() }

	wake := config.Alarm{ID: "wake", Hour: 6, Minute: 30, Days: config.DaysEvery, On: true}
	if err := a.Put(wake); err != nil {
		t.Fatal(err)
	}
	allOff()
	wake.Label = "Work"
	if err := a.Put(wake); err != nil {
		t.Fatal(err)
	}
	if on() {
		t.Error("editing an alarm that was on turned every alarm back on")
	}
	if _, err := a.SetReminder(19, 0, config.DaysEvery, "Bins out", nil); err != nil {
		t.Fatal(err)
	}
	if on() {
		t.Error("a reminder turned every alarm back on")
	}

	if _, err := a.Set(7, 0, config.DaysEvery, ""); err != nil {
		t.Fatal(err)
	}
	if !on() {
		t.Error("an alarm set while all were off is kept silent")
	}

	allOff()
	if _, err := a.Set(7, 0, config.DaysEvery, ""); err != nil {
		t.Fatal(err)
	}
	if !on() {
		t.Error("setting an alarm that is already there, while all were off, keeps it silent")
	}

	allOff()
	spare := config.Alarm{ID: "spare", Hour: 9, Minute: 0, Days: config.DaysEvery}
	if err := a.Put(spare); err != nil {
		t.Fatal(err)
	}
	spare.On = true
	if err := a.Put(spare); err != nil {
		t.Fatal(err)
	}
	if !on() {
		t.Error("switching an alarm on while all were off keeps it silent")
	}
}
