package alarm

import (
	"encoding/json"
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
		},
		Followed: []Followed{{Entity: "input_datetime.school", Label: "School run", Armed: true}},
	}
	timers := []timer.Countdown{{ID: "local:x", Name: "Pasta", Left: 272 * time.Second, Total: 600 * time.Second, Active: true, Local: true}}

	l := listing(now, v, timers)

	if l.Ringing != nil {
		t.Errorf("nothing is ringing, got %+v", l.Ringing)
	}
	want := []ListedAlarm{
		{ID: "a1", Time: "06:45", Days: "weekdays", Label: "Wake up", On: true, Next: "2026-09-24T06:45:00-05:00", RingOn: []string{}},
		{ID: "a2", Time: "14:30", Days: "once", Label: "Take the medication", On: true, Next: "2026-09-23T14:30:00-05:00",
			Reminder: true, RingOn: []string{"Kitchen"}},
		{ID: "a3", Time: "09:00", Days: "weekends", On: false, Next: "", RingOn: []string{}},
	}
	for i, w := range want {
		if !reflect.DeepEqual(l.Alarms[i], w) {
			t.Errorf("alarm %d = %+v, want %+v", i, l.Alarms[i], w)
		}
	}
	if got := l.Timers[0]; got.ID != "local:x" || got.Left != 272 || got.Total != 600 || !got.Local || !got.Running {
		t.Errorf("timer = %+v", got)
	}
	if got := l.Followed[0]; got.Next != "" || got.Label != "School run" {
		t.Errorf("a helper with no usable time has no next: %+v", got)
	}
}

// Home Assistant needs a JSON object, and lists that are empty rather than null are easier to template.
func TestAnEmptyListingIsAnObjectOfEmptyLists(t *testing.T) {
	b, err := json.Marshal(listing(time.Now(), View{}, nil))
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
