package nearest

import (
	"testing"
	"time"
)

// A wake Home Assistant confirmed (#685) is claimed at the time it was heard, with the clarity measured
// then, and answers only if no device heard it better or had already answered it.

func TestAConfirmedWakeWithNoBetterClaimAnswers(t *testing.T) {
	h := newHouse(t)
	heard := h.now
	h.now = h.now.Add(900 * time.Millisecond) // Home Assistant took most of a second to hear it
	var answer *bool
	h.n.Confirmed(0, heard, 11, nil, func(a bool) { answer = &a })
	if len(h.sent) != 1 || h.sent[0].Score != 11 {
		t.Fatalf("claims sent: %+v, want one at the clarity measured at the candidate", h.sent)
	}
	if answer != nil {
		t.Fatal("decided before the window ended")
	}
	h.wait(300 * time.Millisecond)
	if answer == nil || !*answer {
		t.Fatalf("no better claim: answer %v, want true", answer)
	}
	if len(h.faint) != 0 || len(h.opened) != 0 {
		t.Error("a confirmed wake went the way of a wake heard here")
	}
}

func TestAConfirmedWakeLosesToABetterClaimFromBeforeIt(t *testing.T) {
	h := newHouse(t)
	heard := h.now
	h.claimFrom("Lounge", 30) // the device somebody stands next to claimed it as it heard it
	h.now = h.now.Add(900 * time.Millisecond)
	var answer *bool
	h.n.Confirmed(0, heard, 4, nil, func(a bool) { answer = &a })
	h.wait(300 * time.Millisecond)
	if answer == nil || *answer {
		t.Fatalf("a better claim from before: answer %v, want false", answer)
	}
}

func TestAConfirmedWakeStandsDownForOneAlreadyAnswered(t *testing.T) {
	h := newHouse(t)
	heard := h.now
	h.now = h.now.Add(900 * time.Millisecond)
	var answer *bool
	h.n.Confirmed(0, heard, 25, nil, func(a bool) { answer = &a })
	h.n.answeredElsewhere(h.mine, "Lounge")
	h.wait(300 * time.Millisecond)
	if answer == nil || *answer {
		t.Fatalf("told it was answered: answer %v, want false", answer)
	}
}

// With nobody to compare with, or the decision off, it answers at once.
func TestAConfirmedWakeAnswersAtOnceWithNobodyToAsk(t *testing.T) {
	h := newHouse(t)
	h.n.enabled = false
	var answer *bool
	h.n.Confirmed(0, h.now, 4, nil, func(a bool) { answer = &a })
	if answer == nil || !*answer || len(h.sent) != 0 {
		t.Fatalf("off: answer %v, claims %+v; want an answer at once and no claim", answer, h.sent)
	}
}

// A confirmed wake low on clarity is not asked about again: Home Assistant has heard it (#503).
func TestAConfirmedWakeIsNotFaint(t *testing.T) {
	h := newHouse(t)
	confirmBelow(t, true, 15)
	var answer *bool
	h.n.Confirmed(0, h.now, 4, nil, func(a bool) { answer = &a })
	h.wait(300 * time.Millisecond)
	if answer == nil || !*answer || len(h.faint) != 0 {
		t.Fatalf("answer %v, faint %v; want an answer and no second question", answer, h.faint)
	}
}

// A newer claim of this device's own takes a confirmed wake's place: that wake is told it lost, so the
// conversation does not wait on it.
func TestASupersededConfirmedWakeIsTold(t *testing.T) {
	h := newHouse(t)
	var answer *bool
	h.n.Confirmed(0, h.now, 11, nil, func(a bool) { answer = &a })
	h.n.Woke(0)
	h.wait(300 * time.Millisecond)
	if answer == nil || *answer {
		t.Fatalf("superseded: answer %v, want false", answer)
	}
	if len(h.opened) != 1 {
		t.Errorf("the newer wake opened %d turns, want 1", len(h.opened))
	}
}

// A confirmed wake answered at once, with no claim for the word (the device is busy), takes back the
// unsure claim it made while asking, so a device waiting on it is not kept waiting.
func TestAConfirmedWakeAnsweredAtOnceTakesBackItsUnsureClaim(t *testing.T) {
	h := newHouse(t)
	h.n.busy = func() bool { return true }
	var took, answer *bool
	h.n.Confirmed(0, h.now, 11, func(c bool) { took = &c }, func(a bool) { answer = &a })
	if answer == nil || !*answer {
		t.Fatal("a busy device did not answer at once")
	}
	if took == nil || *took {
		t.Fatalf("the unsure claim was not taken back: settled %v", took)
	}
}
