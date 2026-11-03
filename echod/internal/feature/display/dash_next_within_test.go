//go:build !dot && !spot

package display

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The Dashboard's "Today only" cuts at the next local midnight: an event on since yesterday and one
// at 23:59 stay, one at midnight sharp does not.
func TestOnlyTodayCutsAtMidnight(t *testing.T) {
	now := oct(18, 14, 0)
	ev := func(name string, start, end time.Time) hass.Event {
		return hass.Event{Summary: name, Start: start, End: end}
	}
	in := []hass.Event{
		ev("since yesterday", oct(17, 22, 0), oct(18, 16, 0)),
		ev("late", oct(18, 23, 59), oct(19, 0, 30)),
		ev("midnight", oct(19, 0, 0), oct(19, 1, 0)),
		ev("tomorrow", oct(19, 9, 0), oct(19, 10, 0)),
	}
	out := onlyToday(in, now)
	if len(out) != 2 || out[0].Summary != "since yesterday" || out[1].Summary != "late" {
		t.Errorf("today only: %+v", out)
	}
	if got := onlyToday(in[2:], now); len(got) != 0 {
		t.Errorf("nothing today should leave nothing: %+v", got)
	}
}

// "Next up within" narrows both events on the card to the hours chosen; an event already on stays.
func TestPickNextUpWithin(t *testing.T) {
	now := oct(18, 8, 30)
	ev := func(name string, start, end time.Time) hass.Event {
		return hass.Event{Summary: name, Start: start, End: end}
	}
	events := []hass.Event{
		ev("on now", oct(18, 8, 0), oct(18, 9, 0)),
		ev("in 40 minutes", oct(18, 9, 10), oct(18, 10, 0)),
		ev("in 5 hours", oct(18, 13, 30), oct(18, 14, 0)),
		ev("this evening", oct(18, 21, 0), oct(18, 22, 0)),
	}
	names := func(within int) (string, string) {
		c, ok := pickNextUpWithin(events, now, within)
		if !ok {
			return "", ""
		}
		then := ""
		if c.then != nil {
			then = c.then.Summary
		}
		return c.ev.Summary, then
	}
	cases := []struct {
		within        int
		first, second string
	}{
		{0, "on now", "in 40 minutes"},
		{1, "on now", "in 40 minutes"},
		{6, "on now", "in 40 minutes"},
	}
	for _, c := range cases {
		if a, b := names(c.within); a != c.first || b != c.second {
			t.Errorf("within %d: %q then %q", c.within, a, b)
		}
	}
	// Only the on-now event and the 40-minute one fall within an hour; the second is cut at 3 hours
	// when the first is gone.
	rest := events[1:]
	if c, ok := pickNextUpWithin(rest, now, 1); !ok || c.ev.Summary != "in 40 minutes" || c.then != nil {
		t.Errorf("1 hour: %+v", c)
	}
	if c, ok := pickNextUpWithin(rest, now, 3); !ok || c.ev.Summary != "in 40 minutes" || c.then != nil {
		t.Errorf("3 hours: %+v", c)
	}
	if c, ok := pickNextUpWithin(rest, now, 6); !ok || c.then == nil || c.then.Summary != "in 5 hours" {
		t.Errorf("6 hours: %+v", c)
	}
	if c, ok := pickNextUpWithin(rest, now, 0); !ok || c.then == nil || c.then.Summary != "in 5 hours" {
		t.Errorf("default: %+v", c)
	}
	// 3 hours later the default still shows the evening one; 6 hours does not.
	late := []hass.Event{ev("this evening", oct(18, 21, 0), oct(18, 22, 0))}
	if _, ok := pickNextUpWithin(late, now, 0); !ok {
		t.Error("the default should show an event this evening")
	}
	if _, ok := pickNextUpWithin(late, now, 6); ok {
		t.Error("6 hours should not show an event 12 hours off")
	}
	if _, ok := pickNextUpWithin(late, oct(18, 15, 30), 6); !ok {
		t.Error("6 hours should show an event 5.5 hours off")
	}
}

// The two choices are saved as the values the settings file keeps, zero being the old behavior.
func TestDashboardAndNextWithinChoices(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := &Display{poke: make(chan struct{}, 1)}
	for _, c := range []struct {
		id    string
		i     int
		check func() bool
	}{
		{"cal_next_up_within", 3, func() bool { return config.Get().Calendar.NextUpWithin == 6 }},
		{"cal_next_up_within", 0, func() bool { return config.Get().Calendar.NextUpWithin == 0 }},
		{"cal_dashboard_events", 1, func() bool { return config.Get().Calendar.DashboardToday }},
		{"cal_dashboard_events", 0, func() bool { return !config.Get().Calendar.DashboardToday }},
	} {
		if !d.lookChoose(c.id, c.i) || !c.check() {
			t.Errorf("%s choice %d not kept", c.id, c.i)
		}
	}
}
