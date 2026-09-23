package ring

import (
	"testing"
	"time"
)

// at points the package clock at a time the test moves by hand, and puts the real one back after.
func at(t *testing.T, clock *time.Time) {
	t.Helper()
	was := state.now
	state.now = func() time.Time { return *clock }
	t.Cleanup(func() {
		state.now = was
		state.mu.Lock()
		state.sounding, state.until = 0, time.Time{}
		state.mu.Unlock()
	})
}

func TestAHushExpiresByItself(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	at(t, &now)

	done := Sounding()
	defer done()

	if Hushed() {
		t.Fatal("hushed before anything hushed it")
	}

	Hush()
	if !Hushed() {
		t.Fatal("not hushed after a hush")
	}

	// The deadline is the whole safety property: a hush that is never lifted still ends, so no bug
	// can leave an alarm permanently silent.
	now = now.Add(HushFor)
	if Hushed() {
		t.Fatalf("still hushed %v later; a hush must expire by itself", HushFor)
	}
}

// Hushing while nothing is ringing must not arm a hush that lands on the next alarm.
func TestAHushWithNothingRingingDoesNothing(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	at(t, &now)

	Hush()
	if Hushed() {
		t.Fatal("a hush with nothing ringing should do nothing")
	}

	done := Sounding()
	defer done()
	if Hushed() {
		t.Fatal("a ring started hushed, so its first chime would be swallowed")
	}
}

// An alarm and a timer going off together are two rings. The first to finish must not report
// silence while the other is still sounding.
func TestTwoRingsAreBothCounted(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	at(t, &now)

	alarm := Sounding()
	timer := Sounding()

	if !IsSounding() {
		t.Fatal("two rings started and nothing is sounding")
	}
	alarm()
	if !IsSounding() {
		t.Fatal("the alarm finished but the timer is still ringing")
	}
	timer()
	if IsSounding() {
		t.Fatal("both rings finished and something is still sounding")
	}
}

// The last ring to finish clears the hush, so a hush cannot carry into the next alarm.
func TestTheHushDoesNotOutliveTheRing(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	at(t, &now)

	done := Sounding()
	Hush()
	if !Hushed() {
		t.Fatal("not hushed after a hush")
	}
	done()

	if Hushed() {
		t.Fatal("the hush outlived the ring")
	}

	next := Sounding()
	defer next()
	if Hushed() {
		t.Fatal("the next ring started hushed")
	}
}

// Calling done twice must not drive the count negative and make a live ring report silence.
func TestDoneIsIdempotent(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	at(t, &now)

	first := Sounding()
	second := Sounding()

	first()
	first()

	if !IsSounding() {
		t.Fatal("a second done call cancelled a ring that is still sounding")
	}
	second()
	if IsSounding() {
		t.Fatal("still sounding after every ring finished")
	}
}
