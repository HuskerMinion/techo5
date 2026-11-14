package wallclock

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// TestKeepSniffingRepeats checks the link is asked for sniff mode after it settles and again every
// interval: the kernel leaves sniff for every packet and never returns by itself.
func TestKeepSniffingRepeats(t *testing.T) {
	var calls atomic.Int32
	var gotAddress atomic.Value

	f := build()
	f.requestSniff = func(_ context.Context, address string) error {
		calls.Add(1)
		gotAddress.Store(address)
		return nil
	}
	f.sniffAfter = time.Millisecond
	f.sniffEvery = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.keepSniffing(ctx, "AA:BB:CC:DD:EE:FF", nil)
	}()

	// Stop the goroutine before the test ends: a failed assertion must not leave it running.
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("keepSniffing did not stop when its context ended")
		}
	}()

	waitFor(t, "three sniff requests", func() bool { return calls.Load() >= 3 })
	if got := gotAddress.Load(); got != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("sniff requested for %v, want AA:BB:CC:DD:EE:FF", got)
	}
}

// TestKeepSniffingAfterTraffic checks a write brings the request forward: the kernel leaves sniff
// for every packet, so waiting for the minute tick would leave the clock awake after each send.
func TestKeepSniffingAfterTraffic(t *testing.T) {
	calls := make(chan string, 4)

	f := build()
	f.requestSniff = func(_ context.Context, address string) error {
		calls <- address
		return nil
	}
	// Only a send should cause a request here: the settling and safety timers are out of reach.
	f.sniffAfter = time.Hour
	f.sniffEvery = time.Hour
	f.sniffTrafficDelay = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	sent := make(chan struct{}, 1)
	go func() {
		defer close(done)
		f.keepSniffing(ctx, "AA:BB:CC:DD:EE:42", sent)
	}()

	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("keepSniffing did not stop when its context ended")
		}
	}()

	sent <- struct{}{}
	select {
	case got := <-calls:
		if got != "AA:BB:CC:DD:EE:42" {
			t.Fatalf("sniff requested for %q, want the traffic's address", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a write did not bring the sniff request forward")
	}
}

// TestSniffResult checks the two successful answers and that a real refusal is reported.
func TestSniffResult(t *testing.T) {
	for _, tc := range []struct {
		status uint8
		fail   bool
	}{
		{0x00, false}, // success
		{0x0C, false}, // Command Disallowed: already sniffing
		{0x01, true},  // Unknown HCI Command
		{0x12, true},  // Invalid HCI Command Parameters
	} {
		err := sniffResult(tc.status)
		if (err != nil) != tc.fail {
			t.Errorf("sniffResult(0x%02X) = %v, want error %v", tc.status, err, tc.fail)
		}
	}
}

// TestRunStartsSniffingOnAConnection checks the feature, not just keepSniffing: a live link starts
// the sniffer, and closing Run stops it.
func TestRunStartsSniffingOnAConnection(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().WallClock().Set(true, "AA:BB:CC:DD:EE:22"); err != nil {
		t.Fatalf("saving the wall clock: %v", err)
	}

	oldDial := dialClock
	dialClock = func(context.Context, string, time.Duration, time.Duration) (*conn, error) {
		_, link := socketPair(t)
		return newConn(link), nil
	}
	var calls atomic.Int32
	var gotAddress atomic.Value

	f := build()
	f.requestSniff = func(_ context.Context, address string) error {
		calls.Add(1)
		gotAddress.Store(address)
		return nil
	}
	f.sniffAfter = time.Millisecond
	f.sniffEvery = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.Run(ctx) }()

	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Run did not stop")
		}
		dialClock = oldDial
	}()

	waitFor(t, "the clock connected", func() bool { return f.Status() == "Connected" })
	waitFor(t, "two sniff requests", func() bool { return calls.Load() >= 2 })
	if got := gotAddress.Load(); got != "AA:BB:CC:DD:EE:22" {
		t.Fatalf("sniff requested for %v, want the configured clock", got)
	}
}
