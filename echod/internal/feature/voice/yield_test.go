package voice

import (
	"testing"
	"time"
)

// keepYields puts the features that had the microphones back after a test.
func keepYields(t *testing.T) {
	was := yields.Load()
	yields.Store(nil)
	t.Cleanup(func() { yields.Store(was) })
}

// A feature that has the microphones holds turns off for exactly as long as it says it has them.
func TestYieldTo(t *testing.T) {
	keepYields(t)
	taken := true
	YieldTo("a talk", func() bool { return taken }, func() {})
	if !micTaken() {
		t.Error("the microphones were not taken")
	}
	taken = false
	if micTaken() {
		t.Error("the microphones were still taken")
	}
}

// Two features can each take the microphones: the second to say so does not stop the first from
// holding turns off, and the log names whichever has them.
func TestTwoFeaturesCanHoldTheMicrophones(t *testing.T) {
	keepYields(t)
	talk, takes := false, false
	YieldTo("a talk", func() bool { return talk }, func() {})
	YieldTo("takes", func() bool { return takes }, func() {})
	if micTaken() {
		t.Fatal("taken with neither holding them")
	}
	talk = true
	if !micTaken() || heldBy() != "a talk" {
		t.Errorf("the first feature's hold: taken %v by %q", micTaken(), heldBy())
	}
	talk, takes = false, true
	if !micTaken() || heldBy() != "takes" {
		t.Errorf("the second feature's hold: taken %v by %q", micTaken(), heldBy())
	}
}

// The action button lets go of the feature that has the microphones, not the other one.
func TestThePressReleasesTheOneThatHasThem(t *testing.T) {
	keepYields(t)
	released := make(chan string, 2)
	YieldTo("a talk", func() bool { return false }, func() { released <- "a talk" })
	YieldTo("takes", func() bool { return true }, func() { released <- "takes" })
	v := &Voice{}
	v.Action()
	select {
	case got := <-released:
		if got != "takes" {
			t.Errorf("the press released %q, not the takes", got)
		}
	case <-time.After(time.Second):
		t.Fatal("the press released nothing")
	}
	select {
	case got := <-released:
		t.Errorf("the press also released %q", got)
	case <-time.After(50 * time.Millisecond):
	}
}
