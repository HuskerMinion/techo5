package alarm

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

var testZone = func() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.FixedZone("EDT", -4*3600)
	}
	return loc
}()

// Wednesday 16 September 2026.
func at(day, hour, min int) time.Time { return time.Date(2026, 9, day, hour, min, 0, 0, testZone) }

func TestNextRepeatsOnItsDays(t *testing.T) {
	weekdays := source{hour: 7, min: 30, days: config.DaysWeekdays}
	for _, tc := range []struct {
		name string
		from time.Time
		want time.Time
	}{
		{"later today", at(16, 6, 0), at(16, 7, 30)},
		{"exactly now", at(16, 7, 30), at(16, 7, 30)},
		{"tomorrow once today has passed", at(16, 8, 0), at(17, 7, 30)},
		{"over the weekend to Monday", at(18, 9, 0), at(21, 7, 30)},
	} {
		got, ok := weekdays.next(tc.from)
		if !ok || !got.Equal(tc.want) {
			t.Errorf("%s: next = %v %v, want %v", tc.name, got, ok, tc.want)
		}
	}
}

func TestNextOnceIsTheNextOccurrence(t *testing.T) {
	once := source{hour: 6, min: 0}
	if got, _ := once.next(at(16, 5, 0)); !got.Equal(at(16, 6, 0)) {
		t.Errorf("before it: %v", got)
	}
	if got, _ := once.next(at(16, 22, 0)); !got.Equal(at(17, 6, 0)) {
		t.Errorf("after it: %v, want tomorrow morning", got)
	}

	fixed := source{once: at(17, 7, 0)}
	if _, ok := fixed.next(at(17, 7, 1)); ok {
		t.Error("a fixed moment in the past rang again")
	}
}

// Across the autumn clock change a 7:30 alarm is still 7:30 on the wall.
func TestNextKeepsWallTimeAcrossDST(t *testing.T) {
	daily := source{hour: 7, min: 30, days: config.DaysEvery}
	got, _ := daily.next(time.Date(2026, 11, 1, 1, 0, 0, 0, testZone))
	if got.Hour() != 7 || got.Minute() != 30 || got.Day() != 1 {
		t.Errorf("next = %v, want 7:30 on 1 November", got)
	}
}

func TestDueRingsWhatFellInTheWindowOnly(t *testing.T) {
	sources := []source{
		{key: "a", hour: 7, min: 30, days: config.DaysEvery},
		{key: "b", hour: 7, min: 31, days: config.DaysEvery},
	}
	got := due(sources, at(16, 7, 29), at(16, 7, 30))
	if len(got) != 1 || got[0].key != "a" {
		t.Errorf("due = %+v, want just a", got)
	}
	// The next look starts where the last ended: a is not rung twice.
	got = due(sources, at(16, 7, 30), at(16, 7, 31))
	if len(got) != 1 || got[0].key != "b" {
		t.Errorf("due = %+v, want just b", got)
	}
}

// A clock that jumps hours forward rings nothing it skipped over by more than stale.
func TestDueSkipsStaleRings(t *testing.T) {
	sources := []source{{key: "early", hour: 3, min: 0, days: config.DaysEvery}, {key: "now", hour: 9, min: 55, days: config.DaysEvery}}
	got := due(sources, at(16, 1, 0), at(16, 10, 0))
	if len(got) != 1 || got[0].key != "now" {
		t.Errorf("due = %+v, want only the one five minutes ago", got)
	}
}

func TestFromHelper(t *testing.T) {
	if s, ok := fromHelper("input_datetime.wake", "06:45:00", testZone); !ok || s.hour != 6 || s.min != 45 || s.days != config.DaysEvery {
		t.Errorf("time only = %+v %v, want every day at 6:45", s, ok)
	}
	if s, ok := fromHelper("input_datetime.trip", "2026-09-20 04:15:00", testZone); !ok || !s.once.Equal(time.Date(2026, 9, 20, 4, 15, 0, 0, testZone)) {
		t.Errorf("date and time = %+v %v", s, ok)
	}
	for _, bad := range []string{"", "unknown", "unavailable", "2026-09-20"} {
		if _, ok := fromHelper("x", bad, testZone); ok {
			t.Errorf("%q made an alarm", bad)
		}
	}
}

func TestParseClock(t *testing.T) {
	for in, want := range map[string][2]int{
		"7:30": {7, 30}, "07:30": {7, 30}, "19:05:00": {19, 5}, "7:30 pm": {19, 30}, "7:30 P.M.": {19, 30}, "6am": {6, 0}, "12 am": {0, 0},
		// As speech to text writes them.
		"8.14am": {8, 14}, "8.14 a.m.": {8, 14}, "8-18 AM": {8, 18}, "8.30": {8, 30}, "11.30 p.m.": {23, 30},
	} {
		h, m, err := parseClock(in)
		if err != nil || h != want[0] || m != want[1] {
			t.Errorf("parseClock(%q) = %d:%02d %v, want %d:%02d", in, h, m, err, want[0], want[1])
		}
	}
	for _, bad := range []string{"", "soon", "25:00"} {
		if _, _, err := parseClock(bad); err == nil {
			t.Errorf("parseClock(%q) accepted", bad)
		}
	}
}

func TestDays(t *testing.T) {
	for in, want := range map[string]uint8{
		"": config.DaysOnce, "daily": config.DaysEvery, "weekdays": config.DaysWeekdays, "Weekends": config.DaysWeekends,
		"mon,wed,fri": 0b0101010, "tuesday thursday": 0b0010100,
	} {
		if got, err := config.ParseDays(in); err != nil || got != want {
			t.Errorf("ParseDays(%q) = %07b %v, want %07b", in, got, err, want)
		}
	}
	if _, err := config.ParseDays("someday"); err == nil {
		t.Error("ParseDays accepted someday")
	}
	if got := config.DaysLabel(0b0101010); got != "Mon Wed Fri" {
		t.Errorf("DaysLabel = %q", got)
	}
}
