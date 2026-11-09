package update

import (
	"sync/atomic"
	"testing"
	"time"
)

// WaitWhile holds while busy reports true and returns once it is false.
func TestWaitWhileHoldsUntilFree(t *testing.T) {
	wasWait, wasPoll := CallWait, CallPoll
	CallWait, CallPoll = 2*time.Second, time.Millisecond
	t.Cleanup(func() { CallWait, CallPoll = wasWait, wasPoll })

	var busy atomic.Bool
	busy.Store(true)
	done := make(chan struct{})
	go func() { WaitWhile(busy.Load); close(done) }()

	select {
	case <-done:
		t.Fatal("it returned while busy was still true")
	case <-time.After(20 * time.Millisecond):
	}
	busy.Store(false)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("it did not return after busy cleared")
	}
}

// WaitWhile gives up after CallWait even if busy never clears, so a stuck call cannot hold a reset
// forever; a nil check returns at once.
func TestWaitWhileGivesUpAndHandlesNil(t *testing.T) {
	wasWait, wasPoll := CallWait, CallPoll
	CallWait, CallPoll = 30*time.Millisecond, time.Millisecond
	t.Cleanup(func() { CallWait, CallPoll = wasWait, wasPoll })

	start := time.Now()
	WaitWhile(func() bool { return true })
	if d := time.Since(start); d < 20*time.Millisecond {
		t.Errorf("returned after %v, before the cap was up", d)
	}
	WaitWhile(nil) // must not block or panic
}
