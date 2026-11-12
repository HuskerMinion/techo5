package wallclock

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// put sets *v to x for the test and puts it back after. Registered before Run is started, its
// cleanup runs after the one that stops Run, so the loop never reads the variable as it is put back.
func put[T any](t *testing.T, v *T, x T) {
	old := *v
	*v = x
	t.Cleanup(func() { *v = old })
}

// run starts f's loop and stops it when the test ends.
func run(t *testing.T, f *Feature) {
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
}

// TestNextDelay checks the back-off doubles and then stops at the cap: an absent clock must not
// cost a page every ten seconds for ever.
func TestNextDelay(t *testing.T) {
	put(t, &reconnectDelayMax, time.Minute)

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
	put(t, &reconnectDelay, time.Millisecond)
	put(t, &dialClock, func(context.Context, string, time.Duration, time.Duration) (*conn, error) {
		return nil, errors.New("host is down")
	})

	f := build()
	run(t, f)

	waitFor(t, "the clock to be given up on", func() bool { return f.Status() == "Waiting for the clock" })
}

// A clock paired, or an address saved, after the old clock has been gone a while starts the back-off
// over: if the first try at the new one misses, the next comes soon, not after the long wait the old
// clock had reached.
func TestANewAddressStartsTheBackOffOver(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().WallClock().Set(true, "AA:BB:CC:DD:EE:22"); err != nil {
		t.Fatalf("saving the wall clock: %v", err)
	}
	put(t, &reconnectDelay, time.Millisecond)
	put(t, &reconnectDelayMax, time.Hour)
	var dials atomic.Int32
	put(t, &dialClock, func(context.Context, string, time.Duration, time.Duration) (*conn, error) {
		dials.Add(1)
		return nil, errors.New("host is down")
	})

	f := build()
	run(t, f)

	// Eleven misses take about a second, and leave the next wait at about two.
	waitFor(t, "the back-off to grow", func() bool { return dials.Load() >= 11 })
	n := dials.Load()
	if err := config.Set().WallClock().Set(true, "AA:BB:CC:DD:EE:33"); err != nil {
		t.Fatalf("saving the new clock: %v", err)
	}
	f.wakeLoop()
	deadline := time.Now().Add(500 * time.Millisecond)
	for dials.Load() < n+2 {
		if time.Now().After(deadline) {
			t.Fatalf("%d tries in the half second after the new address, want the first and a quick second", dials.Load()-n)
		}
		time.Sleep(time.Millisecond)
	}
}
