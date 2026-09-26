package config

import (
	"testing"
	"time"
)

// Old whole-hour windows still read, and a window can now start and end at any minute, across
// midnight or not.
func TestWindows(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 9, 26, h, m, 0, 0, time.Local) }
	for _, c := range []struct {
		v    string
		now  time.Time
		want bool
	}{
		{"22-6", at(23, 0), true},
		{"22-6", at(5, 59), true},
		{"22-6", at(6, 0), false},
		{"22-6", at(21, 59), false},
		{"19:00-09:30", at(19, 0), true},
		{"19:00-09:30", at(9, 29), true},
		{"19:00-09:30", at(9, 30), false},
		{"19:00-09:30", at(18, 59), false},
		{"13:15-14:45", at(14, 0), true},
		{"13:15-14:45", at(13, 0), false},
		{"", at(3, 0), false},
		{"off", at(3, 0), false},
		{"7-7", at(7, 0), false},
		{"25-6", at(3, 0), false},
		{"19:5-6", at(3, 0), false},
	} {
		if got := InWindow(c.v, c.now); got != c.want {
			t.Errorf("InWindow(%q, %s) = %v", c.v, c.now.Format("15:04"), got)
		}
	}
}

// Whole hours are written as they always were, so a preset reads as itself; anything else carries
// its minutes.
func TestFormatWindow(t *testing.T) {
	for _, c := range []struct {
		from, to int
		want     string
	}{
		{22 * 60, 6 * 60, "22-6"},
		{0, 7 * 60, "0-7"},
		{19 * 60, 9*60 + 30, "19:00-09:30"},
	} {
		got := FormatWindow(c.from, c.to)
		if got != c.want {
			t.Errorf("FormatWindow(%d, %d) = %q, want %q", c.from, c.to, got, c.want)
		}
		if f, to, ok := ParseWindow(got); !ok || f != c.from || to != c.to {
			t.Errorf("%q does not read back", got)
		}
	}
}
