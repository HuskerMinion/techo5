package config

import (
	"testing"
	"time"
)

// A window that crosses midnight is the normal case for quiet hours, and the one that is easy to get
// wrong: ten at night to seven in the morning has to be quiet at two.
func TestQuietHoursCrossMidnight(t *testing.T) {
	s := Speaker{QuietHours: "22-7"}
	at := func(h int) time.Time { return time.Date(2026, 9, 22, h, 30, 0, 0, time.UTC) }
	for _, h := range []int{22, 23, 0, 2, 6} {
		if !s.Quiet(at(h)) {
			t.Errorf("%d:30 is not quiet, want it inside 22-7", h)
		}
	}
	for _, h := range []int{7, 9, 15, 21} {
		if s.Quiet(at(h)) {
			t.Errorf("%d:30 is quiet, want it outside 22-7", h)
		}
	}
}

// A window that does not cross midnight, and the ways of saying never.
func TestAWindowWithinOneDayAndNoWindowAtAll(t *testing.T) {
	day := Speaker{QuietHours: "9-17"}
	at := func(h int) time.Time { return time.Date(2026, 9, 22, h, 0, 0, 0, time.UTC) }
	if !day.Quiet(at(12)) {
		t.Error("midday is not quiet inside 9-17")
	}
	if day.Quiet(at(20)) {
		t.Error("eight in the evening is quiet outside 9-17")
	}
	for _, v := range []string{"", "off", "nonsense", "7-7", "25-30"} {
		if (Speaker{QuietHours: v}).Quiet(at(3)) {
			t.Errorf("%q made the device quiet; anything that is not a window means never", v)
		}
	}
}
