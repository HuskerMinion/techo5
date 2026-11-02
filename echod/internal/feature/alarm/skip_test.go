package alarm

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// skipState points the config store at a file of this test's own.
func skipState(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	config.Use(path)
	return path
}

func skipPut(t *testing.T, al config.Alarm) {
	t.Helper()
	if err := config.Set().Alarms().Put(al); err != nil {
		t.Fatal(err)
	}
}

func skipToken(t *testing.T, a *Alarms, now time.Time) SkipToken {
	t.Helper()
	token, ok := a.Skippable(now)
	if !ok {
		t.Fatalf("nothing was offered to leave out at %v", now)
	}
	return token
}

func findAlarm(t *testing.T, id string) config.Alarm {
	t.Helper()
	for _, al := range config.Get().Alarms.List {
		if al.ID == id {
			return al
		}
	}
	t.Fatalf("alarm %q is not set", id)
	return config.Alarm{}
}

// slot is a daily moment in the test zone.
func slot(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, testZone)
}

// mark is the moment left out for id, or the zero time.
func mark(c config.Alarms, id string) time.Time {
	for _, s := range c.Skipped {
		if s.ID == id {
			return s.At
		}
	}
	return time.Time{}
}

// One occurrence of a recurring alarm is left out from inside the light before it, and only that
// one: the alarm stays on with the same days, does not ring or light that morning, and its next
// morning is its usual one after a restart as well. The next time the screen and the listing show is
// the one after it.
func TestSkippingOneOccurrenceKeepsTheAlarmAndItsDays(t *testing.T) {
	path := skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "wake", Hour: 7, Minute: 0, Days: config.DaysEvery, Label: "Wake up", On: true})

	now := slot(2026, time.October, 19, 6, 45) // Monday, inside the light before 7:00
	monday := slot(2026, time.October, 19, 7, 0)
	a := &Alarms{}
	token := skipToken(t, a, now)
	if token.ID != "wake" || !token.At.Equal(monday) {
		t.Fatalf("offered %+v, want wake at %v", token, monday)
	}
	if err := a.skipOccurrence(token, now); err != nil {
		t.Fatal(err)
	}

	c := config.Get().Alarms
	if got := mark(c, "wake"); !got.Equal(monday) || len(c.Skipped) != 1 {
		t.Errorf("saved marks = %+v, want wake at %v", c.Skipped, monday)
	}
	if len(c.List) != 1 || !c.List[0].On || c.List[0].Days != config.DaysEvery {
		t.Errorf("leaving an occurrence out changed its alarm: %+v", c.List)
	}
	if got := due(a.sources(monday), monday.Add(-time.Minute), monday.Add(time.Minute)); len(got) != 0 {
		t.Errorf("the occurrence left out rang: %+v", got)
	}
	if got := a.SunriseProgress(monday.Add(-15 * time.Minute)); got != 0 {
		t.Errorf("the light came up for the occurrence left out: %.2f", got)
	}
	tuesday := slot(2026, time.October, 20, 7, 0)
	if v := a.View(now); v.Next == nil || !v.Next.At.Equal(tuesday) {
		t.Errorf("next on the screen = %+v, want %v", v.Next, tuesday)
	}
	if v := a.View(now); len(v.Skipped) != 1 {
		t.Errorf("the view does not say what was left out: %+v", v.Skipped)
	}
	if l := listing(now, a.View(now), nil); l.Alarms[0].Next != tuesday.Format(time.RFC3339) {
		t.Errorf("listed next = %q, want %q", l.Alarms[0].Next, tuesday)
	}
	// The morning after is ordinary: it is offered again from inside its own light.
	if next, ok := a.Skippable(tuesday.Add(-15 * time.Minute)); !ok || !next.At.Equal(tuesday) {
		t.Errorf("the next occurrence offered = %+v %v, want %v", next, ok, tuesday)
	}

	// A restart reads the mark back: the same morning is still left out, the next is not, and one
	// skip is not the alarm turned off.
	config.Use(path)
	if got := config.Get().Alarms.Skipped; len(got) != 1 || !got[0].At.Equal(monday) {
		t.Fatalf("the mark did not come back after a restart: %+v", got)
	}
	again := &Alarms{}
	if got := due(again.sources(monday), monday.Add(-time.Minute), monday.Add(time.Minute)); len(got) != 0 {
		t.Errorf("after a restart the occurrence left out rang: %+v", got)
	}
	if v := again.View(now); v.Next == nil || !v.Next.At.Equal(tuesday) {
		t.Errorf("after a restart next = %+v, want %v", v.Next, tuesday)
	}
	if next, ok := again.Skippable(tuesday.Add(-15 * time.Minute)); !ok || !next.At.Equal(tuesday) {
		t.Errorf("after a restart the occurrence offered = %+v %v, want %v", next, ok, tuesday)
	}
}

// A weekly alarm whose next occurrence is left out still finds the following week, eight days from
// the day before the first; and marks stay until their alarm goes, so several may be stepped over.
func TestWeeklyOccurrencesPastTheirMarksAreStillFound(t *testing.T) {
	t.Run("the week after the one left out", func(t *testing.T) {
		skipState(t)
		if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
			t.Fatal(err)
		}
		skipPut(t, config.Alarm{ID: "weekly", Hour: 7, Minute: 0, Days: 1 << time.Monday, Label: "Monday", On: true})

		now := slot(2026, time.October, 19, 6, 45) // Monday, inside the light before 7:00
		monday := slot(2026, time.October, 19, 7, 0)
		a := &Alarms{}
		if err := a.skipOccurrence(skipToken(t, a, now), now); err != nil {
			t.Fatal(err)
		}
		src := localSource(config.Get().Alarms, findAlarm(t, "weekly"))
		next := slot(2026, time.October, 26, 7, 0)
		if got, ok := src.next(now); !ok || !got.Equal(next) {
			t.Errorf("next after the skipped week = %v %v, want %v", got, ok, next)
		}
		if got, ok := src.next(monday); !ok || !got.Equal(next) {
			t.Errorf("next from the skipped moment = %v %v, want %v", got, ok, next)
		}
		if got := due(a.sources(next), next.Add(-time.Minute), next.Add(time.Minute)); len(got) != 1 || got[0].key != "weekly" {
			t.Errorf("the following week did not ring: %+v", got)
		}
	})

	t.Run("three weeks behind it", func(t *testing.T) {
		skipState(t)
		if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
			t.Fatal(err)
		}
		skipPut(t, config.Alarm{ID: "weekly", Hour: 7, Minute: 0, Days: 1 << time.Monday, Label: "Monday", On: true})
		marks := []config.Skip{
			{ID: "weekly", At: slot(2026, time.October, 19, 7, 0)},
			{ID: "weekly", At: slot(2026, time.October, 26, 7, 0)},
			{ID: "weekly", At: slot(2026, time.November, 2, 7, 0)},
		}
		if err := config.Set().Alarms().ChangeStrict(func(c *config.Alarms) error {
			c.Skipped = marks
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		src := localSource(config.Get().Alarms, findAlarm(t, "weekly"))
		want := slot(2026, time.November, 9, 7, 0)
		if got, ok := src.next(slot(2026, time.October, 18, 20, 0)); !ok || !got.Equal(want) {
			t.Errorf("next past three skipped weeks = %v %v, want %v", got, ok, want)
		}
		if got := due([]source{src}, want.Add(-time.Minute), want.Add(time.Minute)); len(got) != 1 || got[0].key != "weekly" {
			t.Errorf("the Monday past the marks did not ring: %+v", got)
		}
	})
}

// The open window offered is the one furthest along, not the one whose alarm is earliest: at 6:57 the
// light before a 7:00 alarm that lasts five minutes has two of them behind it, and the light before a
// 7:10 alarm that lasts thirty has seventeen, so the room is mostly the later alarm's light and that is
// the occurrence offered. Windows evenly far along go to the earlier occurrence, two at the same moment
// to the lower id, an occurrence already left out is no longer the ramp it was, and alarms off leave
// nothing to offer.
func TestSkippablePrefersTheRampFurthestAlong(t *testing.T) {
	day := func(hour, minute int) time.Time { return slot(2026, time.October, 19, hour, minute) }
	for _, tc := range []struct {
		name   string
		now    time.Time
		alarms []config.Alarm
		skip   []config.Skip
		allOff bool
		wantID string // "" for nothing offered
		wantAt time.Time
	}{
		{
			name: "the light further along, though its alarm is later",
			now:  day(6, 57),
			alarms: []config.Alarm{
				{ID: "short", Hour: 7, Minute: 0, Sunrise: 5, Label: "Short light"},
				{ID: "long", Hour: 7, Minute: 10, Sunrise: 30, Label: "Long light"},
			},
			wantID: "long", wantAt: day(7, 10),
		},
		{
			name: "two lights halfway along go to the earlier occurrence, whatever the order",
			now:  day(6, 56),
			alarms: []config.Alarm{
				{ID: "b", Hour: 6, Minute: 58, Sunrise: 4, Label: "B"},
				{ID: "a", Hour: 7, Minute: 0, Sunrise: 8, Label: "A"},
			},
			wantID: "b", wantAt: day(6, 58),
		},
		{
			name: "two at the same moment go to the lower id",
			now:  day(6, 45),
			alarms: []config.Alarm{
				{ID: "c", Hour: 7, Minute: 0, Sunrise: 30, Label: "C"},
				{ID: "a", Hour: 7, Minute: 0, Sunrise: 30, Label: "A"},
			},
			wantID: "a", wantAt: day(7, 0),
		},
		{
			name: "the first moment of a window is open, and two just opened go to the earlier",
			now:  day(6, 30),
			alarms: []config.Alarm{
				{ID: "a", Hour: 7, Minute: 0, Sunrise: 30, Label: "A"},
				{ID: "b", Hour: 6, Minute: 40, Sunrise: 10, Label: "B"},
			},
			wantID: "b", wantAt: day(6, 40),
		},
		{
			name: "a minute before either window opened, nothing",
			now:  day(6, 29),
			alarms: []config.Alarm{
				{ID: "a", Hour: 7, Minute: 0, Sunrise: 30, Label: "A"},
				{ID: "b", Hour: 7, Minute: 5, Sunrise: 10, Label: "B"},
			},
		},
		{
			name: "the furthest along, though an alarm already left out is there",
			now:  day(6, 57),
			alarms: []config.Alarm{
				{ID: "short", Hour: 7, Minute: 0, Sunrise: 5, Label: "Short light"},
				{ID: "long", Hour: 7, Minute: 10, Sunrise: 30, Label: "Long light"},
			},
			skip:   []config.Skip{{ID: "long", At: day(7, 10)}},
			wantID: "short", wantAt: day(7, 0),
		},
		{
			name: "alarms off leave nothing to offer",
			now:  day(6, 57),
			alarms: []config.Alarm{
				{ID: "long", Hour: 7, Minute: 10, Sunrise: 30, Label: "Long light"},
			},
			allOff: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			skipState(t)
			for _, al := range tc.alarms {
				al.Days, al.On = config.DaysEvery, true
				skipPut(t, al)
			}
			if len(tc.skip) > 0 {
				if err := config.Set().Alarms().ChangeStrict(func(c *config.Alarms) error {
					c.Skipped = append(c.Skipped, tc.skip...)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.allOff {
				if err := config.Set().Alarms().AllOff(true); err != nil {
					t.Fatal(err)
				}
			}
			a := &Alarms{}
			token, ok := a.Skippable(tc.now)
			if tc.wantID == "" {
				if ok {
					t.Fatalf("offered %+v, want nothing", token)
				}
				return
			}
			if !ok || token.ID != tc.wantID || !token.At.Equal(tc.wantAt) {
				t.Fatalf("offered %+v %v, want %s at %v", token, ok, tc.wantID, tc.wantAt)
			}
		})
	}
}

// Ramps are compared as exact fractions: the products exceed int64, and the last two fractions below
// round to the same float64.
func TestRampFractionsAreComparedExactly(t *testing.T) {
	const n = time.Duration(1 << 62)
	for _, tc := range []struct {
		name                                 string
		left, window, otherLeft, otherWindow time.Duration
		want                                 bool
	}{
		{"half before three fifths", 15 * time.Minute, 30 * time.Minute, 3 * time.Minute, 5 * time.Minute, true},
		{"three fifths after half", 3 * time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute, false},
		{"exactly equal fractions", 10 * time.Minute, 20 * time.Minute, 20 * time.Minute, 40 * time.Minute, false},
		{"near tie before", n - 2, n - 1, n - 1, n, true},
		{"near tie after", n - 1, n, n - 2, n - 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := leftIsLess(tc.left, tc.window, tc.otherLeft, tc.otherWindow); got != tc.want {
				t.Errorf("%v/%v < %v/%v = %v, want %v", tc.left, tc.window, tc.otherLeft, tc.otherWindow, got, tc.want)
			}
		})
	}
}

// A recurring alarm of the device's that is on and has a wake light can have an occurrence left out;
// a one-off, an alarm off, a reminder, a silent alarm, a light-only one, and one with no light at all
// cannot. Each is asked from inside the light a 7:00 alarm would have, and neither offered nor kept.
func TestOnlyNormalRecurringAlarmsWithAWakeLightCanBeLeftOut(t *testing.T) {
	for _, tc := range []struct {
		name    string
		alarm   config.Alarm
		sunrise int
	}{
		{"one-off", config.Alarm{Hour: 7, Days: config.DaysOnce, On: true}, 30},
		{"off", config.Alarm{Hour: 7, Days: config.DaysEvery}, 30},
		{"reminder", config.Alarm{Hour: 7, Days: config.DaysEvery, On: true, Remind: true}, 30},
		{"silent", config.Alarm{Hour: 7, Days: config.DaysEvery, On: true, Silent: true}, 30},
		{"light only", config.Alarm{Hour: 7, Days: config.DaysEvery, On: true, LightOnly: true}, 30},
		{"alarm light off", config.Alarm{Hour: 7, Days: config.DaysEvery, On: true, Sunrise: config.SunriseOff}, 30},
		{"no light at all", config.Alarm{Hour: 7, Days: config.DaysEvery, On: true}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			skipState(t)
			if err := config.Set().Alarms().SunriseMinutes(tc.sunrise); err != nil {
				t.Fatal(err)
			}
			al := tc.alarm
			al.ID, al.Label = "x", tc.name
			skipPut(t, al)

			now := slot(2026, time.October, 19, 6, 45) // inside the light a 7:00 alarm would have
			a := &Alarms{}
			if token, ok := a.Skippable(now); ok {
				t.Errorf("offered %+v, want nothing", token)
			}
			if err := a.skipOccurrence(SkipToken{ID: "x", At: slot(2026, time.October, 19, 7, 0)}, now); err == nil {
				t.Error("an occurrence that cannot be left out was")
			}
			if got := config.Get().Alarms.Skipped; len(got) != 0 {
				t.Errorf("a refused request left a mark: %+v", got)
			}
		})
	}
}

// An occurrence is offered only from inside the light before it: not before its window opens, not at
// the moment it rings, and not after.
func TestOnlyTheLightBeforeAnOccurrenceLetsItBeLeftOut(t *testing.T) {
	moment := slot(2026, time.October, 19, 7, 0)
	for _, tc := range []struct {
		name string
		now  time.Time
		open bool
	}{
		{"before the window opens", moment.Add(-31 * time.Minute), false},
		{"the first moment of the window", moment.Add(-30 * time.Minute), true},
		{"a second before it rings", moment.Add(-time.Second), true},
		{"the moment it rings", moment, false},
		{"a second after it rang", moment.Add(time.Second), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			skipState(t)
			if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
				t.Fatal(err)
			}
			skipPut(t, config.Alarm{ID: "wake", Hour: 7, Minute: 0, Days: config.DaysEvery, On: true})

			a := &Alarms{}
			token, ok := a.Skippable(tc.now)
			if !tc.open {
				if ok {
					t.Fatalf("offered %+v, want nothing", token)
				}
				if err := a.skipOccurrence(SkipToken{ID: "wake", At: moment}, tc.now); err == nil {
					t.Error("an occurrence was left out outside the light before it")
				}
				if got := config.Get().Alarms.Skipped; len(got) != 0 {
					t.Errorf("a refused request left a mark: %+v", got)
				}
				return
			}
			if !ok || token.ID != "wake" || !token.At.Equal(moment) {
				t.Fatalf("offered %+v %v, want wake at %v", token, ok, moment)
			}
			if err := a.skipOccurrence(token, tc.now); err != nil {
				t.Errorf("the occurrence shown was refused: %v", err)
			}
		})
	}
}

// Another alarm's light coming up is not this alarm's: only an occurrence whose own window is open
// is offered, and asked for outside its window it is refused.
func TestAnotherAlarmsWindowDoesNotOfferThisOne(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "a", Hour: 7, Minute: 0, Days: config.DaysEvery, Label: "A", On: true})
	skipPut(t, config.Alarm{ID: "b", Hour: 9, Minute: 0, Days: config.DaysEvery, Label: "B", On: true})

	a := &Alarms{}
	now := slot(2026, time.October, 19, 6, 45) // inside a's light (6:30-7:00) only
	if token, ok := a.Skippable(now); !ok || token.ID != "a" || !token.At.Equal(slot(2026, time.October, 19, 7, 0)) {
		t.Fatalf("offered %+v %v, want a at 7:00", token, ok)
	}
	if err := a.skipOccurrence(SkipToken{ID: "b", At: slot(2026, time.October, 19, 9, 0)}, now); err == nil {
		t.Error("b was left out while only a's light was coming up")
	}
	if got := config.Get().Alarms.Skipped; len(got) != 0 {
		t.Errorf("a refused request left a mark: %+v", got)
	}

	// Inside b's own light it is offered, at its own time.
	if token, ok := a.Skippable(slot(2026, time.October, 19, 8, 45)); !ok || token.ID != "b" || !token.At.Equal(slot(2026, time.October, 19, 9, 0)) {
		t.Errorf("inside b's light, offered %+v %v, want b at 9:00", token, ok)
	}
}

// The token is checked against the alarm as it is at confirmation: a shifted moment, an unknown or
// deleted alarm, one turned off or moved in between, a helper's entity or a snooze is refused, and
// the same request twice is done once.
func TestSkipTokenIsCheckedAgainBeforeItIsKept(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "wake", Hour: 7, Minute: 0, Days: config.DaysEvery, Label: "Wake up", On: true})

	now := slot(2026, time.October, 19, 6, 45) // inside the light before 7:00
	a := &Alarms{}
	token := skipToken(t, a, now)
	for _, bad := range []SkipToken{
		{ID: token.ID, At: token.At.Add(time.Minute)},
		{ID: "nope", At: token.At},
		// A helper followed from Home Assistant and a snooze are not alarms of the device: neither is
		// a candidate, whatever is asked for.
		{ID: "input_datetime.wake", At: token.At},
		{ID: "snooze:wake", At: token.At},
		{ID: token.ID, At: now.Add(-2 * time.Hour)},
	} {
		if err := a.skipOccurrence(bad, now); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
		if got := config.Get().Alarms.Skipped; len(got) != 0 {
			t.Fatalf("a refused token left a mark: %+v", got)
		}
	}

	al := findAlarm(t, "wake")
	al.On = false
	if err := a.Put(al); err != nil {
		t.Fatal(err)
	}
	if err := a.skipOccurrence(token, now); err == nil {
		t.Error("an alarm turned off between showing and confirming was left out")
	}

	al = findAlarm(t, "wake")
	al.On, al.Hour = true, 8
	if err := a.Put(al); err != nil {
		t.Fatal(err)
	}
	if err := a.skipOccurrence(token, now); err == nil {
		t.Error("an alarm moved between showing and confirming was left out at its old time")
	}

	// Inside the light before 8:00 it is offered anew, saved once, and asking twice is done once.
	later := slot(2026, time.October, 19, 7, 45)
	fresh := skipToken(t, a, later)
	if fresh.ID != "wake" || fresh.At.Hour() != 8 {
		t.Fatalf("offered %+v, want wake at 8:00", fresh)
	}
	if err := a.skipOccurrence(fresh, later); err != nil {
		t.Fatal(err)
	}
	if err := a.skipOccurrence(fresh, later); err != nil {
		t.Errorf("asking twice for the same occurrence failed: %v", err)
	}
	if got := config.Get().Alarms.Skipped; len(got) != 1 {
		t.Errorf("asking twice left %d marks, want 1: %+v", len(got), got)
	}
}

// A token is a moment inside a light: confirmed after that light has gone, or on another day, it
// leaves nothing out.
func TestAStaleTokenLeavesNothingOut(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "wake", Hour: 7, Minute: 0, Days: config.DaysEvery, On: true})

	shown := slot(2026, time.October, 19, 6, 45) // inside the light before 7:00
	a := &Alarms{}
	token := skipToken(t, a, shown)
	if err := a.skipOccurrence(token, shown.Add(15*time.Minute+time.Second)); err == nil {
		t.Error("an occurrence was left out after its light had gone and it had begun")
	}
	if err := a.skipOccurrence(token, shown.Add(24*time.Hour)); err == nil {
		t.Error("yesterday's token left out another day's occurrence")
	}
	if got := config.Get().Alarms.Skipped; len(got) != 0 {
		t.Errorf("a stale token left a mark: %+v", got)
	}
}

// An occurrence the file could not take is not left out: the request says so and nothing holds in
// memory to ring or not ring after a restart differently.
func TestAnOccurrenceThatCouldNotBeSavedIsNotLeftOut(t *testing.T) {
	path := skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "wake", Hour: 7, Minute: 0, Days: config.DaysEvery, On: true})

	now := slot(2026, time.October, 19, 6, 45)
	a := &Alarms{}
	token := skipToken(t, a, now)
	if err := os.Mkdir(path+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := a.skipOccurrence(token, now); err == nil {
		t.Fatal("an occurrence that could not be saved said it was left out")
	}
	if got := config.Get().Alarms.Skipped; len(got) != 0 {
		t.Errorf("a mark that could not be saved held: %+v", got)
	}
	if v := a.View(now); v.Next == nil || !v.Next.At.Equal(token.At) {
		t.Errorf("after the failed request next = %+v, want %v", v.Next, token.At)
	}
	if err := os.Remove(path + ".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := a.skipOccurrence(token, now); err != nil {
		t.Errorf("the same request after the file became writable: %v", err)
	}
}

// A file that was never readable is not an exception: a promise that an occurrence will not ring is
// not made from memory alone, and the file is left as it was.
func TestAnOccurrenceIsNotLeftOutOnAFileThatWasNeverReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	const original = "{not json"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	config.Use(path)
	if config.LoadError() == nil {
		t.Fatal("an unreadable file loaded")
	}
	// Both of these hold in memory for the run, as Update always has; the checked change below must
	// not be the third.
	if err := config.Set().Alarms().SunriseMinutes(30); err == nil {
		t.Fatal("a setting was saved over an unreadable file")
	}
	if err := config.Set().Alarms().Put(config.Alarm{ID: "wake", Hour: 7, Days: config.DaysEvery, On: true}); err == nil {
		t.Fatal("an alarm was saved over an unreadable file")
	}
	if len(config.Get().Alarms.List) != 1 {
		t.Fatal("the alarm did not hold for the run, so there is nothing to ask about")
	}

	now := slot(2026, time.October, 19, 6, 45) // inside the light the alarm has in memory
	a := &Alarms{}
	token := skipToken(t, a, now)
	if err := a.skipOccurrence(token, now); err == nil {
		t.Fatal("an occurrence was reported left out on a file that was never readable")
	}
	if got := config.Get().Alarms.Skipped; len(got) != 0 {
		t.Errorf("a mark was kept only in memory: %+v", got)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != original {
		t.Errorf("the unreadable file was touched: %q %v", b, err)
	}
}

// Leaving an occurrence out waits for the scheduler's look, so it cannot be written after the alarm
// has started to ring, and the look that follows it sees the mark and does not ring it.
func TestLeavingAnOccurrenceOutIsSerializedWithTheScheduler(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "wake", Hour: 7, Minute: 0, Days: config.DaysEvery, On: true})

	now := slot(2026, time.October, 19, 6, 45)
	a := &Alarms{}
	endRing(t, a)
	token := skipToken(t, a, now)

	a.editMu.Lock()
	done := make(chan error, 1)
	go func() { done <- a.skipOccurrence(token, now) }()
	select {
	case err := <-done:
		a.editMu.Unlock()
		t.Fatalf("the occurrence was left out while the scheduler held the gate: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	a.editMu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("leaving the occurrence out did not finish after the gate opened")
	}
	if a.ringDue(token.At.Add(-time.Minute), token.At.Add(time.Minute)) {
		t.Error("the scheduler rang the occurrence that was just left out")
	}
	if a.Ringing() {
		t.Error("something is ringing after the occurrence was left out")
	}
}

// Waiting for the scheduler must not let a request use the time at which it was shown: the clock is
// read once the gate is held, and a request that waited past the alarm's time is refused.
func TestSkipReadsTheClockAfterTheSchedulerGate(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "wake", Hour: 7, Days: config.DaysEvery, On: true})
	a := &Alarms{}
	shown := slot(2026, time.October, 19, 6, 45)
	token := skipToken(t, a, shown)

	var clock atomic.Int64
	clock.Store(shown.UnixNano())
	read := make(chan struct{}, 1)
	done := make(chan error, 1)
	started := make(chan struct{})
	a.editMu.Lock()
	go func() {
		close(started)
		done <- a.leaveOut(token, func() time.Time {
			read <- struct{}{}
			return time.Unix(0, clock.Load()).In(shown.Location())
		})
	}()
	<-started
	select {
	case <-read:
		a.editMu.Unlock()
		<-done
		t.Fatal("the clock was read before the scheduler's gate opened")
	case <-time.After(20 * time.Millisecond):
	}
	clock.Store(token.At.Add(time.Second).UnixNano())
	a.editMu.Unlock()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a request waiting past the alarm's time reported success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the request did not finish after the scheduler's gate opened")
	}
	if marks := config.Get().Alarms.Skipped; len(marks) != 0 {
		t.Fatalf("a refused request left marks: %+v", marks)
	}
}

// Concurrent confirmations of the same shown occurrence all succeed — it is one request — and leave
// exactly one mark.
func TestConcurrentRequestsLeaveOneOccurrenceOutOnce(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "wake", Hour: 7, Minute: 0, Days: config.DaysEvery, On: true})

	now := slot(2026, time.October, 19, 6, 45)
	a := &Alarms{}
	token := skipToken(t, a, now)

	const n = 8
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- a.skipOccurrence(token, now)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("one of the same requests failed: %v", err)
		}
	}
	if got := config.Get().Alarms.Skipped; len(got) != 1 || got[0].ID != "wake" || !got[0].At.Equal(token.At) {
		t.Errorf("marks = %+v, want exactly one for wake", got)
	}
}

// Leaving one alarm's occurrence out changes nothing about another alarm, which still rings as usual.
func TestLeavingOneOccurrenceOutLeavesOtherAlarmsAlone(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "a", Hour: 7, Minute: 0, Days: config.DaysEvery, Label: "A", On: true})
	skipPut(t, config.Alarm{ID: "b", Hour: 8, Minute: 0, Days: config.DaysEvery, Label: "B", On: true})

	now := slot(2026, time.October, 19, 6, 45) // inside a's light
	a := &Alarms{}
	token := skipToken(t, a, now)
	if token.ID != "a" {
		t.Fatalf("offered %+v, want a", token)
	}
	if err := a.skipOccurrence(token, now); err != nil {
		t.Fatal(err)
	}
	c := config.Get().Alarms
	if len(c.List) != 2 || !c.List[0].On || !c.List[1].On || c.List[0].Days != config.DaysEvery || c.List[1].Days != config.DaysEvery {
		t.Errorf("leaving a's occurrence out changed the alarms: %+v", c.List)
	}
	atA, atB := slot(2026, time.October, 19, 7, 0), slot(2026, time.October, 19, 8, 0)
	if got := due(a.sources(atA), atA.Add(-time.Minute), atA.Add(time.Minute)); len(got) != 0 {
		t.Errorf("a rang at the occurrence left out: %+v", got)
	}
	if got := due(a.sources(atB), atB.Add(-time.Minute), atB.Add(time.Minute)); len(got) != 1 || got[0].key != "b" {
		t.Errorf("b did not ring as usual: %+v", got)
	}
}

// Switching the alarm off and on again — from Home Assistant by id or by time — is not an undo of an
// occurrence the user already chose to skip: the mark is kept, across a reload too, and the next
// occurrence after it is still the one that comes.
func TestSwitchingTheAlarmKeepsTheSkippedOccurrence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		switch_ func(a *Alarms, on bool) error
	}{
		{"by id", func(a *Alarms, on bool) error { return a.switchByID(on, "wake") }},
		{"by time", func(a *Alarms, on bool) error { return a.switchFromHA(on, "7:00", "Wake up", "") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := skipState(t)
			if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
				t.Fatal(err)
			}
			skipPut(t, config.Alarm{ID: "wake", Hour: 7, Days: config.DaysEvery, Label: "Wake up", On: true})
			a := &Alarms{}
			now := slot(2026, time.October, 19, 6, 45)
			token := skipToken(t, a, now)
			if err := a.skipOccurrence(token, now); err != nil {
				t.Fatal(err)
			}
			for _, on := range []bool{false, true} {
				if err := tc.switch_(a, on); err != nil {
					t.Fatal(err)
				}
				if got := findAlarm(t, "wake"); got.On != on || got.Hour != 7 || got.Days != config.DaysEvery {
					t.Fatalf("switching changed the alarm unexpectedly: %+v", got)
				}
			}
			config.Use(path)
			c := config.Get().Alarms
			if !hasSkip(c, token.ID, token.At) {
				t.Fatal("off/on and a reload lost the skipped occurrence")
			}
			if got := due(a.sources(token.At), token.At.Add(-time.Minute), token.At.Add(time.Minute)); len(got) != 0 {
				t.Fatalf("the skipped occurrence returned after off/on: %+v", got)
			}
			next := token.At.AddDate(0, 0, 1)
			if v := a.View(now); v.Next == nil || !v.Next.At.Equal(next) {
				t.Fatalf("next occurrence = %+v, want %v", v.Next, next)
			}
		})
	}
}

// A mark is not dropped when another request is made after its moment: a look, or the time the device
// was away, still has to find it then, or the occurrence rings and is called missed. The history is
// one entry per occurrence somebody chose not to be woken by, kept until its alarm is deleted.
func TestAMarkIsKeptUntilItsAlarmIsDeleted(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	// b's own light is two hours, so its window is open once a's occurrence has just gone by.
	skipPut(t, config.Alarm{ID: "a", Hour: 7, Minute: 0, Days: config.DaysEvery, Label: "A", On: true})
	skipPut(t, config.Alarm{ID: "b", Hour: 8, Minute: 0, Days: config.DaysEvery, Sunrise: 120, Label: "B", On: true})

	a := &Alarms{}
	before := slot(2026, time.October, 19, 6, 45) // inside a's light before 7:00
	first := skipToken(t, a, before)
	if first.ID != "a" {
		t.Fatalf("offered %+v, want a", first)
	}
	if err := a.skipOccurrence(first, before); err != nil {
		t.Fatal(err)
	}
	justAfter := slot(2026, time.October, 19, 7, 0).Add(time.Second)
	second := skipToken(t, a, justAfter)
	if second.ID != "b" {
		t.Fatalf("offered %+v, want b", second)
	}
	if err := a.skipOccurrence(second, justAfter); err != nil {
		t.Fatal(err)
	}
	c := config.Get().Alarms
	if len(c.Skipped) != 2 || !mark(c, "a").Equal(first.At) || !mark(c, "b").Equal(second.At) {
		t.Errorf("the later request changed the marks = %+v", c.Skipped)
	}
	// Neither of them was taken for rung or missed while the device was away over both.
	resume, missed := splitAway(a.sources(slot(2026, time.October, 19, 12, 0)), slot(2026, time.October, 18, 20, 0), slot(2026, time.October, 19, 12, 0))
	if len(resume) != 0 || len(missed) != 0 {
		t.Errorf("an occurrence left out was taken for missed: resume %+v, missed %+v", resume, missed)
	}
	if err := a.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if got := config.Get().Alarms.Skipped; len(got) != 1 || got[0].ID != "b" {
		t.Errorf("deleting a left %+v, want only b's mark", got)
	}
}

// Midnight is an ordinary moment, and a mark survives the year boundary: the following midnight and
// the following Monday are found.
func TestSkippingAcrossMidnightAndTheNewYear(t *testing.T) {
	t.Run("midnight", func(t *testing.T) {
		skipState(t)
		if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
			t.Fatal(err)
		}
		skipPut(t, config.Alarm{ID: "midnight", Hour: 0, Minute: 0, Days: config.DaysEvery, On: true})

		now := slot(2026, time.October, 18, 23, 45) // Sunday, inside the light before midnight
		a := &Alarms{}
		token := skipToken(t, a, now)
		monday := slot(2026, time.October, 19, 0, 0)
		if !token.At.Equal(monday) {
			t.Fatalf("offered %v, want %v", token.At, monday)
		}
		if err := a.skipOccurrence(token, now); err != nil {
			t.Fatal(err)
		}
		tuesday := slot(2026, time.October, 20, 0, 0)
		if v := a.View(now); v.Next == nil || !v.Next.At.Equal(tuesday) {
			t.Errorf("next = %+v, want %v", v.Next, tuesday)
		}
		if got := due(a.sources(monday), monday.Add(-time.Minute), monday); len(got) != 0 {
			t.Errorf("midnight left out rang: %+v", got)
		}
		if got := due(a.sources(tuesday), tuesday.Add(-time.Minute), tuesday); len(got) != 1 || got[0].key != "midnight" {
			t.Errorf("the following midnight did not ring: %+v", got)
		}
	})

	t.Run("a weekly one across the new year", func(t *testing.T) {
		skipState(t)
		if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
			t.Fatal(err)
		}
		skipPut(t, config.Alarm{ID: "weekly", Hour: 7, Minute: 0, Days: 1 << time.Monday, On: true})

		now := slot(2026, time.December, 28, 6, 45) // Monday, inside the light before 7:00
		a := &Alarms{}
		if err := a.skipOccurrence(skipToken(t, a, now), now); err != nil {
			t.Fatal(err)
		}
		next := slot(2027, time.January, 4, 7, 0)
		c := config.Get().Alarms
		if got, ok := localSource(c, c.List[0]).next(now); !ok || !got.Equal(next) {
			t.Errorf("next across the new year = %v %v, want %v", got, ok, next)
		}
	})
}

// A skip on the day before a clock change preserves the next wall-clock time, not a fixed 24-hour
// delay: the following morning still rings at its usual time.
func TestASkippedOccurrenceKeepsItsWallTimeAcrossTheClockChange(t *testing.T) {
	for _, tc := range []struct {
		name    string
		month   time.Month
		day     int
		elapsed time.Duration
	}{
		{"spring", time.March, 7, 23 * time.Hour},
		{"fall", time.October, 31, 25 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			skipState(t)
			if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
				t.Fatal(err)
			}
			skipPut(t, config.Alarm{ID: "wake", Hour: 7, Minute: 30, Days: config.DaysEvery, On: true})
			a := &Alarms{}
			now := slot(2026, tc.month, tc.day, 7, 15)
			token := skipToken(t, a, now)
			if err := a.skipOccurrence(token, now); err != nil {
				t.Fatal(err)
			}
			next := token.At.AddDate(0, 0, 1)
			if next.Sub(token.At) != tc.elapsed {
				t.Fatalf("test zone does not cross the expected change: %v", next.Sub(token.At))
			}
			if v := a.View(now); v.Next == nil || !v.Next.At.Equal(next) || v.Next.At.Hour() != 7 || v.Next.At.Minute() != 30 {
				t.Fatalf("next occurrence = %+v, want 07:30 at %v", v.Next, next)
			}
			if got := due(a.sources(next), next.Add(-time.Minute), next.Add(time.Minute)); len(got) != 1 || got[0].key != "wake" {
				t.Fatalf("next occurrence did not ring after the clock change: %+v", got)
			}
		})
	}
}

// Alarms off (D#350) leaves nothing to leave out: an alarm turned off globally is not offered, a
// token from a screen drawn before the switch was turned is refused rather than kept, and nothing is
// written. Turning them back on keeps a mark made before and gives the occurrence after it, so the
// alarm's next morning is not the one somebody left out, and the scheduled sensor says that one too.
func TestAlarmsOffOffersAndAcceptsNoOccurrence(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "wake", Hour: 7, Days: config.DaysEvery, Label: "Wake up", On: true})
	a := &Alarms{}
	now := slot(2026, time.October, 19, 6, 45) // Monday, inside the light before 7:00
	monday := slot(2026, time.October, 19, 7, 0)
	token := skipToken(t, a, now)

	// The switch is turned while the page still shows the button: what it drew is no longer wanted,
	// and the alarm itself is not one that can ring.
	if err := config.Set().Alarms().AllOff(true); err != nil {
		t.Fatal(err)
	}
	if got, ok := a.Skippable(now); ok {
		t.Errorf("an alarm was offered while alarms are off: %+v", got)
	}
	if err := a.skipOccurrence(token, now); err == nil {
		t.Error("a token shown before alarms were turned off was accepted")
	}
	if got := config.Get().Alarms.Skipped; len(got) != 0 {
		t.Fatalf("a refused request left a mark: %+v", got)
	}
	if got := due(a.sources(monday), monday.Add(-time.Minute), monday.Add(time.Minute)); len(got) != 0 {
		t.Errorf("an alarm rings while alarms are off: %+v", got)
	}
	if got := scheduledText(a.sources(now), now); got != "" {
		t.Errorf("the scheduled sensor says %q while alarms are off, want nothing", got)
	}

	// On again: the occurrence can be left out as it was, and the mark is kept across the switch.
	if err := config.Set().Alarms().AllOff(false); err != nil {
		t.Fatal(err)
	}
	if err := a.skipOccurrence(token, now); err != nil {
		t.Fatal(err)
	}
	for _, off := range []bool{true, false} {
		if err := config.Set().Alarms().AllOff(off); err != nil {
			t.Fatal(err)
		}
		if got := config.Get().Alarms.Skipped; len(got) != 1 || !got[0].At.Equal(monday) {
			t.Fatalf("alarms off=%v: marks = %+v, want wake at %v", off, got, monday)
		}
	}
	if got := due(a.sources(monday), monday.Add(-time.Minute), monday.Add(time.Minute)); len(got) != 0 {
		t.Errorf("the occurrence left out rings after off and on: %+v", got)
	}
	tuesday := monday.AddDate(0, 0, 1)
	if v := a.View(now); v.Next == nil || !v.Next.At.Equal(tuesday) {
		t.Errorf("next = %+v, want %v", v.Next, tuesday)
	}
	if got, want := scheduledText(a.sources(now), now), tuesday.UTC().Format(time.RFC3339); got != want {
		t.Errorf("scheduled = %q, want %q", got, want)
	}
}

// An untrusted clock offers and accepts nothing: before NTP has run the RTC can be years out, and a
// mark written then would leave out a morning that is not today's.
func TestSkipRefusesAnUntrustedClock(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "wake", Hour: 7, Days: config.DaysEvery, On: true})
	now := time.Date(2024, time.January, 1, 6, 45, 0, 0, time.UTC)
	a := &Alarms{}
	if token, ok := a.Skippable(now); ok {
		t.Fatalf("offered %+v before the clock was set", token)
	}
	if err := a.skipOccurrence(SkipToken{ID: "wake", At: now.Add(15 * time.Minute)}, now); err == nil {
		t.Fatal("accepted a skip before the clock was set")
	}
	if got := config.Get().Alarms.Skipped; len(got) != 0 {
		t.Fatalf("stored a skip before the clock was set: %+v", got)
	}
}

// A wake light already held for the next occurrence is let go when that occurrence is left out.
func TestLeavingAnOccurrenceOutLetsGoOfALightHeldForIt(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "wake", Hour: 8, Minute: 0, Days: config.DaysEvery, On: true})

	now := slot(2026, time.October, 19, 7, 45) // inside the window before 8:00
	a := &Alarms{}
	a.endLight(a.sources(now), now)
	if a.bridge.key != "wake" {
		t.Fatalf("the light was not held for wake: %+v", a.bridge)
	}
	if err := a.skipOccurrence(skipToken(t, a, now), now); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	held := a.bridge
	a.mu.Unlock()
	if held.key != "" {
		t.Errorf("the light is still held for the occurrence left out: %+v", held)
	}
	if h := config.Get().Alarms.Held; h != nil {
		t.Errorf("the held light is still saved: %+v", h)
	}
}
