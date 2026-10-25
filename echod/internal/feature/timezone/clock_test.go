package timezone

import (
	"testing"
	"time"
)

func TestNeedsClock(t *testing.T) {
	ha := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name   string
		now    time.Time
		ha     time.Time
		synced bool
		want   bool
	}{
		{"never set", time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC), ha, false, true},
		{"never set, the flag says synced", time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC), ha, true, true},
		{"right", ha.Add(3 * time.Second), ha, false, false},
		{"behind, no NTP", ha.Add(-time.Hour), ha, false, true},
		{"ahead, no NTP", ha.Add(time.Minute), ha, false, true},
		{"NTP keeps it", ha.Add(time.Hour), ha, true, false},
		{"Home Assistant's clock never set", ha, time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), false, false},
	} {
		if got := needsClock(c.now, c.ha, c.synced); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// The answer's seconds set the clock when it needs it, and nothing is set when it does not.
func TestTakeTime(t *testing.T) {
	now, synced, set := clockNow, clockSynced, clockSet
	t.Cleanup(func() { clockNow, clockSynced, clockSet = now, synced, set })
	var got time.Time
	clockSet = func(t time.Time) error { got = t; return nil }
	clockSynced = func() bool { return false }

	clockNow = func() time.Time { return time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC) }
	takeTime(1792670400)
	if got.Unix() != 1792670400 {
		t.Errorf("a clock never set was set to %v, want Home Assistant's", got)
	}

	got = time.Time{}
	clockNow = func() time.Time { return time.Unix(1792670402, 0) }
	takeTime(1792670400)
	takeTime(0)
	if !got.IsZero() {
		t.Errorf("a right clock was set to %v", got)
	}
}
