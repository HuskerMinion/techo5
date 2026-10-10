//go:build !dot

package display

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
	"github.com/HuskerMinion/techo5/echod/internal/feature/setup"
)

// The PIN pad: what a setting behind the settings lock (security/lock.go) shows first while the lock is
// on. A right PIN changes the setting that was tapped, and leaves the rest open for a while; Cancel, or
// half a minute of nothing, puts the pad away. The same pad is drawn on the Show and on the Spot, each
// to its own shape.

const pinIdle = 30 * time.Second

// pinKeys are the pad's keys in reading order: three across, four down.
var pinKeys = []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "back", "0", "ok"}

type pinView struct {
	open   bool
	digits int    // how many have been typed; the pad shows dots, never the digits
	msg    string // what went wrong last, or empty
	title  string // what the pad is asking for
}

var pinPad struct {
	mu    sync.Mutex
	open  bool
	entry string
	msg   string
	at    time.Time
	after func() // what opens once the PIN is right

	// setting is a new PIN being chosen rather than one being asked for: typed, then typed again, with
	// first the first of the two.
	setting bool
	first   string
}

// openPIN puts the pad up for a tap on the device, with after to run once the right PIN is in: the
// person at the device decides what the PIN is for.
func openPIN(after func()) {
	pinPad.mu.Lock()
	pinPad.open, pinPad.entry, pinPad.msg, pinPad.at, pinPad.after = true, "", "", time.Now(), after
	pinPad.setting, pinPad.first = false, ""
	pinPad.mu.Unlock()
}

// openPINSet puts the pad up for a new PIN, from the settings' own row.
func openPINSet() {
	pinPad.mu.Lock()
	pinPad.open, pinPad.entry, pinPad.msg, pinPad.at, pinPad.after = true, "", "", time.Now(), nil
	pinPad.setting, pinPad.first = true, ""
	pinPad.mu.Unlock()
}

// pinNow is the pad as it is to be drawn, closing it first when it has sat idle too long.
func pinNow(now time.Time) pinView {
	pinPad.mu.Lock()
	defer pinPad.mu.Unlock()
	if pinPad.open && now.Sub(pinPad.at) > pinIdle {
		pinPad.open, pinPad.entry, pinPad.msg, pinPad.after = false, "", "", nil
	}
	title := "Enter the PIN"
	switch {
	case pinPad.setting && pinPad.first == "":
		title = "A new PIN, 4 to 8 digits"
	case pinPad.setting:
		title = "The same PIN again"
	}
	return pinView{open: pinPad.open, digits: len(pinPad.entry), msg: pinPad.msg, title: title}
}

func pinIsOpen() bool { return pinNow(time.Now()).open }

// everyday is whether the settings row id is part of using the device rather than setting it up, and
// so open to everyone while the lock is on: the alarms and timers, the alarm editor, the volume, the
// tone, the headphones, the microphone, the wake sound, the sleep timer, and the brightness. Everything
// else is behind the PIN, including rows added later: a row is open only by being named here.
func everyday(id string) bool {
	for _, prefix := range []string{"alarm:", "timer:", "e."} {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	switch id {
	case "snoozed", "snooze", "ringvol", "alarmsound", "sunrise", "sunface", "newtimer", // Alarms & Timers
		"volume", "bass", "treble", "output", "mic", "waketone", "sleep", // Sound
		"brightness", "auto", "dimmest": // Display
		return true
	}
	return false
}

// rowLocked is whether a tap on the row id asks for the PIN first.
func rowLocked(id string) bool { return id != "" && !everyday(id) && security.Locked() }

// gate runs tap, a tap on the settings row id, now, or once the PIN is right when the row is behind the
// lock. Taps on a locked row only put the pad up: they are no tries at the PIN, so they never count
// toward the wait. up says whether the settings are still showing when the PIN comes in, since a
// setting changed behind a screen that has gone is not what was asked for.
func gate(id string, up func() bool, tap func()) {
	if !rowLocked(id) {
		if security.LockSet() && !everyday(id) {
			security.KeepOpen()
		}
		tap()
		return
	}
	openPIN(func() {
		if up() {
			tap()
		}
	})
}

// sheetWasOpen is whether the settings were open at the last frame.
var sheetWasOpen atomic.Bool

// relockOnClose puts the lock back on as the settings close, however they closed: a PIN typed for one
// setting does not leave the rest open behind a screen that has gone.
func relockOnClose(open bool) {
	if sheetWasOpen.Swap(open) && !open {
		security.Relock()
	}
}

// answerSetup answers a browser asking to be let in to the setup page. Allow is behind the settings
// lock too: the setup page is every setting and more.
//
// The PIN lets in the browser that was asking when Allow was tapped, and no other: one that asked
// while the PIN was being typed was never seen. Nor does the PIN leave the settings open behind it.
func answerSetup(allow bool) {
	if allow && security.Locked() {
		asking := setup.Get().Asking()
		openPIN(func() {
			setup.Get().AllowAsking(asking)
			security.Relock()
		})
		return
	}
	setup.Get().Answer(allow)
}

// pinPress is a key on the pad: a digit, back, ok, or cancel.
func pinPress(key string) {
	pinPad.mu.Lock()
	if !pinPad.open {
		pinPad.mu.Unlock()
		return
	}
	pinPad.at = time.Now()
	switch key {
	case "cancel":
		pinPad.open, pinPad.entry, pinPad.msg, pinPad.after = false, "", "", nil
		pinPad.mu.Unlock()
		return
	case "back":
		if n := len(pinPad.entry); n > 0 {
			pinPad.entry = pinPad.entry[:n-1]
		}
		pinPad.mu.Unlock()
		return
	case "ok":
		if len(pinPad.entry) < 4 {
			// Too short to be any PIN: not a wrong try, so it does not count toward the wait.
			pinPad.msg = "At least 4 digits"
			pinPad.mu.Unlock()
			return
		}
	default:
		if len(key) == 1 && key[0] >= '0' && key[0] <= '9' && len(pinPad.entry) < 8 {
			pinPad.entry += key
			pinPad.msg = ""
		}
		pinPad.mu.Unlock()
		return
	}
	entry := pinPad.entry
	pinPad.entry = ""
	if pinPad.setting {
		pinPad.mu.Unlock()
		pinSetting(entry)
		return
	}
	pinPad.mu.Unlock()

	ok, wait := security.TryPIN(entry)
	pinPad.mu.Lock()
	if !ok {
		pinPad.msg = "Wrong PIN"
		if wait > 0 {
			pinPad.msg = fmt.Sprintf("Try again in %s", waitWords(wait))
		}
		pinPad.mu.Unlock()
		return
	}
	after := pinPad.after
	pinPad.open, pinPad.msg, pinPad.after = false, "", nil
	pinPad.mu.Unlock()
	if after != nil {
		after()
	}
}

func waitWords(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d s", int((d+time.Second-1)/time.Second))
	}
	return fmt.Sprintf("%d min", int((d+time.Minute-1)/time.Minute))
}

// pinSetting is OK while a new PIN is being chosen: the first time it is kept, the second it has to
// match, and then it is the PIN.
func pinSetting(entry string) {
	pinPad.mu.Lock()
	switch {
	case len(entry) < 4:
		pinPad.msg = "At least 4 digits"
	case pinPad.first == "":
		pinPad.first, pinPad.msg = entry, ""
	case entry != pinPad.first:
		pinPad.first, pinPad.msg = "", "They did not match"
	default:
		// Saving writes the config and tells its listeners: not with the pad's lock held.
		pinPad.mu.Unlock()
		err := security.Get().SetPIN(entry)
		if err == nil {
			// Set from inside the settings: they stay open until they close.
			security.TryPIN(entry)
		}
		pinPad.mu.Lock()
		if err != nil {
			pinPad.msg = "Could not save it"
		} else {
			pinPad.open, pinPad.setting, pinPad.first, pinPad.msg = false, false, "", ""
		}
	}
	pinPad.mu.Unlock()
}
