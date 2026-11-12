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
	oldRequest, oldAfter, oldEvery := requestSniff, sniffAfter, sniffEvery

	var calls atomic.Int32
	var gotAddress atomic.Value
	requestSniff = func(_ context.Context, address string) error {
		calls.Add(1)
		gotAddress.Store(address)
		return nil
	}
	sniffAfter = time.Millisecond
	sniffEvery = 5 * time.Millisecond

	f := build()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.keepSniffing(ctx, "AA:BB:CC:DD:EE:FF")
	}()

	// Stop the goroutine before restoring the package variables: a failed assertion must not leave
	// it reading the real sniffClock after the test.
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("keepSniffing did not stop when its context ended")
		}
		requestSniff, sniffAfter, sniffEvery = oldRequest, oldAfter, oldEvery
	}()

	waitFor(t, "three sniff requests", func() bool { return calls.Load() >= 3 })
	if got := gotAddress.Load(); got != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("sniff requested for %v, want AA:BB:CC:DD:EE:FF", got)
	}
}

// TestSniffResult checks the two successful answers and that a real refusal is reported.
func TestSniffResult(t *testing.T) {
	for _, tc := range []struct {
		status uint8
		fail   bool
	}{
		{0x00, false}, // success
		{0x0C, false}, // Command Disallowed: already sniffing, or not the master
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

	oldDial, oldRequest, oldAfter, oldEvery := dialClock, requestSniff, sniffAfter, sniffEvery
	dialClock = func(context.Context, string, time.Duration, time.Duration) (*conn, error) {
		_, link := socketPair(t)
		return newConn(link), nil
	}
	var calls atomic.Int32
	var gotAddress atomic.Value
	requestSniff = func(_ context.Context, address string) error {
		calls.Add(1)
		gotAddress.Store(address)
		return nil
	}
	sniffAfter = time.Millisecond
	sniffEvery = 10 * time.Millisecond

	f := build()
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
		dialClock, requestSniff, sniffAfter, sniffEvery = oldDial, oldRequest, oldAfter, oldEvery
	}()

	waitFor(t, "the clock connected", func() bool { return f.Status() == "Connected" })
	waitFor(t, "two sniff requests", func() bool { return calls.Load() >= 2 })
	if got := gotAddress.Load(); got != "AA:BB:CC:DD:EE:22" {
		t.Fatalf("sniff requested for %v, want the configured clock", got)
	}
}
