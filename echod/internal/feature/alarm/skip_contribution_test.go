package alarm

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Waiting for the scheduler must not let a request use the time at which it was
// shown: once the gate opens past the alarm's time, there is nothing left to skip.
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

// Switching the alarm through Home Assistant is not an undo of an occurrence the
// user already chose to skip. Re-enabling it keeps the next scheduled occurrence.
func TestHomeAssistantSwitchingKeepsTheSkippedOccurrence(t *testing.T) {
	for _, byID := range []bool{false, true} {
		name := "time"
		if byID {
			name = "id"
		}
		t.Run(name, func(t *testing.T) {
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
				var err error
				if byID {
					err = a.switchByID(on, "wake")
				} else {
					err = a.switchFromHA(on, "7:00", "Wake up", "")
				}
				if err != nil {
					t.Fatal(err)
				}
				if got := findAlarm(t, "wake"); got.On != on || got.Hour != 7 || got.Days != config.DaysEvery {
					t.Fatalf("switch changed the alarm unexpectedly: %+v", got)
				}
			}
			config.Use(path)
			c := config.Get().Alarms
			if !hasSkip(c, token.ID, token.At) {
				t.Fatal("off/on and reload lost the skipped occurrence")
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

// A skip on the day before a DST transition preserves the next wall-clock time,
// not a fixed 24-hour delay. Ambiguous and missing clock times retain the
// scheduler's existing policy; these alarms use an unambiguous 07:30.
func TestASkipKeepsWallTimeAcrossShortAndLongNights(t *testing.T) {
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
				t.Fatalf("test zone does not cross the expected DST transition: %v", next.Sub(token.At))
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
