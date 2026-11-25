package alarm

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

func TestListingCarriesIdsAndNextTimes(t *testing.T) {
	loc := time.FixedZone("test", -5*3600)
	now := time.Date(2026, 9, 23, 14, 0, 0, 0, loc) // a Wednesday
	v := View{
		Local: []config.Alarm{
			{ID: "a1", Hour: 6, Minute: 45, Days: config.DaysWeekdays, Label: "Wake up", On: true},
			{ID: "a2", Hour: 14, Minute: 30, Label: "Take the medication", On: true, Remind: true, RingOn: []string{"Kitchen"}},
			{ID: "a3", Hour: 9, Minute: 0, Days: config.DaysWeekends, On: false},
			{ID: "a4", Hour: 6, Minute: 0, Days: config.DaysWeekdays, On: true, Silent: true},
			{ID: "a5", Hour: 6, Minute: 15, Days: config.DaysWeekdays, On: true, LightOnly: true},
			{ID: "a6", Hour: 7, Minute: 0, Days: config.DaysWeekdays, On: true, Wake: config.WakeWith{Kind: config.WakeRadio, Name: "WXYZ"}},
		},
		Followed: []Followed{{Entity: "input_datetime.school", Label: "School run", Armed: true}},
	}
	timers := []timer.Countdown{{ID: "local:x", Name: "Pasta", Left: 272 * time.Second, Total: 600 * time.Second, Active: true, Local: true},
		{ID: "local:y", Name: "Pairing", Left: time.Minute, Total: 2 * time.Minute, Active: true, Local: true, Silent: true}}

	l := listing(now, v, timers, nil)

	if l.Ringing != nil {
		t.Errorf("nothing is ringing, got %+v", l.Ringing)
	}
	want := []ListedAlarm{
		{ID: "a1", Time: "06:45", Days: "weekdays", Label: "Wake up", On: true, Next: "2026-09-24T06:45:00-05:00", RingOn: []string{}, WakeWith: "alarm_sound"},
		{ID: "a2", Time: "14:30", Days: "once", Label: "Take the medication", On: true, Next: "2026-09-23T14:30:00-05:00",
			Reminder: true, RingOn: []string{"Kitchen"}, WakeWith: "alarm_sound"},
		{ID: "a3", Time: "09:00", Days: "weekends", On: false, Next: "", RingOn: []string{}, WakeWith: "alarm_sound"},
		{ID: "a4", Time: "06:00", Days: "weekdays", On: true, Next: "2026-09-24T06:00:00-05:00", RingOn: []string{}, Silent: true, WakeWith: "alarm_sound"},
		{ID: "a5", Time: "06:15", Days: "weekdays", On: true, Next: "2026-09-24T06:15:00-05:00", RingOn: []string{}, LightOnly: true, WakeWith: "alarm_sound"},
		{ID: "a6", Time: "07:00", Days: "weekdays", On: true, Next: "2026-09-24T07:00:00-05:00", RingOn: []string{}, WakeWith: "radio: WXYZ"},
	}
	for i, w := range want {
		if !reflect.DeepEqual(l.Alarms[i], w) {
			t.Errorf("alarm %d = %+v, want %+v", i, l.Alarms[i], w)
		}
	}
	if got := l.Timers[0]; got.ID != "local:x" || got.Left != 272 || got.Total != 600 || !got.Local || !got.Running {
		t.Errorf("timer = %+v", got)
	}
	// A silent timer says so, for an automation that lists the timers and leaves its own waits be (#723).
	if l.Timers[0].Silent || !l.Timers[1].Silent {
		t.Errorf("silent = %v, %v; want only the timer_start_silent one silent", l.Timers[0].Silent, l.Timers[1].Silent)
	}
	if got := l.Followed[0]; got.Next != "" || got.Label != "School run" {
		t.Errorf("a helper with no usable time has no next: %+v", got)
	}
}

// Home Assistant needs a JSON object, and lists that are empty rather than null are easier to template.
func TestAnEmptyListingIsAnObjectOfEmptyLists(t *testing.T) {
	b, err := json.Marshal(listing(time.Now(), View{}, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, k := range []string{`"alarms":[]`, `"snoozed":[]`, `"followed":[]`, `"timers":[]`, `"ringing":null`} {
		if !strings.Contains(s, k) {
			t.Errorf("%s missing from %s", k, s)
		}
	}
}

// alarm_delete_id deletes the alarm it names, and given a timer's id from the listing it says where the
// timer goes instead of only that there is no such alarm.
func TestDeleteByIDTellsATimerFromAnAlarm(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	a := build()
	al, err := a.Set(6, 45, config.DaysWeekdays, "Wake up")
	if err != nil {
		t.Fatal(err)
	}
	timers := []timer.Countdown{{ID: "local:x", Local: true}, {ID: "01JABC", Name: "Pasta"}}

	for _, c := range []struct{ id, want string }{
		{"local:x", "timer_cancel"},
		{"01JABC", "set through Home Assistant"},
		{"nope", "no alarm"},
	} {
		if err := a.deleteFromHA(c.id, timers); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: %v, want it to say %q", c.id, err, c.want)
		}
	}
	if err := a.deleteFromHA(al.ID, timers); err != nil {
		t.Fatal(err)
	}
	if len(config.Get().Alarms.List) != 0 {
		t.Errorf("the alarm is still there: %+v", config.Get().Alarms.List)
	}
}

// alarm_turn_off and alarm_turn_on switch alarms without deleting them (#317), by time and label or
// by id, and say so when nothing matches.
func TestSwitchFromHA(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	a := build()
	wake, err := a.Set(6, 45, config.DaysWeekdays, "Wake up")
	if err != nil {
		t.Fatal(err)
	}
	gym, err := a.Set(6, 45, config.DaysWeekdays, "Gym")
	if err != nil {
		t.Fatal(err)
	}
	on := func() map[string]bool {
		m := map[string]bool{}
		for _, al := range config.Get().Alarms.List {
			m[al.ID] = al.On
		}
		return m
	}

	if err := a.switchFromHA(false, "6:45 am", "wake UP", ""); err != nil {
		t.Fatal(err)
	}
	if m := on(); m[wake.ID] || !m[gym.ID] || len(m) != 2 {
		t.Errorf("after turning off Wake up: %v", m)
	}
	if err := a.switchFromHA(false, "6:45", "", ""); err != nil {
		t.Fatal(err)
	}
	if m := on(); m[wake.ID] || m[gym.ID] || len(m) != 2 {
		t.Errorf("after turning off both: %v", m)
	}
	if err := a.switchFromHA(true, "", "", gym.ID); err != nil {
		t.Fatal(err)
	}
	if m := on(); m[wake.ID] || !m[gym.ID] {
		t.Errorf("after turning Gym on by id: %v", m)
	}
	for _, c := range []struct{ clock, label, id, want string }{
		{"7:00", "", "", "none at 7:00"},
		{"6:45", "Nap", "", "none at 6:45"},
		{"", "", "nope", "no alarm"},
		{"", "", "", ""},
	} {
		if err := a.switchFromHA(true, c.clock, c.label, c.id); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want it to say %q", c, err, c.want)
		}
	}
}

// Switching by time leaves reminders at that time alone; by id it switches what it names.
func TestSwitchByTimeLeavesReminders(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	a := build()
	wake, err := a.Set(7, 0, config.DaysWeekdays, "Wake up")
	if err != nil {
		t.Fatal(err)
	}
	pills, err := a.SetReminder(7, 0, config.DaysWeekdays, "Pills", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.switchFromHA(false, "7:00", "", ""); err != nil {
		t.Fatal(err)
	}
	for _, al := range config.Get().Alarms.List {
		if al.ID == wake.ID && al.On || al.ID == pills.ID && !al.On {
			t.Errorf("after turning off 7:00: %+v", al)
		}
	}
	if err := a.switchFromHA(false, "", "", pills.ID); err != nil {
		t.Fatal(err)
	}
}

// Home Assistant asks for every field an action has, so switching by id has actions of its own and the
// time-and-label ones ask for no id (#317).
func TestSwitchActionsAskOnlyForWhatTheyUse(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	a := build()
	want := map[string]string{
		"alarm_turn_off": "time label", "alarm_turn_on": "time label",
		"alarm_turn_off_id": "id", "alarm_turn_on_id": "id",
	}
	for _, act := range a.Actions() {
		w, ok := want[act.Name]
		if !ok {
			continue
		}
		delete(want, act.Name)
		var names []string
		for _, arg := range act.Args {
			names = append(names, arg.Name)
		}
		if got := strings.Join(names, " "); got != w {
			t.Errorf("%s asks for %q, want %q", act.Name, got, w)
		}
	}
	if len(want) != 0 {
		t.Errorf("missing actions: %v", want)
	}
	al, err := a.Set(6, 45, config.DaysWeekdays, "Wake up")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.switchByID(false, " "+al.ID+" "); err != nil {
		t.Fatal(err)
	}
	if config.Get().Alarms.List[0].On {
		t.Error("alarm_turn_off_id left the alarm on")
	}
	if err := a.switchByID(true, ""); err == nil || !strings.Contains(err.Error(), "no alarm id") {
		t.Errorf("a blank id: %v", err)
	}
}

// A ringing timer is the listing's ringing when no alarm rings, named and timed, and says it is a timer
// (#736): it has left the timers, so nothing else in the answer would say so. A ringing alarm comes first.
func TestARingingTimerIsListedAsRinging(t *testing.T) {
	loc := time.FixedZone("test", -5*3600)
	now := time.Date(2026, 11, 22, 15, 58, 12, 0, loc)
	rung := &timer.Rung{Name: "Pasta", Since: now.Add(-3 * time.Second)}

	l := listing(now, View{}, nil, rung)
	want := ListedRing{Label: "Pasta", Since: "2026-11-22T15:58:09-05:00", Kind: "timer"}
	if l.Ringing == nil || *l.Ringing != want {
		t.Errorf("ringing = %+v, want %+v", l.Ringing, want)
	}

	alarm := &Ring{Label: "Wake up", At: now.Add(-time.Minute)}
	l = listing(now, View{Ringing: alarm}, nil, rung)
	want = ListedRing{Label: "Wake up", Since: "2026-11-22T15:57:12-05:00", Kind: "alarm"}
	if l.Ringing == nil || *l.Ringing != want {
		t.Errorf("with an alarm ringing too, ringing = %+v, want %+v", l.Ringing, want)
	}
}
