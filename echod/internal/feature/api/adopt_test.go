package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
)

// adoptFiles points the window at a temporary folder with a device key in it.
func adoptFiles(t *testing.T) esphome.PSK {
	t.Helper()
	dir := t.TempDir()
	oldKey, oldAdopt := keyFile, adoptPath
	keyFile, adoptPath = filepath.Join(dir, "psk"), filepath.Join(dir, "adopt-until")
	t.Cleanup(func() { keyFile, adoptPath = oldKey, oldAdopt })
	var k esphome.PSK
	k[0] = 9
	if err := writePSK(keyFile, k); err != nil {
		t.Fatal(err)
	}
	return k
}

func newTestAPI() *API { return &API{reconnect: make(chan struct{}, 1)} }

// Opening the window takes the key away and serves with none, until the time it says.
func TestOpeningTheWindowServesWithNoKey(t *testing.T) {
	adoptFiles(t)
	a := newTestAPI()
	until, err := a.OpenAdoption()
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(until); d < adoptWindow-time.Minute || d > adoptWindow {
		t.Errorf("the window shuts in %v, want about %v", d, adoptWindow)
	}
	if k, err := loadPSK(keyFile); err != nil || !k.IsZero() {
		t.Errorf("after opening, the key read back is %v (%v), want none", k, err)
	}
	if k := a.takeNextPSK(); k == nil || !k.IsZero() {
		t.Errorf("the server was given %v, want the zero key", k)
	}
	if a.AdoptionOpenUntil().IsZero() {
		t.Error("the window does not say it is open")
	}
}

// A Home Assistant setting the key in the window: kept, and the window is over.
func TestAKeySetInTheWindowIsKept(t *testing.T) {
	adoptFiles(t)
	a := newTestAPI()
	if _, err := a.OpenAdoption(); err != nil {
		t.Fatal(err)
	}
	a.takeNextPSK() // the server has taken the zero key
	var theirs esphome.PSK
	theirs[5] = 42
	if err := a.keySet(theirs); err != nil {
		t.Fatal(err)
	}
	if k, err := loadPSK(keyFile); err != nil || *k != theirs {
		t.Errorf("the key kept is %v (%v), want the one Home Assistant set", k, err)
	}
	if !a.AdoptionOpenUntil().IsZero() {
		t.Error("the window is still open after a key was set")
	}
}

// A restart: a window past its time is shut with a new key, one still open stays open, and a key set
// before the restart leaves nothing to shut. A device with no window keeps what it has.
func TestARestartShutsAWindowPastItsTime(t *testing.T) {
	adoptFiles(t)
	a := newTestAPI()
	zero := esphome.Unprovisioned()

	if got := a.resumeAdoption(zero); got != zero {
		t.Error("a device with no window open had its key changed")
	}

	write := func(at time.Time) {
		if err := os.WriteFile(adoptPath, []byte(at.UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(time.Now().Add(-time.Minute))
	got := a.resumeAdoption(zero)
	if got.IsZero() {
		t.Fatal("a window past its time was left open")
	}
	if k, err := loadPSK(keyFile); err != nil || *k != *got {
		t.Errorf("the new key was not kept (%v)", err)
	}
	if _, err := os.Stat(adoptPath); !os.IsNotExist(err) {
		t.Error("the window's time was left behind")
	}

	write(time.Now().Add(time.Hour))
	if got := a.resumeAdoption(zero); !got.IsZero() {
		t.Error("a window still open was shut early")
	}

	var set esphome.PSK
	set[1] = 3
	if got := a.resumeAdoption(&set); *got != set {
		t.Error("a key set before the restart was replaced")
	}
	if _, err := os.Stat(adoptPath); !os.IsNotExist(err) {
		t.Error("a window a key was set in was kept")
	}
}

// A timer left from an earlier window does not shut a window opened again since: only the window's own
// end shuts it.
func TestAnEarlierWindowsTimerLeavesALaterWindowOpen(t *testing.T) {
	adoptFiles(t)
	a := newTestAPI()
	until, err := a.OpenAdoption()
	if err != nil {
		t.Fatal(err)
	}
	a.takeNextPSK()
	// The first window's timer, firing now: its end is earlier than the window open now.
	a.shutAdoptionAt(until.Add(-5 * time.Minute))
	time.Sleep(100 * time.Millisecond)
	if k, err := loadPSK(keyFile); err != nil || !k.IsZero() {
		t.Errorf("an earlier window's timer shut this one: key %v (%v)", k, err)
	}
	if a.AdoptionOpenUntil().IsZero() {
		t.Error("the window no longer says it is open")
	}
}

// A key set in the moment after the window's timer shut it, on the connection it has not dropped yet,
// is the key both kept and served: the window's own key, queued a moment before, gives way to it.
func TestAKeySetAsTheWindowShutsIsServed(t *testing.T) {
	adoptFiles(t)
	a := newTestAPI()
	if _, err := a.OpenAdoption(); err != nil {
		t.Fatal(err)
	}
	a.takeNextPSK() // the server has taken the zero key
	// The window's end, due now.
	due := time.Now().Add(-time.Second).UTC().Truncate(time.Second)
	if err := os.WriteFile(adoptPath, []byte(due.Format(time.RFC3339)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.shutAdoptionAt(due)
	time.Sleep(100 * time.Millisecond) // the timer has shut it and queued its key
	var theirs esphome.PSK
	theirs[3] = 7
	if err := a.keySet(theirs); err != nil {
		t.Fatal(err)
	}
	if k, err := loadPSK(keyFile); err != nil || *k != theirs {
		t.Fatalf("the key kept is %v (%v), want Home Assistant's", k, err)
	}
	if s := a.nextServer(); s.PSK == nil || *s.PSK != theirs {
		t.Errorf("the next server serves %v, want the key kept", s.PSK)
	}
}
