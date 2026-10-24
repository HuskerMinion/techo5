package phone

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A call that waits before it is dialed has the line at once, as a call being placed, and Hang up
// during the wait ends it there: nothing is dialed, and Home Assistant hears of no call.
func TestHangingUpACallStillWaitingDialsNothing(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	p := Get()
	p.mu.Lock()
	p.line = &line{} // never dialed on: the wait is canceled first
	p.mu.Unlock()
	p.set(func(s *State) { s.Configured, s.Registered = true, true })
	t.Cleanup(func() {
		p.mu.Lock()
		p.line = nil
		p.mu.Unlock()
		p.set(func(s *State) { s.Phase, s.Configured, s.Registered = Idle, false, false })
	})
	var events []component.Event
	stop := component.Fire.Listen(func(e component.Event) { events = append(events, e) })
	defer stop()

	waiting := make(chan struct{})
	gaveUp := make(chan bool, 1)
	err := p.CallAfter("1 555 123 4567", "", func(ctx context.Context) bool {
		close(waiting)
		<-ctx.Done()
		gaveUp <- true
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
	<-waiting
	if st := p.State(); st.Phase != Dialing || !st.Waiting {
		t.Fatalf("while waiting: %+v", st)
	}
	if err := p.Call("106"); err == nil {
		t.Error("a second call took the line from the one waiting")
	}

	p.Hangup()
	select {
	case <-gaveUp:
	case <-time.After(time.Second):
		t.Fatal("Hang up did not end the wait")
	}
	deadline := time.Now().Add(time.Second)
	for p.State().Phase != Idle && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if st := p.State(); st.Phase != Idle || st.Waiting {
		t.Errorf("after Hang up: %+v", st)
	}
	for _, e := range events {
		if e.Name == Event {
			t.Errorf("Home Assistant heard of a call never dialed: %v", e.Data)
		}
	}
}
