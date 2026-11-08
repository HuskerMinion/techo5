package wallclock

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// TestPairingDropsTheLiveLinkBeforeItSearches checks the order the review asked for: a pairing does
// not begin with the clock still connected, and the loop dials nothing while the search has the
// adapter.
func TestPairingDropsTheLiveLinkBeforeItSearches(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().WallClock().Set(true, "AA:BB:CC:DD:EE:11"); err != nil {
		t.Fatalf("saving the wall clock: %v", err)
	}

	f := build()

	var (
		dialsMu sync.Mutex
		dials   int
	)
	oldDial := dialClock
	dialClock = func(context.Context, string, time.Duration, time.Duration) (*conn, error) {
		_, link := socketPair(t)
		dialsMu.Lock()
		dials++
		dialsMu.Unlock()
		return newConn(link), nil
	}
	defer func() { dialClock = oldDial }()

	searched := make(chan struct{})
	release := make(chan struct{})
	oldPair := pairClock
	pairClock = func(f *Feature) {
		defer f.failed("No clock found in pairing mode")
		f.mu.Lock()
		live := f.live
		f.mu.Unlock()
		if live != nil {
			t.Error("pairing began with a live link to the clock")
		}
		close(searched)
		<-release
	}
	defer func() { pairClock = oldPair }()

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

	waitFor(t, "the clock connected", func() bool { return f.Status() == "Connected" })

	if err := f.Pair(); err != nil {
		t.Fatalf("Pair: %v", err)
	}
	select {
	case <-searched:
	case <-time.After(5 * time.Second):
		t.Fatal("pairing did not take the adapter")
	}

	dialsMu.Lock()
	n := dials
	dialsMu.Unlock()
	if n != 1 {
		t.Errorf("the loop dialed %d times while pairing held the adapter, want 1", n)
	}

	close(release)
	waitFor(t, "the loop to reconnect after pairing", func() bool {
		dialsMu.Lock()
		defer dialsMu.Unlock()
		return dials == 2
	})
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
