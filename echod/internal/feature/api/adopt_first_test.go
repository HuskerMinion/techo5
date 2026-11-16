package api

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
)

// markerFiles is adoptFiles with the installer's marker beside the key, and the key taken away when
// keyed is false: a device installed with --let-home-assistant-add has none.
func markerFiles(t *testing.T, keyed bool) {
	t.Helper()
	adoptFiles(t)
	old, oldOnline, oldPoll := adoptMarker, adoptOnline, adoptPoll
	adoptMarker = filepath.Join(filepath.Dir(keyFile), "adopt-on-first-start")
	adoptOnline, adoptPoll = func() bool { return true }, 10*time.Millisecond
	t.Cleanup(func() { adoptMarker, adoptOnline, adoptPoll = old, oldOnline, oldPoll })
	if err := os.WriteFile(adoptMarker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !keyed {
		if err := os.Remove(keyFile); err != nil {
			t.Fatal(err)
		}
	}
}

// startFirst is adoptOnFirstStart, with the test's end waiting for its wait for a network to be over:
// the variables markerFiles puts back are what that wait reads.
func startFirst(t *testing.T, ctx context.Context, a *API, psk *esphome.PSK) {
	t.Helper()
	a.adoptOnFirstStart(ctx, psk)
	if done := a.firstWindow; done != nil {
		t.Cleanup(func() {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("the wait for a network did not end")
			}
		})
	}
}

func markerGone(t *testing.T) bool {
	t.Helper()
	_, err := os.Stat(adoptMarker)
	return os.IsNotExist(err)
}

// The marker on a device with no key becomes a window of firstInstallWindow, and is removed: it is
// honored once.
func TestTheMarkerBecomesAWindow(t *testing.T) {
	markerFiles(t, false)
	a := newTestAPI()
	until, err := a.openFirstWindow()
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(until); d < firstInstallWindow-time.Minute || d > firstInstallWindow {
		t.Errorf("the window shuts in %v, want about %v", d, firstInstallWindow)
	}
	if got := a.AdoptionOpenUntil(); !got.Equal(until) {
		t.Errorf("the window says it shuts at %v, want %v", got, until)
	}
	if !markerGone(t) {
		t.Error("the marker was left behind")
	}
	if k, err := loadPSK(keyFile); err != nil || !k.IsZero() {
		t.Errorf("the key read back is %v (%v), want none", k, err)
	}
	if again, err := a.openFirstWindow(); err != nil || !again.IsZero() {
		t.Errorf("a second look opened another window: %v (%v)", again, err)
	}
}

// Start's look waits for a network before it opens the window: the unit's clock may be wrong until
// then.
func TestTheMarkerWaitsForANetwork(t *testing.T) {
	markerFiles(t, false)
	var up atomic.Bool
	adoptOnline = up.Load
	a := newTestAPI()
	startFirst(t, context.Background(), a, esphome.Unprovisioned())
	time.Sleep(50 * time.Millisecond)
	if !a.AdoptionOpenUntil().IsZero() || markerGone(t) {
		t.Fatal("the window opened with no network")
	}
	up.Store(true)
	deadline := time.Now().Add(2 * time.Second)
	for a.AdoptionOpenUntil().IsZero() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if a.AdoptionOpenUntil().IsZero() {
		t.Fatal("the window did not open once the device had a network")
	}
	if !markerGone(t) {
		t.Error("the marker was left behind")
	}
}

// A start that ends before the device had a network leaves the marker for the next start.
func TestTheMarkerOutlivesAStartWithNoNetwork(t *testing.T) {
	markerFiles(t, false)
	adoptOnline = func() bool { return false }
	a := newTestAPI()
	ctx, cancel := context.WithCancel(context.Background())
	startFirst(t, ctx, a, esphome.Unprovisioned())
	cancel()
	time.Sleep(50 * time.Millisecond)
	if markerGone(t) {
		t.Error("the marker went with a start that never had a network")
	}
	if !a.AdoptionOpenUntil().IsZero() {
		t.Error("a window opened with no network")
	}
}

// A device that already has a key ignores the marker, removes it, and keeps its key.
func TestADeviceWithAKeyIgnoresTheMarker(t *testing.T) {
	markerFiles(t, true)
	k, err := loadPSK(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	a := newTestAPI()
	startFirst(t, context.Background(), a, k)
	if !markerGone(t) {
		t.Error("the marker was left behind")
	}
	if !a.AdoptionOpenUntil().IsZero() {
		t.Error("a device with a key opened a window")
	}
	if got, err := loadPSK(keyFile); err != nil || *got != *k {
		t.Errorf("the key is now %v (%v), want the one it had", got, err)
	}
}

// A key a Home Assistant set while the device waited is kept, and the marker opens nothing.
func TestAKeySetBeforeTheWindowLeavesItShut(t *testing.T) {
	markerFiles(t, false)
	a := newTestAPI()
	var theirs esphome.PSK
	theirs[2] = 5
	if err := a.keySet(theirs); err != nil {
		t.Fatal(err)
	}
	if until, err := a.openFirstWindow(); err != nil || !until.IsZero() {
		t.Errorf("a window opened over a key that was set: %v (%v)", until, err)
	}
	if !markerGone(t) {
		t.Error("the marker was left behind")
	}
	if got, err := loadPSK(keyFile); err != nil || *got != theirs {
		t.Errorf("the key is now %v (%v), want Home Assistant's", got, err)
	}
}

// A window open already, from the setup page or kept across a restart, is left as it is.
func TestTheMarkerLeavesAnOpenWindowAlone(t *testing.T) {
	markerFiles(t, false)
	a := newTestAPI()
	open, err := writeAdoptUntil(5 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if until, err := a.openFirstWindow(); err != nil || !until.IsZero() {
		t.Errorf("the marker opened another window: %v (%v)", until, err)
	}
	if got := a.AdoptionOpenUntil(); !got.Equal(open) {
		t.Errorf("the window shuts at %v, want the one open before, %v", got, open)
	}
	if !markerGone(t) {
		t.Error("the marker was left behind")
	}
}
