package firstrun

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// A reset accepted while a call is going on holds its teardown and restart until the call ends, rather
// than cutting the call off; a call that never ends still lets the reset through once the wait is up.
func TestResetWaitsOutACall(t *testing.T) {
	fresh(t)

	restarted := make(chan struct{}, 1)
	wasRestart, wasCall, wasDelay := restart, inCall, answerDelay
	wasWait, wasPoll := update.CallWait, update.CallPoll
	restart = func() { restarted <- struct{}{} }
	answerDelay = 0
	update.CallWait, update.CallPoll = time.Second, time.Millisecond
	var busy atomic.Bool
	busy.Store(true)
	inCall = func() bool { return busy.Load() }
	t.Cleanup(func() {
		restart, inCall, answerDelay = wasRestart, wasCall, wasDelay
		update.CallWait, update.CallPoll = wasWait, wasPoll
	})

	if err := Reset(); err != nil {
		t.Fatal(err)
	}
	// Held while the call is up.
	select {
	case <-restarted:
		t.Fatal("it restarted while a call was in progress")
	case <-time.After(30 * time.Millisecond):
	}
	// Released once the call ends.
	busy.Store(false)
	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatal("it did not restart after the call ended")
	}
}
