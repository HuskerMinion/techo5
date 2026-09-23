package alarm

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func TestAReminderTimeIsATimeOfDayOrATimeFromNow(t *testing.T) {
	now := time.Date(2026, 9, 23, 14, 7, 30, 0, time.UTC)
	for _, c := range []struct {
		in           string
		hour, minute int
		fromNow      bool
	}{
		{"2:30 pm", 14, 30, false},
		{"07:05", 7, 5, false},
		{"in 20 minutes", 14, 28, true}, // 14:27:30, rounded up so it is never early
		{"in 30 seconds", 14, 8, true},  // exactly 14:08:00 needs no rounding
		{"1h", 15, 8, true},
		{"in 10 hours", 0, 8, true}, // past midnight: tomorrow's 00:08, which is the next one
	} {
		h, m, rel, err := reminderTime(c.in, now)
		if err != nil || h != c.hour || m != c.minute || rel != c.fromNow {
			t.Errorf("reminderTime(%q) = %d:%02d from now %v, %v; want %d:%02d from now %v",
				c.in, h, m, rel, err, c.hour, c.minute, c.fromNow)
		}
	}
	if _, _, _, err := reminderTime("sometime", now); err == nil {
		t.Error("an unreadable time should be refused")
	}
}

// A reminder and an alarm at the same time with the same words are two different things: setting
// one must not turn the other into it.
func TestAReminderAndAnAlarmAtTheSameTimeStayApart(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	a := build()

	al, err := a.Set(8, 0, config.DaysOnce, "Pills")
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.SetReminder(8, 0, config.DaysOnce, "Pills", []string{"Kitchen"})
	if err != nil {
		t.Fatal(err)
	}
	if r.ID == al.ID || !r.Remind || al.Remind {
		t.Fatalf("alarm %+v and reminder %+v were merged", al, r)
	}
	if again, _ := a.SetReminder(8, 0, config.DaysOnce, "Pills", nil); again.ID != r.ID || len(again.RingOn) != 0 {
		t.Errorf("setting the same reminder again made %+v, want %s with no other devices", again, r.ID)
	}
	if n := len(config.Get().Alarms.List); n != 2 {
		t.Errorf("%d alarms saved, want 2", n)
	}
	if _, err := a.SetReminder(8, 0, config.DaysOnce, "  ", nil); err == nil {
		t.Error("a reminder with nothing to say should be refused")
	}
}
