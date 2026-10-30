package alarm

import (
	"os"
	"path/filepath"
	"sync"
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

// A daily slot in the test zone.
func slot(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, testZone)
}

// One occurrence of a recurring alarm is left out from inside the light before it, and only that
// one: the alarm stays on with the same days, does not ring or light that morning, and its next
// morning is its usual one after a restart as well.
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
	if len(c.Skipped) != 1 || c.Skipped[0].ID != "wake" || !c.Skipped[0].At.Equal(monday) {
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

// A weekly alarm whose next occurrence is left out must still find the following week, which from
// the day before the first is eight days away.
func TestTheWeekAfterASkippedWeeklyOccurrenceIsStillFound(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "weekly", Hour: 7, Minute: 0, Days: 1 << time.Monday, Label: "Monday", On: true})

	now := slot(2026, time.October, 19, 6, 45) // Monday, inside the light before 7:00
	monday := slot(2026, time.October, 19, 7, 0)
	a := &Alarms{}
	token := skipToken(t, a, now)
	if !token.At.Equal(monday) {
		t.Fatalf("offered %v, want %v", token.At, monday)
	}
	if err := a.skipOccurrence(token, now); err != nil {
		t.Fatal(err)
	}

	c := config.Get().Alarms
	src := localSource(c, c.List[0])
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
}

// Marks stay until their alarm goes, so a weekly one can have several behind it: the search steps
// over every one of them and still finds its next Monday.
func TestSeveralSkippedWeeksAreSteppedOver(t *testing.T) {
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
}

// The occurrence among the windows open now whose light is furthest along is offered, and two an even
// way along go to the earlier moment, then to the lower id. A window that has closed is not a
// candidate, however early its alarm is.
func TestSkippableChoosesTheOpenRampFurthestAlong(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	for _, al := range []config.Alarm{
		{ID: "z", Hour: 7, Minute: 0, Days: config.DaysEvery, Sunrise: 60, On: true},
		{ID: "a", Hour: 7, Minute: 0, Days: config.DaysEvery, Sunrise: 60, On: true},
		{ID: "b", Hour: 6, Minute: 30, Days: config.DaysEvery, Sunrise: 60, On: true},
	} {
		skipPut(t, al)
	}
	now := slot(2026, time.October, 19, 6, 10) // all three windows are open
	a := &Alarms{}
	if token := skipToken(t, a, now); token.ID != "b" || !token.At.Equal(slot(2026, time.October, 19, 6, 30)) {
		t.Fatalf("offered %+v, want the earliest, b", token)
	}

	b := findAlarm(t, "b")
	b.On = false
	if err := a.Put(b); err != nil {
		t.Fatal(err)
	}
	if token := skipToken(t, a, now); token.ID != "a" || !token.At.Equal(slot(2026, time.October, 19, 7, 0)) {
		t.Errorf("with the earliest off, offered %+v, want a by id", token)
	}

	// Back on, but its window has closed by 6:40: a's is the one open now.
	b = findAlarm(t, "b")
	b.On = true
	if err := a.Put(b); err != nil {
		t.Fatal(err)
	}
	if token := skipToken(t, a, slot(2026, time.October, 19, 6, 40)); token.ID != "a" {
		t.Errorf("with b's window closed, offered %+v, want a", token)
	}
}

// The open window offered is the one furthest along, not the one whose alarm is earliest: at 6:57 the
// light before a 7:00 alarm that lasts five minutes has two of them behind it, and the light before a
// 7:10 alarm that lasts thirty has seventeen, so the room is mostly the later alarm's light and that is
// the occurrence offered. Windows evenly far along go to the earlier occurrence — of two equally
// advanced lights, the one that will end first — and two at the same moment to the lower id, whatever
// order the list is in. The first moment of a window is open, a window already left out is not the ramp
// it was, a light-only alarm is a ramp like any other, and alarms off leave nothing to offer.
func TestRampFractionsAreComparedExactly(t *testing.T) {
	// The products exceed int64, and the last two fractions round to the same float64.
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
			name: "two lights halfway along go to the earlier occurrence",
			now:  day(6, 56),
			alarms: []config.Alarm{
				{ID: "a", Hour: 7, Minute: 0, Sunrise: 8, Label: "A"},
				{ID: "b", Hour: 6, Minute: 58, Sunrise: 4, Label: "B"},
			},
			wantID: "b", wantAt: day(6, 58),
		},
		{
			name: "halfway tie with the list reversed",
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
			name: "the same moment, the list the other way round",
			now:  day(6, 45),
			alarms: []config.Alarm{
				{ID: "a", Hour: 7, Minute: 0, Sunrise: 30, Label: "A"},
				{ID: "c", Hour: 7, Minute: 0, Sunrise: 30, Label: "C"},
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
			name: "a light-only alarm's ramp is a ramp like any other",
			now:  day(6, 57),
			alarms: []config.Alarm{
				{ID: "short", Hour: 7, Minute: 0, Sunrise: 5, Label: "Short light"},
				{ID: "light", Hour: 7, Minute: 10, Sunrise: 45, Label: "Morning light", LightOnly: true},
			},
			wantID: "light", wantAt: day(7, 10),
		},
		{
			name: "an occurrence already left out is not the ramp it was",
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

// A recurring alarm of the device's that is on and has a wake light can have an occurrence left
// out; a one-off, an alarm off, a reminder and a silent alarm cannot, and one with no light at all
// has no ramp to leave out. Its own window is asked about as well, and for these there is none: a
// 7:00 alarm at 6:45 has its light coming up for the ones that may be left out, and these are not the
// kind. A light-only alarm has its own light and is left out from inside it too, which
// skip_light_only_test.go covers.
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

	// With a off, nothing is coming up at 6:45: b's own light begins at 8:30.
	off := findAlarm(t, "a")
	off.On = false
	if err := a.Put(off); err != nil {
		t.Fatal(err)
	}
	if token, ok := a.Skippable(now); ok {
		t.Errorf("b was offered at %v, before its own light: %+v", now, token)
	}
	if err := a.skipOccurrence(SkipToken{ID: "b", At: slot(2026, time.October, 19, 9, 0)}, now); err == nil {
		t.Error("b was left out before its own light began")
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
// deleted alarm, or one turned off or moved in between is refused, and the same request twice is
// done once.
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
// leaves nothing out, and the check is made when the request is processed rather than when it was
// shown.
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
	if got := config.Get().Alarms.Skipped; len(got) != 0 {
		t.Errorf("a stale token left a mark: %+v", got)
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

// The file never being readable is not an exception: a promise that an occurrence will not ring is
// not made from memory alone. This is the case UpdateOrUndo used to hold; the checked change here
// must not.
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
		t.Errorf("a mark was kept in memory only: %+v", got)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != original {
		t.Errorf("the unreadable file was touched: %q %v", b, err)
	}
}

// Leaving an occurrence out waits for the scheduler's look, so it cannot be written after the alarm
// has started to ring, and the look that follows sees it and does not ring it.
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

// Leaving one alarm's occurrence out changes nothing about another alarm.
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

// A mark is not dropped when another request is made after its moment: a look, or the time the
// device was away, still has to find it then, or the occurrence rings and is called missed.
func TestALaterRequestKeepsAnEarlierMark(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	// b's own light is two hours, so its window is open once a's occurrence has just gone by.
	skipPut(t, config.Alarm{ID: "a", Hour: 7, Minute: 0, Days: config.DaysEvery, Label: "A", On: true})
	skipPut(t, config.Alarm{ID: "b", Hour: 8, Minute: 0, Days: config.DaysEvery, Sunrise: 120, Label: "B", On: true})

	a := &Alarms{}
	before := slot(2026, time.October, 19, 6, 45) // inside a's light before 7:00
	token := skipToken(t, a, before)
	if token.ID != "a" || !token.At.Equal(slot(2026, time.October, 19, 7, 0)) {
		t.Fatalf("offered %+v, want a at 7:00", token)
	}
	if err := a.skipOccurrence(token, before); err != nil {
		t.Fatal(err)
	}

	// A second request a second after a's time, from inside b's light.
	justAfter := slot(2026, time.October, 19, 7, 0).Add(time.Second)
	token = skipToken(t, a, justAfter)
	if token.ID != "b" || !token.At.Equal(slot(2026, time.October, 19, 8, 0)) {
		t.Fatalf("offered %+v, want b at 8:00", token)
	}
	if err := a.skipOccurrence(token, justAfter); err != nil {
		t.Fatal(err)
	}

	c := config.Get().Alarms
	if len(c.Skipped) != 2 || !hasSkip(c, "a", slot(2026, time.October, 19, 7, 0)) || !hasSkip(c, "b", slot(2026, time.October, 19, 8, 0)) {
		t.Errorf("the later request changed the marks = %+v, want a at 7:00 and b at 8:00", c.Skipped)
	}
	if got := due(a.sources(justAfter), justAfter.Add(-2*time.Second), justAfter); len(got) != 0 {
		t.Errorf("a rang for the occurrence its mark was written for: %+v", got)
	}
	if a.Ringing() {
		t.Error("something is ringing")
	}
	resume, missed := splitAway(a.sources(slot(2026, time.October, 19, 7, 10)), slot(2026, time.October, 18, 20, 0), slot(2026, time.October, 19, 7, 10))
	if len(resume) != 0 || len(missed) != 0 {
		t.Errorf("the occurrence left out was taken for rung or missed: resume %+v, missed %+v", resume, missed)
	}
	if got := due(a.sources(slot(2026, time.October, 19, 8, 0).Add(time.Second)), slot(2026, time.October, 19, 7, 59), slot(2026, time.October, 19, 8, 0).Add(time.Second)); len(got) != 0 {
		t.Errorf("b rang at the occurrence left out: %+v", got)
	}
}

// A mark is the device's own record of what it left out: it stays, its morning behind it included,
// until the alarm it belongs to is deleted. That is all its growth: one entry per occurrence
// somebody chose not to be woken by.
func TestAMarkIsKeptUntilItsAlarmIsDeleted(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "wake", Hour: 7, Minute: 0, Days: config.DaysEvery, On: true})

	a := &Alarms{}
	monday := slot(2026, time.October, 19, 6, 45)
	if err := a.skipOccurrence(skipToken(t, a, monday), monday); err != nil {
		t.Fatal(err)
	}
	tuesday := slot(2026, time.October, 20, 6, 45)
	if err := a.skipOccurrence(skipToken(t, a, tuesday), tuesday); err != nil {
		t.Fatal(err)
	}
	marks := config.Get().Alarms.Skipped
	if len(marks) != 2 || !marks[0].At.Equal(slot(2026, time.October, 19, 7, 0)) || !marks[1].At.Equal(slot(2026, time.October, 20, 7, 0)) {
		t.Errorf("marks = %+v, want both mornings, the one behind included", marks)
	}
	if err := a.Delete("wake"); err != nil {
		t.Fatal(err)
	}
	if got := config.Get().Alarms.Skipped; len(got) != 0 {
		t.Errorf("deleting the alarm left its marks: %+v", got)
	}
}

// Midnight is an ordinary moment: the occurrence at 00:00 is left out from inside the light before
// it, and the following day's is found.
func TestSkippingAnOccurrenceAtMidnight(t *testing.T) {
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
}

// The wall time survives the autumn clock change: a 7:30 alarm left out on the morning the clock
// goes back still rings at 7:30 the next day.
func TestSkippingAcrossTheClockChange(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "wake", Hour: 7, Minute: 30, Days: config.DaysEvery, On: true})

	now := slot(2026, time.November, 1, 7, 15) // Sunday, the clock went back at 2:00; inside the light
	a := &Alarms{}
	token := skipToken(t, a, now)
	sunday := slot(2026, time.November, 1, 7, 30)
	if !token.At.Equal(sunday) {
		t.Fatalf("offered %v, want %v", token.At, sunday)
	}
	if err := a.skipOccurrence(token, now); err != nil {
		t.Fatal(err)
	}
	monday := slot(2026, time.November, 2, 7, 30)
	c := config.Get().Alarms
	if got, ok := localSource(c, c.List[0]).next(now); !ok || !got.Equal(monday) || got.Hour() != 7 || got.Minute() != 30 {
		t.Errorf("next after the clock change = %v %v, want 7:30 on %v", got, ok, monday)
	}
	if got := due(a.sources(sunday), sunday.Add(-time.Minute), sunday.Add(time.Minute)); len(got) != 0 {
		t.Errorf("the morning left out rang: %+v", got)
	}
	if got := due(a.sources(monday), monday.Add(-time.Minute), monday.Add(time.Minute)); len(got) != 1 || got[0].key != "wake" {
		t.Errorf("the next morning did not ring: %+v", got)
	}
}

// The listing Home Assistant reads shows the next occurrence after the one left out.
func TestTheListingShowsTheNextOccurrenceAfterASkip(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "wake", Hour: 7, Minute: 0, Days: config.DaysEvery, On: true})

	now := slot(2026, time.October, 19, 6, 45)
	a := &Alarms{}
	token := skipToken(t, a, now)
	if err := a.skipOccurrence(token, now); err != nil {
		t.Fatal(err)
	}
	l := listing(now, a.View(now), nil)
	if len(l.Alarms) != 1 {
		t.Fatalf("listing has %d alarms", len(l.Alarms))
	}
	want := slot(2026, time.October, 20, 7, 0).Format(time.RFC3339)
	if l.Alarms[0].Next != want {
		t.Errorf("listed next = %q, want %q", l.Alarms[0].Next, want)
	}
}

// An occurrence left out before the device was away is not called missed when it comes back.
func TestASkippedOccurrenceIsNotMissedWhileTheDeviceWasAway(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "wake", Hour: 7, Minute: 0, Days: config.DaysEvery, On: true})

	a := &Alarms{}
	token := skipToken(t, a, slot(2026, time.October, 19, 6, 45))
	if err := a.skipOccurrence(token, slot(2026, time.October, 19, 6, 45)); err != nil {
		t.Fatal(err)
	}
	since := slot(2026, time.October, 18, 20, 0)
	back := slot(2026, time.October, 19, 12, 0)
	resume, missed := splitAway(a.sources(back), since, back)
	if len(resume) != 0 || len(missed) != 0 {
		t.Errorf("the occurrence left out was taken for missed: resume %+v, missed %+v", resume, missed)
	}
}

// Deleting the alarm forgets the occurrence it had left out.
func TestDeletingAnAlarmForgetsTheOccurrenceLeftOut(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "wake", Hour: 7, Minute: 0, Days: config.DaysEvery, On: true})
	now := slot(2026, time.October, 19, 6, 45)
	a := &Alarms{}
	if err := a.skipOccurrence(skipToken(t, a, now), now); err != nil {
		t.Fatal(err)
	}
	if err := a.Delete("wake"); err != nil {
		t.Fatal(err)
	}
	if got := config.Get().Alarms.Skipped; len(got) != 0 {
		t.Errorf("deleting the alarm left its mark: %+v", got)
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
	token := skipToken(t, a, now)
	if err := a.skipOccurrence(token, now); err != nil {
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

// A skipped week across the new year is still one week, and the following Monday is found.
func TestASkippedWeekAcrossTheNewYearStillFindsTheNext(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(30); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "weekly", Hour: 7, Minute: 0, Days: 1 << time.Monday, On: true})

	now := slot(2026, time.December, 28, 6, 45) // Monday, inside the light before 7:00
	monday := slot(2026, time.December, 28, 7, 0)
	a := &Alarms{}
	token := skipToken(t, a, now)
	if !token.At.Equal(monday) {
		t.Fatalf("offered %v, want %v", token.At, monday)
	}
	if err := a.skipOccurrence(token, now); err != nil {
		t.Fatal(err)
	}
	next := slot(2027, time.January, 4, 7, 0)
	c := config.Get().Alarms
	if got, ok := localSource(c, c.List[0]).next(now); !ok || !got.Equal(next) {
		t.Errorf("next across the new year = %v %v, want %v", got, ok, next)
	}
}
