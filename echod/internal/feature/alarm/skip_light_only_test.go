package alarm

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A light-only alarm is a wake light and no more, and its light is a morning like any other: the
// ramp before it is the moment somebody knows which morning they would rather not be woken by, so
// the same occurrence is left out from inside its own light. The mark is saved before the request is
// answered, keeps the alarm on with the same days, stops that one ramp and leaves the next
// occurrence — the next day, for a daily alarm — its usual one. Its time is where it would have
// ended the night, so that too is not reached.
func TestALightOnlyAlarmsOwnRampCanBeLeftOut(t *testing.T) {
	path := skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(15); err != nil {
		t.Fatal(err)
	}
	// Its own twenty minutes win over the default of fifteen.
	skipPut(t, config.Alarm{ID: "light", Hour: 6, Minute: 30, Days: config.DaysEvery,
		Label: "Morning light", On: true, LightOnly: true, Sunrise: 20})

	a := &Alarms{}
	now := slot(2026, time.October, 19, 6, 20) // Monday, half way up to 6:30
	monday := slot(2026, time.October, 19, 6, 30)
	if got := a.SunriseProgress(now); got <= 0 {
		t.Fatalf("the light-only alarm's own light was not coming up before it: %.2f", got)
	}
	token := skipToken(t, a, now)
	if token.ID != "light" || !token.At.Equal(monday) {
		t.Fatalf("offered %+v, want light at %v", token, monday)
	}
	if err := a.skipOccurrence(token, now); err != nil {
		t.Fatal(err)
	}

	// The mark written is that alarm and that moment, and the alarm itself is untouched: one skip is
	// not the alarm turned off, moved or made something else.
	c := config.Get().Alarms
	if len(c.Skipped) != 1 || c.Skipped[0].ID != "light" || !c.Skipped[0].At.Equal(monday) {
		t.Errorf("saved marks = %+v, want light at %v", c.Skipped, monday)
	}
	al := findAlarm(t, "light")
	if !al.On || al.Days != config.DaysEvery || !al.LightOnly || al.Sunrise != 20 {
		t.Errorf("leaving an occurrence out changed its alarm: %+v", al)
	}

	// That morning's ramp is gone; nothing rang, and nothing held the light at full after a time that
	// never came, so the night is not ended either.
	if got := a.SunriseProgress(now); got != 0 {
		t.Errorf("the light came up for the occurrence left out: %.2f", got)
	}
	if got := due(a.sources(monday), monday.Add(-time.Minute), monday.Add(time.Minute)); len(got) != 0 {
		t.Errorf("the occurrence left out was fired: %+v", got)
	}
	if until, ok := a.LightOnlyGlow(monday); ok {
		t.Errorf("the light was held after an occurrence left out, until %v", until)
	}

	// The next morning is ordinary: it comes up and is offered again from inside its light.
	tuesday := slot(2026, time.October, 20, 6, 30)
	if got := a.SunriseProgress(tuesday.Add(-10 * time.Minute)); got != 0.5 {
		t.Errorf("the next morning's light is %.2f of the way, want 0.50", got)
	}
	if next, ok := a.Skippable(tuesday.Add(-10 * time.Minute)); !ok || !next.At.Equal(tuesday) {
		t.Errorf("the next occurrence offered = %+v %v, want %v", next, ok, tuesday)
	}

	// A restart reads the mark back: the same morning stays dark, the next one comes up as usual.
	config.Use(path)
	again := &Alarms{}
	if got := config.Get().Alarms.Skipped; len(got) != 1 || !got[0].At.Equal(monday) {
		t.Fatalf("the mark did not come back after a restart: %+v", got)
	}
	if got := again.SunriseProgress(now); got != 0 {
		t.Errorf("after a restart the occurrence left out lit the room: %.2f", got)
	}
	if got := due(again.sources(monday), monday.Add(-time.Minute), monday.Add(time.Minute)); len(got) != 0 {
		t.Errorf("after a restart the occurrence left out was fired: %+v", got)
	}
	if got := again.SunriseProgress(tuesday.Add(-10 * time.Minute)); got != 0.5 {
		t.Errorf("after a restart the next morning's light is %.2f of the way, want 0.50", got)
	}
	if next, ok := again.Skippable(tuesday.Add(-10 * time.Minute)); !ok || !next.At.Equal(tuesday) {
		t.Errorf("after a restart the occurrence offered = %+v %v, want %v", next, ok, tuesday)
	}
}

// A light-only alarm's window is the one its light has (config.Alarms.SunriseFor): its own minutes,
// the device's default, or the fallback when there is nothing else — not a length of its own. The
// offer opens at that many minutes before it and not a minute sooner.
func TestALightOnlyAlarmsWindowIsTheOneItsLightHas(t *testing.T) {
	for _, tc := range []struct {
		name           string
		def, own, want int
	}{
		{"its own light", 15, 20, 20},
		{"the device's default", 15, config.SunriseOff, 15},
		{"the fallback with no default", 0, config.SunriseOff, config.LightOnlyMinutes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			skipState(t)
			if err := config.Set().Alarms().SunriseMinutes(tc.def); err != nil {
				t.Fatal(err)
			}
			al := config.Alarm{ID: "light", Hour: 7, Days: config.DaysEvery, On: true, LightOnly: true, Sunrise: tc.own}
			skipPut(t, al)
			if got := config.Get().Alarms.SunriseFor(al); got != tc.want {
				t.Fatalf("the light-only alarm's light is %d minutes, want %d", got, tc.want)
			}
			at := slot(2026, time.October, 19, 7, 0)
			a := &Alarms{}
			if token, ok := a.Skippable(at.Add(-time.Duration(tc.want) * time.Minute)); !ok || !token.At.Equal(at) {
				t.Errorf("at the first moment of its window offered %+v %v, want %v", token, ok, at)
			}
			if token, ok := a.Skippable(at.Add(-time.Duration(tc.want+1) * time.Minute)); ok {
				t.Errorf("a minute before its window opened offered %+v, want nothing", token)
			}
		})
	}
}

// The rules that were already the skip's are the light-only alarm's too: alarms on, the alarm on,
// repeating, its own light before that very occurrence, and the token used as it was shown.
func TestTheSameRulesHoldForALightOnlyAlarm(t *testing.T) {
	put := func(t *testing.T) {
		t.Helper()
		skipState(t)
		if err := config.Set().Alarms().SunriseMinutes(15); err != nil {
			t.Fatal(err)
		}
		skipPut(t, config.Alarm{ID: "light", Hour: 7, Days: config.DaysEvery, Label: "Morning light",
			On: true, LightOnly: true, Sunrise: 20})
	}
	now := slot(2026, time.October, 19, 6, 50) // inside the light before 7:00
	seven := slot(2026, time.October, 19, 7, 0)

	t.Run("alarms off", func(t *testing.T) {
		put(t)
		a := &Alarms{}
		token := skipToken(t, a, now)
		if err := config.Set().Alarms().AllOff(true); err != nil {
			t.Fatal(err)
		}
		if got, ok := a.Skippable(now); ok {
			t.Errorf("a light-only alarm was offered while alarms are off: %+v", got)
		}
		if err := a.skipOccurrence(token, now); err == nil {
			t.Error("a light-only token shown before alarms were turned off was accepted")
		}
		if got := config.Get().Alarms.Skipped; len(got) != 0 {
			t.Errorf("a refused request left a mark: %+v", got)
		}
	})

	t.Run("turned off since the offer", func(t *testing.T) {
		put(t)
		a := &Alarms{}
		token := skipToken(t, a, now)
		al := findAlarm(t, "light")
		al.On = false
		if err := config.Set().Alarms().Put(al); err != nil {
			t.Fatal(err)
		}
		if err := a.skipOccurrence(token, now); err == nil {
			t.Error("a light-only occurrence the alarm no longer has was left out")
		}
		if got := config.Get().Alarms.Skipped; len(got) != 0 {
			t.Errorf("a refused request left a mark: %+v", got)
		}
	})

	t.Run("at the moment it would end the night", func(t *testing.T) {
		put(t)
		a := &Alarms{}
		if token, ok := a.Skippable(seven); ok {
			t.Errorf("offered %+v at the alarm's own time, want nothing", token)
		}
		if err := a.skipOccurrence(SkipToken{ID: "light", At: seven}, seven); err == nil {
			t.Error("a light-only occurrence was left out at the moment it had begun")
		}
		if got := config.Get().Alarms.Skipped; len(got) != 0 {
			t.Errorf("a refused request left a mark: %+v", got)
		}
	})

	t.Run("a moment that is not the one shown", func(t *testing.T) {
		put(t)
		a := &Alarms{}
		token := skipToken(t, a, now)
		if err := a.skipOccurrence(SkipToken{ID: token.ID, At: token.At.Add(time.Minute)}, now); err == nil {
			t.Error("a token naming another moment was accepted")
		}
		if got := config.Get().Alarms.Skipped; len(got) != 0 {
			t.Errorf("a refused request left a mark: %+v", got)
		}
	})

	for _, tc := range []struct {
		name string
		al   config.Alarm
	}{
		{"a one-off light-only", config.Alarm{ID: "light", Hour: 7, Days: config.DaysOnce, On: true, LightOnly: true}},
		{"a silent light-only", config.Alarm{ID: "light", Hour: 7, Days: config.DaysEvery, On: true, LightOnly: true, Silent: true}},
		{"a light-only reminder", config.Alarm{ID: "light", Hour: 7, Days: config.DaysEvery, On: true, LightOnly: true, Remind: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			skipState(t)
			if err := config.Set().Alarms().SunriseMinutes(15); err != nil {
				t.Fatal(err)
			}
			skipPut(t, tc.al)
			a := &Alarms{}
			if token, ok := a.Skippable(now); ok {
				t.Errorf("offered %+v, want nothing", token)
			}
			if err := a.skipOccurrence(SkipToken{ID: "light", At: seven}, now); err == nil {
				t.Error("an occurrence that cannot be left out was")
			}
			if got := config.Get().Alarms.Skipped; len(got) != 0 {
				t.Errorf("a refused request left a mark: %+v", got)
			}
		})
	}
}

// Leaving a light-only occurrence out is that alarm's own light and nothing else's: an alarm beside
// it keeps its switch, its light and its ring, and is offered from inside its own window as before.
func TestSkippingALightOnlyAlarmLeavesAnotherAlarmAlone(t *testing.T) {
	skipState(t)
	if err := config.Set().Alarms().SunriseMinutes(15); err != nil {
		t.Fatal(err)
	}
	skipPut(t, config.Alarm{ID: "light", Hour: 6, Minute: 30, Days: config.DaysEvery,
		Label: "Morning light", On: true, LightOnly: true, Sunrise: 20})
	skipPut(t, config.Alarm{ID: "wake", Hour: 7, Minute: 0, Days: config.DaysEvery,
		Label: "Wake up", On: true, Sunrise: 20})

	a := &Alarms{}
	now := slot(2026, time.October, 19, 6, 20) // the light-only window only: 6:10 to 6:30
	token := skipToken(t, a, now)
	if token.ID != "light" {
		t.Fatalf("offered %+v, want the light-only alarm", token)
	}
	if err := a.skipOccurrence(token, now); err != nil {
		t.Fatal(err)
	}

	// The other alarm is untouched and its own light still comes up when its window opens.
	al := findAlarm(t, "wake")
	if !al.On || al.LightOnly || al.Hour != 7 {
		t.Errorf("the alarm beside the skip changed: %+v", al)
	}
	if got := a.SunriseProgress(slot(2026, time.October, 19, 6, 45)); got != 0.25 {
		t.Errorf("the other alarm's light is %.2f of the way, want 0.25", got)
	}
	seven := slot(2026, time.October, 19, 7, 0)
	if got := due(a.sources(seven), seven.Add(-time.Minute), seven.Add(time.Minute)); len(got) != 1 || got[0].key != "wake" {
		t.Errorf("the other alarm did not ring: %+v", got)
	}
	if next, ok := a.Skippable(slot(2026, time.October, 19, 6, 50)); !ok || next.ID != "wake" || !next.At.Equal(seven) {
		t.Errorf("the other alarm offered = %+v %v, want wake at %v", next, ok, seven)
	}

	// And the light-only alarm's own next morning is still its light.
	if got := a.SunriseProgress(slot(2026, time.October, 20, 6, 20)); got != 0.5 {
		t.Errorf("the next morning's light is %.2f of the way, want 0.50", got)
	}
}
