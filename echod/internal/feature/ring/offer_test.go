package ring

import (
	"testing"
	"time"
)

// engines stands in for alarm and timer registering what they can do, and counts what was called.
type engines struct {
	silenced, snoozed int
}

func (e *engines) register(snooze bool) {
	Silences(func() bool { e.silenced++; return true })
	if snooze {
		Snoozes(func() bool { e.snoozed++; return true })
	}
}

// The first press quiets the ring without ending it, because a ring that had already ended would
// have nothing left to snooze. Silence is the urgent act; the decision can wait.
func TestTheFirstPressQuietsTheRingWithoutEndingIt(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	at(t, &now)
	var e engines
	e.register(true)

	done := Sounding()
	defer done()

	if Quiet() {
		t.Fatal("quiet before anything quieted it")
	}
	if !Silence() {
		t.Fatal("a ring was sounding and Silence said there was none")
	}
	if !Quiet() {
		t.Error("the chime is not held back after a press")
	}
	if !Offered() {
		t.Error("nothing is being offered, so a second press cannot mean yes")
	}
	if Lapsed() {
		t.Error("the offer lapsed the instant it was made")
	}
	if e.silenced != 0 || e.snoozed != 0 {
		t.Errorf("the ring was ended by the first press (silenced %d, snoozed %d); it should only go quiet",
			e.silenced, e.snoozed)
	}
}

// Ignoring the offer leaves it stopped: a snooze nobody confirmed is the surprising outcome.
func TestAnUnansweredOfferStopsTheRing(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	at(t, &now)
	var e engines
	e.register(true)

	done := Sounding()
	defer done()
	Silence()

	now = now.Add(OfferFor - time.Millisecond)
	if Lapsed() {
		t.Fatal("the offer lapsed early")
	}
	if !Offered() {
		t.Fatal("the offer was withdrawn before it ran out")
	}

	now = now.Add(time.Millisecond)
	if !Lapsed() {
		t.Fatal("the offer ran out and the ring was not told to stop")
	}
	if Offered() {
		t.Error("a lapsed offer is still being offered; a press now would snooze rather than stop")
	}
	// The ring stays quiet on the way out — it must not chime once more before it ends.
	if !Quiet() {
		t.Error("a lapsed ring chimed again before stopping")
	}
}

// A second press inside the window snoozes rather than silencing again.
func TestASecondPressAcceptsTheSnooze(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	at(t, &now)
	var e engines
	e.register(true)

	done := Sounding()
	defer done()
	Silence()

	now = now.Add(OfferFor / 2)
	if !Accept() {
		t.Fatal("accepting the offer did nothing")
	}
	if e.snoozed != 1 {
		t.Errorf("the alarm was snoozed %d times, want once", e.snoozed)
	}
	if Offered() {
		t.Error("the offer is still open after being taken up")
	}
	if Lapsed() {
		t.Error("an accepted offer later reads as unanswered, which would stop a snoozed alarm")
	}
}

// A timer registers no snooze, so accepting stops it. An alarm beside it is snoozed rather than
// stopped: snoozes run first, and the silences that follow catch what had none.
func TestAcceptSnoozesWhatCanBeAndStopsTheRest(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	at(t, &now)

	var alarm, timer engines
	alarm.register(true)  // an alarm: can be put off
	timer.register(false) // a timer: cannot

	done := Sounding()
	defer done()
	Silence()

	Accept()
	if alarm.snoozed != 1 {
		t.Errorf("the alarm was snoozed %d times, want once", alarm.snoozed)
	}
	if timer.silenced != 1 {
		t.Errorf("the timer was stopped %d times, want once — a timer cannot be put off", timer.silenced)
	}
}

// Nothing ringing, nothing to silence — so a button press does its ordinary job.
func TestSilenceWithNothingRingingDoesNothing(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	at(t, &now)
	var e engines
	e.register(true)

	if Silence() {
		t.Fatal("Silence claimed a ring when none was sounding; the press would be swallowed")
	}
	if Offered() || Lapsed() {
		t.Error("an offer exists with nothing ringing")
	}
}

// End is what "stop" means, with or without an offer open, and it does not snooze.
func TestEndStopsWithoutSnoozing(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	at(t, &now)
	var e engines
	e.register(true)

	done := Sounding()
	defer done()
	Silence()

	if !End() {
		t.Fatal("End did nothing")
	}
	if e.silenced != 1 {
		t.Errorf("stopped %d times, want once", e.silenced)
	}
	if e.snoozed != 0 {
		t.Error("End snoozed the alarm; it means stop")
	}
	if Offered() || Lapsed() {
		t.Error("the offer outlived the ring being ended")
	}
}

// A hush and an offer are different states and must not be confused: a hush expires so the alarm
// comes back, an offer expires so the alarm goes away. A hush must never make a ring look answered.
func TestAHushIsNotAnOffer(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	at(t, &now)
	var e engines
	e.register(true)

	done := Sounding()
	defer done()

	Hush()
	if !Quiet() {
		t.Fatal("a hush should hold the chime back")
	}
	if Offered() {
		t.Error("a hush is being offered; a button press would snooze instead of silencing")
	}

	now = now.Add(HushFor)
	if Lapsed() {
		t.Fatal("a hush that expired stopped the alarm; only an unanswered offer may do that")
	}
	if Quiet() {
		t.Error("the alarm did not come back after the hush expired")
	}
}
