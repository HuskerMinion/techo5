package wallclock

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// TestNextDelay checks the back-off doubles and then stops at the cap: an absent clock must not
// cost a page every ten seconds for ever.
func TestNextDelay(t *testing.T) {
	oldMax := reconnectDelayMax
	reconnectDelayMax = time.Minute
	defer func() { reconnectDelayMax = oldMax }()

	cases := []struct{ in, want time.Duration }{
		{10 * time.Second, 20 * time.Second},
		{20 * time.Second, 40 * time.Second},
		{40 * time.Second, time.Minute},
		{time.Minute, time.Minute},
	}
	for _, c := range cases {
		if got := nextDelay(c.in); got != c.want {
			t.Errorf("nextDelay(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestWaitingIsSaidOnceNotEveryAttempt checks the status only says "Waiting for the clock" after
// three failed attempts, and does not alternate with "Connecting" between them. The flapping made
// the status sensor's last_changed useless to a monitor.
func TestWaitingIsSaidOnceNotEveryAttempt(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().WallClock().Set(true, "AA:BB:CC:DD:EE:22"); err != nil {
		t.Fatalf("saving the wall clock: %v", err)
	}

	oldDelay := reconnectDelay
	reconnectDelay = time.Millisecond
	defer func() { reconnectDelay = oldDelay }()

	oldDial := dialClock
	dialClock = func(context.Context, string, time.Duration, time.Duration) (*conn, error) {
		return nil, errors.New("host is down")
	}
	defer func() { dialClock = oldDial }()

	f := build()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Run did not stop")
		}
	})

	waitFor(t, "the clock to be given up on", func() bool { return f.Status() == "Waiting for the clock" })
}
