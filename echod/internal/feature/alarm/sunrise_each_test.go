package alarm

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Each alarm lights the room for its own window, or not at all. The light used to come up before
// whatever rang next, so a two o'clock reminder lit the room for twenty minutes, and an alarm that
// wanted no light could not say so.
func TestEachAlarmHasItsOwnWakeLight(t *testing.T) {
	now := time.Date(2026, 9, 24, 6, 30, 0, 0, time.UTC)
	c := config.Alarms{SunriseMinutes: 20}
	src := func(al config.Alarm) source {
		return source{key: al.ID, hour: al.Hour, min: al.Minute, days: config.DaysEvery, remind: al.Remind, sunrise: c.SunriseFor(al)}
	}

	defaulted := src(config.Alarm{ID: "a", Hour: 6, Minute: 40})                              // 10 of 20 min left
	own := src(config.Alarm{ID: "b", Hour: 6, Minute: 35, Sunrise: 10})                       // 5 of 10 left
	off := src(config.Alarm{ID: "c", Hour: 6, Minute: 31, Sunrise: config.SunriseOff})        // none
	reminder := src(config.Alarm{ID: "d", Hour: 6, Minute: 31, Remind: true, Label: "Pills"}) // none

	if got := sunriseAt([]source{defaulted}, now); got != 0.5 {
		t.Errorf("an alarm on the default is %.2f of the way, want 0.50", got)
	}
	if got := sunriseAt([]source{own}, now); got != 0.5 {
		t.Errorf("an alarm with its own ten minutes is %.2f of the way, want 0.50", got)
	}
	if got := sunriseAt([]source{off, reminder}, now); got != 0 {
		t.Errorf("an alarm with the light off and a reminder lit the room: %.2f", got)
	}
	if got := sunriseAt([]source{off, reminder, defaulted}, now); got != 0.5 {
		t.Errorf("the next thing ringing having no light hid the one that has: %.2f", got)
	}
	if got := sunriseAt([]source{src(config.Alarm{ID: "e", Hour: 7, Minute: 30})}, now); got != 0 {
		t.Errorf("an hour out, the light has already started: %.2f", got)
	}
}

func TestTheDefaultCoversAlarmsThatDoNotChoose(t *testing.T) {
	c := config.Alarms{SunriseMinutes: 15}
	for _, tc := range []struct {
		al   config.Alarm
		want int
	}{
		{config.Alarm{}, 15},
		{config.Alarm{Sunrise: 5}, 5},
		{config.Alarm{Sunrise: config.SunriseOff}, 0},
		{config.Alarm{Sunrise: 30, Remind: true}, 0},
	} {
		if got := c.SunriseFor(tc.al); got != tc.want {
			t.Errorf("SunriseFor(%+v) = %d, want %d", tc.al, got, tc.want)
		}
	}
	if got := (config.Alarms{}).SunriseFor(config.Alarm{}); got != 0 {
		t.Errorf("with no default an alarm that did not choose got %d minutes", got)
	}
}
