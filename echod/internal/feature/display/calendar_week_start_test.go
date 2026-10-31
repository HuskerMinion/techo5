//go:build !dot && !spot

package display

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Weeks start on Sunday until Week starts on says Monday, and then the month's rows, the week view and
// a day's week all begin on the Monday, a Sunday counting as the last day of its week.
func TestWeekStartsOnMonday(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 15, 0, 0, 0, time.Local) }
	midnight := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 0, 0, 0, 0, time.Local) }

	if got := weekStart(day(time.October, 28)); !got.Equal(midnight(time.October, 25)) {
		t.Errorf("by default the week of Wednesday 28 October starts on Sunday 25, got %v", got)
	}
	if dayInWeek(day(time.November, 1)) != 0 {
		t.Errorf("by default a Sunday is the first day of its week")
	}

	if err := config.Set().Calendar().MondayFirst(true); err != nil {
		t.Fatal(err)
	}
	if got := weekStart(day(time.October, 28)); !got.Equal(midnight(time.October, 26)) {
		t.Errorf("the week of Wednesday 28 October starts on Monday 26, got %v", got)
	}
	if got := weekStart(day(time.November, 1)); !got.Equal(midnight(time.October, 26)) {
		t.Errorf("Sunday 1 November is the last day of the week from Monday 26 October, got %v", got)
	}
	if dayInWeek(day(time.November, 1)) != 6 || dayInWeek(day(time.November, 2)) != 0 {
		t.Errorf("a Sunday is the seventh day and a Monday the first")
	}
	// November 2026 starts on a Sunday: five rows from Sunday, six from Monday.
	first := midnight(time.November, 1)
	if weeks := (dayInWeek(first) + 30 + 6) / 7; weeks != 6 {
		t.Errorf("November 2026 from Monday takes six rows, got %d", weeks)
	}
}

// Week starts on is a choice of two on the screen and in Home Assistant, Sunday first, and choosing
// Monday is what the calendar draws from.
func TestWeekStartsOnSwitch(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	s, ok := lookByRow(lookRowPrefix + "week_starts")
	if !ok {
		t.Fatal("no Week starts on row")
	}
	if !slices.Equal(s.opts, []string{"Sunday", "Monday"}) {
		t.Fatalf("its choices are %v", s.opts)
	}
	if lookOpt(s, deviceLook()) != 0 {
		t.Errorf("a new device starts its weeks on Sunday")
	}
	if err := s.set(false); err != nil {
		t.Fatal(err)
	}
	if !config.Get().Calendar.MondayFirst || lookOpt(s, deviceLook()) != 1 {
		t.Errorf("choosing Monday is saved and shown")
	}
	if err := s.set(true); err != nil {
		t.Fatal(err)
	}
	if config.Get().Calendar.MondayFirst {
		t.Errorf("choosing Sunday again turns it off")
	}
}
