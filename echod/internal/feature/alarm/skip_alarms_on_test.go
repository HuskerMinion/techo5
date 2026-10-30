package alarm

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Alarms off (D#350) leaves nothing to leave out: an alarm turned off globally is not offered, a token
// from a screen drawn before the switch was turned is refused rather than kept, and nothing is written.
// Turning them back on keeps a mark made before and gives the occurrence after it, so the alarm's next
// morning is not the one somebody left out, and the Next scheduled alarm sensor says that one too.
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
	if token.ID != "wake" || !token.At.Equal(monday) {
		t.Fatalf("offered %+v, want wake at %v", token, monday)
	}

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
		if got := config.Get().Alarms.Skipped; len(got) != 1 || got[0].ID != "wake" || !got[0].At.Equal(monday) {
			t.Fatalf("alarms off=%v: marks = %+v, want wake at %v", off, got, monday)
		}
	}

	// The occurrence left out still does not ring, and the one that comes is the day after it: the
	// alarm's own days and switch were never touched.
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
