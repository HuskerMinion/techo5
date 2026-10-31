//go:build !dot && !spot

package display

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The week starts on Sunday unless somebody turns Week starts Monday on; then a Sunday is the last day
// of its week, not the first.
func TestTheWeekStartsOnMondayOnlyWhenTurnedOn(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	day := func(d int) time.Time { return time.Date(2026, time.November, d, 15, 0, 0, 0, time.Local) }
	cases := []struct {
		monday   bool
		on, want int
	}{
		{false, 4, 1}, {false, 1, 1}, {false, 7, 1}, // Wednesday, Sunday, Saturday: Sunday the 1st
		{true, 4, 2}, {true, 2, 2}, {true, 8, 2}, // Wednesday, Monday, Sunday: Monday the 2nd
		{true, 1, -5}, // Sunday the 1st: Monday, October 26
	}
	for _, c := range cases {
		if err := config.Set().Calendar().MondayFirst(c.monday); err != nil {
			t.Fatal(err)
		}
		want := time.Date(2026, time.November, c.want, 0, 0, 0, 0, time.Local)
		if got := weekStart(day(c.on)); !got.Equal(want) {
			t.Errorf("monday %v: the week holding November %d starts %v, want %v", c.monday, c.on, got, want)
		}
	}
	// The month's grid: November 2026 starts on a Sunday, the first column's with Sunday first and the
	// last with Monday first.
	first := day(1)
	if err := config.Set().Calendar().MondayFirst(false); err != nil {
		t.Fatal(err)
	}
	if intoWeek(first) != 0 {
		t.Errorf("Sunday first: November 1 in column %d", intoWeek(first))
	}
	if err := config.Set().Calendar().MondayFirst(true); err != nil {
		t.Fatal(err)
	}
	if intoWeek(first) != 6 {
		t.Errorf("Monday first: November 1 in column %d", intoWeek(first))
	}
}

// Off by default: a fresh device's look has it off.
func TestWeekStartsMondayIsOffByDefault(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if deviceLook().monday || config.Get().Calendar.MondayFirst {
		t.Error("Week starts Monday is on by default")
	}
}
