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
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
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
	what   string // the setting the PIN is for, or empty for the settings as a whole
}

var pinPad struct {
	mu    sync.Mutex
	open  bool
	entry string
	msg   string
	at    time.Time
	after func() // what opens once the PIN is right
	what  string // the setting it is for, shown on the pad

	// setting is a new PIN being chosen rather than one being asked for: typed, then typed again, with
	// first the first of the two.
	setting bool
	first   string
}

// openPIN puts the pad up for a tap on the device, with after to run once the right PIN is in: the
// person at the device decides what the PIN is for. what names it on the pad, so whoever types the
// PIN can see what it will change, since that may not be what they think it is for.
func openPIN(what string, after func()) {
	pinPad.mu.Lock()
	pinPad.open, pinPad.entry, pinPad.msg, pinPad.at, pinPad.after = true, "", "", time.Now(), after
	pinPad.what = what
	pinPad.setting, pinPad.first = false, ""
	pinPad.mu.Unlock()
}

// openPINSet puts the pad up for a new PIN, from the settings' own row.
func openPINSet() {
	pinPad.mu.Lock()
	pinPad.open, pinPad.entry, pinPad.msg, pinPad.at, pinPad.after = true, "", "", time.Now(), nil
	pinPad.what = ""
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
	title := tr("pin_enter")
	switch {
	case pinPad.setting && pinPad.first == "":
		title = tr("pin_new")
	case pinPad.setting:
		title = tr("pin_again")
	}
	return pinView{open: pinPad.open, digits: len(pinPad.entry), msg: pinPad.msg, title: title, what: pinPad.what}
}

func pinIsOpen() bool { return pinNow(time.Now()).open }

// everyday is whether the settings row id is part of using the device rather than setting it up, and
// so open to everyone while the lock is on. The line: how the device looks, sounds and is used here is
// open (the alarms and timers, the volume and tone, the speaking voice, quiet hours and do not disturb,
// the brightness and the night, the skin, theme, colors, font and clock, what the clock, the calendar
// and the photos look like); what the device connects to, shares, or takes from other systems is
// behind the PIN (Wi-Fi, Bluetooth, streaming, Music Assistant, SSH, cameras and the screen on the
// network, which calendars and photo folders it reads, the weather's place, the time zone, updates),
// and so is what reaches past this device: another player's controls (Now playing follows), a
// camera's sound, the call button's contacts, the room's light as Home Assistant sees it, the wake
// word's threshold, and the presence camera. The wake word is open only while one is set: choosing a
// different word is how the device is used, but where the owner chose none, choosing one would turn
// listening back on. The voice assistant itself is chosen in Home Assistant, not here. Everything not
// named is behind the PIN, including rows added later: a row is open only by being named here.
func everyday(id string) bool {
	for _, prefix := range []string{"alarm:", "timer:", "e.", "role:"} {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	switch id {
	case "wakeword":
		return wakeWordSet()
	case "snoozed", "snooze", "ringvol", "alarmsound", "alarmwords", "alarmrise", "sunrise", "sunface", "allalarms", "newtimer", // Alarms & Timers
		"volume", "voicevol", "callvol", "bass", "treble", "output", "mic", "sleep", // Sound
		"waketone", "ttsvoice", "hasounds", "louder", "quiet", "dnd",
		"brightness", "auto", "dimmest", "idledim", "awayoff", // Display
		"night", "atnight", "nightstyle", "nightglow", "nightinfo", "nightalarm", "nighttap",
		"skin", "theme", "colors", "colorsdone", "font", "textsize",
		"clock", "clockstyle", "clockpos", "weatherpos", "datecolor", "glancesize", "glancemusic", "clocktap",
		"musicstrip", "musiclayout", "lyrics", "callscreen", "answertime", "timerstyle", "turnstyle",
		"weatherfx", "alerts", "radarsrc", "camtime", "camfit", "cambands", "camshade",
		"slideshow", "weatherart", "photoevery", "shuffle", "wholephoto", "photopairs", "photocaption", "photocountdown", // Photos
		"calpop", "calpopwhen", "calpopsound", "calpopallday", // Calendar
		// The calendar's look rows, each by name: lookRowPrefix and the ids in lookSwitches
		// (calendar_look.go, not in the Spot's build). A look row added there stays behind the PIN
		// until it is named here.
		"cal_opens_on", "cal_day_timeline", "cal_coming_up_refined", "cal_month_details", "cal_event_icons",
		"cal_countdowns", "cal_week_view", "cal_week_starts_monday", "cal_chip_size", "cal_next_up",
		"cal_dashboard_events", "cal_next_up_within", "cal_weather":
		return true
	}
	return false
}

// wakeWordSet is whether the first slot is listening for a word, which keeps the wake word row open;
// a variable for the tests.
var wakeWordSet = func() bool {
	active := voice.Get().ActiveWakeWords()
	return len(active) > 0 && active[0] != ""
}

// rowLocked is whether a tap on the row id asks for the PIN first.
func rowLocked(id string) bool { return id != "" && !everyday(id) && security.Locked() }

// gate runs tap, a tap on the settings row id, now, or once the PIN is right when the row is behind the
// lock; what is the row's name for the pad. Taps on a locked row only put the pad up: they are no tries
// at the PIN, so they never count toward the wait. A nil tap is the PIN opening the lock and nothing
// more.
//
// up says whether the settings are still showing when the PIN comes in. If they are not (closed behind
// the pad by a timeout, a page Home Assistant put up, anything), the tap is not what anybody can see
// any more, and the lock goes back on: a right PIN must not leave every setting open for whoever opens
// the screen next.
func gate(id, what string, up func() bool, tap func()) {
	if !rowLocked(id) {
		if tap != nil {
			tap()
		}
		return
	}
	openPIN(what, func() {
		if !up() {
			security.Relock()
			return
		}
		if tap != nil {
			tap()
		}
	})
}

// stillHere keeps an open lock open while the settings are being touched, everyday rows and scrolling
// included: it comes back two minutes after the last touch, not two minutes after the PIN.
func stillHere() {
	if security.LockSet() {
		security.KeepOpen()
	}
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
// Allow lets in asking, the browser whose page was tapped, and no other, with the PIN or without: one
// that asked while the PIN was being typed, or since the page was drawn, was never seen. Nor does the
// PIN leave the settings open behind it.
func answerSetup(allow bool, asking string) {
	switch {
	case !allow:
		setup.Get().Answer(false)
	case security.Locked():
		openPIN(tr("ui_setup_page"), func() {
			setup.Get().AllowAsking(asking)
			security.Relock()
		})
	default:
		setup.Get().AllowAsking(asking)
	}
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
			pinPad.msg = tr("pin_too_short")
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
		pinPad.msg = tr("pin_wrong")
		if wait > 0 {
			pinPad.msg = fmt.Sprintf(tr("pin_try_again_in"), waitWords(wait))
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
		return fmt.Sprintf(tr("pin_wait_seconds"), int((d+time.Second-1)/time.Second))
	}
	return fmt.Sprintf(tr("pin_wait_minutes"), int((d+time.Minute-1)/time.Minute))
}

// pinSetting is OK while a new PIN is being chosen: the first time it is kept, the second it has to
// match, and then it is the PIN.
func pinSetting(entry string) {
	pinPad.mu.Lock()
	switch {
	case len(entry) < 4:
		pinPad.msg = tr("pin_too_short")
	case pinPad.first == "":
		pinPad.first, pinPad.msg = entry, ""
	case entry != pinPad.first:
		pinPad.first, pinPad.msg = "", tr("pin_no_match")
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
			pinPad.msg = tr("pin_not_saved")
		} else {
			pinPad.open, pinPad.setting, pinPad.first, pinPad.msg = false, false, "", ""
		}
	}
	pinPad.mu.Unlock()
}
