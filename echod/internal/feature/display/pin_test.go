//go:build !dot

package display

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
)

// typePIN types pin on the pad and presses OK.
func typePIN(pin string) {
	for _, k := range pin {
		pinPress(string(k))
	}
	pinPress("ok")
}

// With the lock on, the settings open to everyone: what is part of using the device changes at a tap,
// and the rest asks for the PIN, changes once it is right, and stays open behind it until relocked.
func TestTheLockIsOnTheRowsNotTheDoor(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := security.Get().SetPIN("2468"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pinPress("cancel")
		_ = security.Get().SetPIN("")
	})
	up := func() bool { return true }

	for _, id := range []string{"alarm:a1", "e.hour", "timer:t1", "newtimer", "snooze", "alarmsound",
		"volume", "bass", "mic", "sleep", "brightness", "auto"} {
		ran := false
		gate(id, up, func() { ran = true })
		if !ran || pinIsOpen() {
			t.Errorf("%s: ran %v, pad up %v; an everyday setting changes without the PIN", id, ran, pinIsOpen())
		}
	}

	ran := 0
	tap := func() { ran++ }
	for range 10 {
		gate("ssh", up, tap)
	}
	if ran != 0 || !pinIsOpen() {
		t.Fatalf("ssh: ran %d times, pad up %v; want the pad and no change", ran, pinIsOpen())
	}
	if c := config.Get().Security; c.LockFails != 0 {
		t.Errorf("taps on a locked row counted as %d wrong PINs", c.LockFails)
	}

	typePIN("0000")
	if ran != 0 || !pinIsOpen() {
		t.Fatalf("a wrong PIN: ran %d, pad up %v", ran, pinIsOpen())
	}
	typePIN("2468")
	if ran != 1 || pinIsOpen() || security.Locked() {
		t.Fatalf("the right PIN: ran %d, pad up %v, locked %v", ran, pinIsOpen(), security.Locked())
	}
	gate("wakeword", up, tap)
	if ran != 2 || pinIsOpen() {
		t.Errorf("once open, another locked setting: ran %d, pad up %v", ran, pinIsOpen())
	}

	// The settings closed while the PIN was being typed: nothing behind them changes.
	security.Relock()
	gate("ssh", func() bool { return false }, tap)
	typePIN("2468")
	if ran != 2 {
		t.Errorf("a setting changed behind settings that had closed")
	}
}

// Everything the lock was set up to keep children away from stays behind it, and every row the
// Alarms card can show stays open: a row is open only by being named.
func TestWhichRowsAreEveryday(t *testing.T) {
	for _, cat := range []category{catConnections, catSecurity, catGeneral} {
		rows, _ := categoryRows(sheetView{st: settings{cat: cat}})
		for _, row := range rows {
			if row.id != "" && everyday(row.id) {
				t.Errorf("%s: %q is open while the lock is on", categoryNames[cat], row.id)
			}
		}
	}
	for _, id := range []string{"wakeword", "wakesens", "voice", "quiet", "dnd", "night", "theme", "slideshow",
		"airplay", "spotify", "dlna", "sendspin", "settingslock", "ssh", "updates", "restart", "", "unknown"} {
		if everyday(id) {
			t.Errorf("%q is open while the lock is on", id)
		}
	}
	for _, row := range alarmsCard(sheetView{st: settings{cat: catAlarms}}).rows {
		if row.id != "" && !everyday(row.id) {
			t.Errorf("Alarms: %q asks for the PIN", row.id)
		}
	}
}

// A locked row is marked for its padlock while the lock is on, and only then.
func TestLockedRowsAreMarked(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	card := cardView{rows: []settingRow{{id: "ssh"}, {id: "volume"}, {label: "Look", kind: ctlHeading}}}
	for _, row := range withLocks(card).rows {
		if row.locked {
			t.Errorf("%q marked with no PIN set", row.id)
		}
	}
	if err := security.Get().SetPIN("2468"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = security.Get().SetPIN("") })
	got := withLocks(card).rows
	if !got[0].locked || got[1].locked || got[2].locked {
		t.Errorf("marked: ssh %v, volume %v, heading %v; want only ssh", got[0].locked, got[1].locked, got[2].locked)
	}
	if card.rows[0].locked {
		t.Error("the card's own rows were changed")
	}
}
