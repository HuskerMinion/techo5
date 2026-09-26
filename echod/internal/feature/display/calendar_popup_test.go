//go:build !dot && !spot

package display

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// When each event pops up: a timed one its lead before it starts until ten minutes in, an all-day one
// from the morning of its first day, only on the calendars chosen, and each only once.
func TestDuePopups(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Screen().Night("22:00-06:30"); err != nil {
		t.Fatal(err)
	}
	at := func(d, h, m int) time.Time { return time.Date(2026, 9, d, h, m, 0, 0, time.Local) }
	dentist := hass.Event{Calendar: "calendar.family", Summary: "Dentist", Start: at(26, 15, 0), End: at(26, 16, 0)}
	birthday := hass.Event{Calendar: "calendar.family", Summary: "Birthday", Start: at(26, 0, 0), End: at(27, 0, 0), AllDay: true}
	trip := hass.Event{Calendar: "calendar.family", Summary: "Trip", Start: at(25, 0, 0), End: at(28, 0, 0), AllDay: true}
	standup := hass.Event{Calendar: "calendar.work", Summary: "Standup", Start: at(26, 15, 5), End: at(26, 15, 20)}
	events := []hass.Event{dentist, birthday, trip, standup}

	c := config.Calendar{Popups: true, Sources: []string{"calendar.family", "calendar.work"}}
	names := func(now time.Time, c config.Calendar, shown map[string]bool) []string {
		var out []string
		for _, e := range duePopups(events, now, c, shown) {
			out = append(out, e.Summary)
		}
		return out
	}
	check := func(what string, got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s: %v, want %v", what, got, want)
			return
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s: %v, want %v", what, got, want)
				return
			}
		}
	}

	check("before the morning", names(at(26, 6, 0), c, nil))
	check("the morning", names(at(26, 6, 30), c, nil), "Birthday") // the trip began yesterday
	check("16 minutes before", names(at(26, 14, 44), c, map[string]bool{popupKey(birthday): true}))
	check("15 minutes before", names(at(26, 14, 45), c, map[string]bool{popupKey(birthday): true}), "Dentist")
	check("both", names(at(26, 14, 50), c, map[string]bool{popupKey(birthday): true}), "Dentist", "Standup")
	check("shown already", names(at(26, 14, 55), c, map[string]bool{popupKey(birthday): true, popupKey(dentist): true}), "Standup")
	check("ten minutes in", names(at(26, 15, 10), c, map[string]bool{popupKey(birthday): true}), "Standup")

	only := c
	only.PopupCalendars = []string{"calendar.work"}
	check("work only", names(at(26, 14, 55), only, nil), "Standup")

	never := c
	never.PopupAllDayNever = true
	check("all-day never", names(at(26, 9, 0), never, nil))

	start := c
	start.PopupBefore = -1
	check("as it starts: a minute before", names(at(26, 14, 59), start, map[string]bool{popupKey(birthday): true}))
	check("as it starts", names(at(26, 15, 0), start, map[string]bool{popupKey(birthday): true}), "Dentist")
}

// The heading's words for how soon.
func TestPopupSoon(t *testing.T) {
	now := time.Date(2026, 9, 26, 14, 45, 0, 0, time.Local)
	e := hass.Event{Start: now.Add(15 * time.Minute), End: now.Add(75 * time.Minute)}
	for _, c := range []struct {
		at   time.Time
		want string
	}{{now, "In 15 minutes"}, {now.Add(14 * time.Minute), "In a minute"}, {now.Add(16 * time.Minute), "Now"}} {
		if got := popupSoon(e, c.at); got != c.want {
			t.Errorf("at %s: %q, want %q", c.at.Format("15:04"), got, c.want)
		}
	}
	if popupSoon(hass.Event{AllDay: true}, now) != "Today" {
		t.Error("all day")
	}
}
