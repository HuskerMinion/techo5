package alarm

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// An action's days take a single day for a one-off - today, tomorrow or a date - as well as the
// repeating days they always took, and refuse a moment that has already gone by.
func TestDaysOrDate(t *testing.T) {
	now := time.Date(2026, 9, 23, 14, 7, 0, 0, time.Local)
	for _, c := range []struct {
		in      string
		h, m    int
		days    uint8
		date    string
		refused bool
	}{
		{"", 18, 0, config.DaysOnce, "", false},
		{"weekdays", 7, 0, config.DaysWeekdays, "", false},
		{"today", 18, 0, config.DaysOnce, "2026-09-23", false},
		{"Tomorrow", 6, 30, config.DaysOnce, "2026-09-24", false},
		{"2026-09-29", 18, 0, config.DaysOnce, "2026-09-29", false},
		{"today", 9, 0, 0, "", true},       // gone by at 14:07
		{"2026-09-01", 18, 0, 0, "", true}, // in the past
	} {
		days, date, err := daysOrDate(c.in, c.h, c.m, now)
		if c.refused {
			if err == nil {
				t.Errorf("%q at %d:%02d should be refused", c.in, c.h, c.m)
			}
			continue
		}
		if err != nil || days != c.days || date != c.date {
			t.Errorf("%q = %v %q %v, want %v %q", c.in, days, date, err, c.days, c.date)
		}
	}
}

// A dated one-off rings at that moment on that day, not the next time the clock reaches it.
func TestADatedOneOffIsAFixedMoment(t *testing.T) {
	al := config.Alarm{Hour: 18, Minute: 0, Days: config.DaysOnce, Date: "2026-09-29", On: true}
	at, ok := al.OnDate()
	if !ok || !at.Equal(time.Date(2026, 9, 29, 18, 0, 0, 0, time.Local)) {
		t.Fatalf("OnDate = %v %v", at, ok)
	}
	src := source{hour: al.Hour, min: al.Minute, days: al.Days, once: at}
	next, ok := src.next(time.Date(2026, 9, 23, 19, 0, 0, 0, time.Local))
	if !ok || !next.Equal(at) {
		t.Errorf("next = %v %v, want the dated moment %v", next, ok, at)
	}
	if al.When() != "Tue, Sep 29" {
		t.Errorf("When = %q", al.When())
	}
	// A repeating alarm has no date, whatever is written in it.
	al.Days = config.DaysWeekdays
	if _, ok := al.OnDate(); ok {
		t.Error("a repeating alarm read as dated")
	}
}
