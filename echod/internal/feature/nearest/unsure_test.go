package nearest

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
)

// A device not sure of its wake word tells the others as it asks Home Assistant (#685): one that heard
// it for sure but less well waits for the answer, and never stands down for the unsure claim alone.

// unsureFrom is an unsure claim from another device arriving now.
func (h *house) unsureFrom(name, turn string, score float64) {
	h.n.took(claim{From: name, Turn: turn, Word: unsurePrefix + "okay_nabu", Score: score})
}

// until lets time go by in heldPoll steps, running what was waiting, until d has passed.
func (h *house) until(d time.Duration) {
	for end := h.now.Add(d); h.now.Before(end); {
		h.now = h.now.Add(heldPoll)
		due := h.later
		h.later = nil
		for _, f := range due { // a decision held again queues its next look
			f()
		}
	}
}

func TestASureWakeWaitsOnABetterUnsureOneAndLosesWhenItIsHeard(t *testing.T) {
	h := newHouse(t) // scores 20
	h.n.Woke(0)
	h.unsureFrom("Lounge", "u1", 30)
	h.until(300 * time.Millisecond)
	if len(h.opened) != 0 {
		t.Fatal("answered over a device still asking home assistant")
	}
	h.until(500 * time.Millisecond)
	h.claimFrom("Lounge", 30) // Home Assistant heard it there: its claim for the word itself
	h.until(200 * time.Millisecond)
	if len(h.opened) != 0 || len(h.later) != 0 {
		t.Fatalf("opened %d turns, still waiting %d: want it to stand down", len(h.opened), len(h.later))
	}
}

func TestASureWakeAnswersOnceTheUnsureOneIsWithdrawn(t *testing.T) {
	h := newHouse(t)
	heard := h.now
	h.n.Woke(0)
	h.unsureFrom("Lounge", "u1", 30)
	h.until(600 * time.Millisecond)
	if len(h.opened) != 0 {
		t.Fatal("answered before the unsure claim was settled")
	}
	h.n.took(claim{From: "Lounge", Turn: "u1", Word: unsurePrefix + "okay_nabu", Score: 30, Withdrawn: true})
	h.until(100 * time.Millisecond)
	if len(h.opened) != 1 || !h.opened[0].Equal(heard) {
		t.Fatalf("opened %v, want one turn reaching back to the word", h.opened)
	}
}

func TestASureWakeStopsWaitingOnAnUnsureOneThatNeverSettles(t *testing.T) {
	h := newHouse(t)
	h.n.Woke(0)
	h.unsureFrom("Lounge", "u1", 30)
	h.until(unsureFor - 100*time.Millisecond)
	if len(h.opened) != 0 {
		t.Fatal("stopped waiting early")
	}
	h.until(200 * time.Millisecond)
	if len(h.opened) != 1 {
		t.Fatalf("opened %d turns after unsureFor, want 1", len(h.opened))
	}
}

func TestAWeakerUnsureClaimIsNotWaitedOn(t *testing.T) {
	h := newHouse(t)
	h.n.Woke(0)
	h.unsureFrom("Kitchen", "u1", 4)
	h.wait(300 * time.Millisecond)
	if len(h.opened) != 1 {
		t.Fatalf("opened %d turns, want 1 at the end of the window", len(h.opened))
	}
}

// An unsure claim is for its word only, and a withdrawal for its own claim only.
func TestAnUnsureClaimCountsForItsWordAndTurnOnly(t *testing.T) {
	h := newHouse(t)
	h.n.Woke(0)
	h.n.took(claim{From: "Lounge", Turn: "u1", Word: unsurePrefix + "hey_jarvis", Score: 30})
	h.unsureFrom("Lounge", "u2", 30)
	h.n.took(claim{From: "Lounge", Turn: "u1", Word: unsurePrefix + "okay_nabu", Withdrawn: true})
	h.until(300 * time.Millisecond)
	if len(h.opened) != 0 {
		t.Fatal("a withdrawal of another claim, or one for another word, ended the wait")
	}
}

// Asking tells the others with an unsure claim, and takes it back unless Home Assistant heard the word.
func TestAskingTellsTheOthersAndTakesItBack(t *testing.T) {
	h := newHouse(t)
	var told []claim
	h.n.tell = func(_ []announce.Peer, c claim) { told = append(told, c) }
	settled := h.n.Asking(0, 9)
	if settled == nil || len(told) != 1 || told[0].Word != unsurePrefix+"okay_nabu" || told[0].Score != 9 || told[0].Withdrawn {
		t.Fatalf("told %+v, want one unsure claim at the clarity", told)
	}
	settled(false)
	if len(told) != 2 || !told[1].Withdrawn || told[1].Turn != told[0].Turn {
		t.Fatalf("told %+v, want the claim withdrawn", told)
	}
	told = nil
	h.n.Asking(0, 9)(true)
	if len(told) != 1 {
		t.Fatalf("told %+v after home assistant heard it, want no withdrawal", told)
	}
	h.n.enabled = false
	if h.n.Asking(0, 9) != nil {
		t.Error("an unsure claim with nearest device answers off")
	}
}

// A device that keeps sending fresh unsure claims holds the wake word unsureFor at most in all, not
// unsureFor from each one.
func TestFreshUnsureClaimsDoNotHoldForever(t *testing.T) {
	h := newHouse(t)
	h.n.Woke(0)
	for i := range 8 {
		h.unsureFrom("Lounge", "u"+string(rune('a'+i)), 30)
		h.until(500 * time.Millisecond)
	}
	if len(h.opened) != 1 {
		t.Fatalf("opened %d turns after 4 s of fresh unsure claims, want it answered once", len(h.opened))
	}
}
