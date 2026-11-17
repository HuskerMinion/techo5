package alarm

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ygelfand/go-esphome-device/api"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
)

// The next alarm the clocks show (View's Next: after the day clock's date, and less the reminders under
// an Echo Show 5's or Show 8's night clock, #619) is the device's alarms and the Home Assistant helpers it follows together, whichever
// rings first, on the alarms' own weekdays. An alarm turned off is not it, and with every alarm turned
// off there is none.
func TestTheNextAlarmIsTheSoonerOfTheDevicesAndTheHelpers(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	const helper = "input_datetime.next_alarm_shown"
	if err := config.Set().Alarms().Follow([]string{helper}); err != nil {
		t.Fatal(err)
	}
	work := config.Alarm{ID: "work", Hour: 7, Minute: 30, Days: config.DaysWeekdays, On: true}
	skipPut(t, work)
	a := &Alarms{}
	says := func(state string) {
		t.Helper()
		if err := hastate.Get().Handle(context.Background(), nil, &api.HomeAssistantStateResponse{EntityId: helper, State: state}); err != nil {
			t.Fatal(err)
		}
	}
	next := func(now time.Time) time.Time {
		if v := a.View(now); v.Next != nil {
			return v.Next.At
		}
		return time.Time{}
	}
	friday, saturday := at(18, 3, 0), at(19, 3, 0)
	check := func(why string, now, want time.Time) {
		t.Helper()
		if got := next(now); !got.Equal(want) {
			t.Errorf("%s: next %v, want %v", why, got, want)
		}
	}

	says("unknown")
	check("the helper says nothing usable", friday, at(18, 7, 30))
	check("a weekday alarm over the weekend", saturday, at(21, 7, 30))

	says("06:45:00")
	check("the helper first", friday, at(18, 6, 45))
	check("the helper first, on a Saturday", saturday, at(19, 6, 45))

	says("08:00:00")
	check("the helper after the device's", friday, at(18, 7, 30))

	work.On = false
	skipPut(t, work)
	check("the device's turned off", friday, at(18, 8, 0))

	if err := config.Set().Alarms().AllOff(true); err != nil {
		t.Fatal(err)
	}
	if v := a.View(friday); v.Next != nil {
		t.Errorf("with every alarm off the next is %+v", v.Next)
	}
}

// A reminder is said, not rung: the next alarm a bedside night clock shows (NextAlarm) steps over one
// due first, where the day clock's Next keeps it, and a snooze is still an alarm that rings. Reminders
// stay on with every alarm off, and then there is no next alarm at all.
func TestTheNextAlarmLeavesOutReminders(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	skipPut(t, config.Alarm{ID: "trash", Hour: 6, Minute: 0, Days: config.DaysEvery, Label: "Take out the trash", On: true, Remind: true})
	skipPut(t, config.Alarm{ID: "work", Hour: 7, Minute: 30, Days: config.DaysEvery, On: true})
	a := &Alarms{}
	friday := at(18, 3, 0)
	v := a.View(friday)
	if v.Next == nil || v.Next.Key != "trash" || !v.Next.At.Equal(at(18, 6, 0)) {
		t.Errorf("Next = %+v, want the reminder at 6:00", v.Next)
	}
	if v.NextAlarm == nil || v.NextAlarm.Key != "work" || !v.NextAlarm.At.Equal(at(18, 7, 30)) {
		t.Errorf("NextAlarm = %+v, want the alarm at 7:30", v.NextAlarm)
	}

	a.snoozed = []source{{key: "snooze:work", once: at(18, 3, 9)}}
	if v := a.View(friday); v.NextAlarm == nil || !v.NextAlarm.Snoozed || !v.NextAlarm.At.Equal(at(18, 3, 9)) {
		t.Errorf("with a snooze, NextAlarm = %+v, want the snooze at 3:09", v.NextAlarm)
	}
	a.snoozed = nil

	if err := config.Set().Alarms().AllOff(true); err != nil {
		t.Fatal(err)
	}
	if v := a.View(friday); v.Next == nil || v.Next.Key != "trash" || v.NextAlarm != nil {
		t.Errorf("with every alarm off: Next = %+v, NextAlarm = %+v, want the reminder and no alarm", v.Next, v.NextAlarm)
	}
}
