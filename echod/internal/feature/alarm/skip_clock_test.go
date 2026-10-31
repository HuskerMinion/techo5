package alarm

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

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
