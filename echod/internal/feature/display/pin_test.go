//go:build !dot

package display

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		gate(id, id, up, func() { ran = true })
		if !ran || pinIsOpen() {
			t.Errorf("%s: ran %v, pad up %v; an everyday setting changes without the PIN", id, ran, pinIsOpen())
		}
	}

	ran := 0
	tap := func() { ran++ }
	for range 10 {
		gate("ssh", "SSH", up, tap)
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
	gate("wakeword", "Wake word", up, tap)
	if ran != 2 || pinIsOpen() {
		t.Errorf("once open, another locked setting: ran %d, pad up %v", ran, pinIsOpen())
	}

	// The settings closed while the PIN was being typed: nothing behind them changes, and the lock
	// stays on for whoever opens them next.
	security.Relock()
	gate("ssh", "SSH", func() bool { return false }, tap)
	if p := pinNow(time.Now()); p.what != "SSH" {
		t.Errorf("the pad is for %q, want SSH", p.what)
	}
	typePIN("2468")
	if ran != 2 || !security.Locked() {
		t.Errorf("behind settings that had closed: ran %d, locked %v; want nothing changed and the lock on", ran, security.Locked())
	}

	// A nil tap is the PIN opening the lock and nothing more.
	gate("theme", "", up, nil)
	typePIN("2468")
	if security.Locked() || pinIsOpen() {
		t.Errorf("the PIN alone: locked %v, pad up %v", security.Locked(), pinIsOpen())
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
	for _, id := range []string{"wakeword", "wakesens", "ttsvoice", "quiet", "dnd", "night", "theme", "slideshow",
		"airplay", "spotify", "dlna", "sendspin", "settingslock", "ssh", "updates", "restart", "", "unknown"} {
		if everyday(id) {
			t.Errorf("%q is open while the lock is on", id)
		}
	}
	// The two mixed categories: only what the lock leaves open by name is open there.
	open := map[category][]string{}
	for _, cat := range []category{catDisplay, catSound} {
		rows, _ := categoryRows(sheetView{st: settings{cat: cat, auto: true}})
		for _, row := range rows {
			if row.id != "" && everyday(row.id) {
				open[cat] = append(open[cat], row.id)
			}
		}
	}
	if got := strings.Join(open[catDisplay], " "); got != "brightness auto dimmest" && got != "brightness auto" {
		t.Errorf("Display: open rows %q, want the brightness ones", got)
	}
	for _, id := range open[catSound] {
		if !strings.Contains(" volume voicevol callvol bass treble output mic waketone sleep ", " "+id+" ") {
			t.Errorf("Sound: %q is open", id)
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
