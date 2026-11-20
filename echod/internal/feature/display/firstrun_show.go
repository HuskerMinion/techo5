//go:build !dot && !spot

package display

import (
	"image"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/firstrun"
)

// First run on a Show's screen (and the Skylight's, and a Panel's): a card a step, its buttons along
// the foot, the Wi-Fi page and the settings' time zone list opened over it, and the Wi-Fi page's
// keyboard for a name or a room.

// firstRunActive is whether first run has the screen: still to do, and neither the Wi-Fi page nor the
// settings it opens over it are up. Those draw and take touches as they always do, and first run comes
// back when they close.
func (d *Display) firstRunActive() bool {
	if !firstrun.Pending() {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.sheet && !d.wifiOpen
}

// firstRunButtons are a step's buttons, the one to press last.
func firstRunButtons(v firstRunView) []frButton {
	if v.typing != "" {
		return nil
	}
	switch v.step {
	case frLanguage:
		return []frButton{frClockButton(v.clock24), {tr("firstrun_next"), "next"}}
	case frWifi:
		if v.wifi.Connected {
			return []frButton{{tr("firstrun_change"), "wifi"}, {tr("firstrun_next"), "next"}}
		}
		return []frButton{{tr("firstrun_not_now"), "next"}, {tr("firstrun_choose_wifi"), "wifi"}}
	case frZone:
		return []frButton{{tr("firstrun_change"), "zone"}, {tr("firstrun_next"), "next"}}
	case frName:
		if v.renaming {
			return nil
		}
		return []frButton{{tr("firstrun_change_name"), "rename"}, {tr("firstrun_next"), "next"}}
	case frHome:
		switch {
		case v.home:
			return []frButton{{tr("firstrun_next"), "next"}}
		case !v.adopt.IsZero() || v.unkeyed:
			return []frButton{{tr("firstrun_skip_for_now"), "next"}}
		case v.showKey || v.wifi.Address == "":
			if v.wifi.Address == "" {
				return []frButton{{tr("firstrun_use_it_on_its_own"), "next"}}
			}
			return []frButton{{tr("firstrun_on_its_own"), "next"}, {tr("firstrun_add_it_instead"), "adopt"}}
		}
		return []frButton{{tr("firstrun_on_its_own"), "next"}, {tr("firstrun_type_the_key"), "key"}, {tr("firstrun_add_it"), "adopt"}}
	case frUpdates:
		if v.auto {
			return []frButton{{tr("firstrun_leave_them_off"), "auto"}, {tr("firstrun_next"), "next"}}
		}
		return []frButton{{tr("firstrun_turn_them_on"), "auto"}, {tr("firstrun_next"), "next"}}
	case frSound:
		return []frButton{{tr("firstrun_play_again"), "again"}, {tr("firstrun_yes"), "next"}}
	}
	return nil
}

// firstRunTap is a tap while first run has the screen. The hello and done cards take a tap anywhere.
func (d *Display) firstRunTap(x, y int) {
	switch step := firstRunStep(); step {
	case frHello:
		d.firstRunGo(stepAfter(step))
		return
	case frDone:
		d.firstRunFinish()
		return
	}
	if d.r == nil {
		return
	}
	v := d.firstRunView(time.Now())
	if v.typing != "" {
		d.firstRunKey(d.r.keyAt(x, y, v.symbols))
		return
	}
	if v.step == frLanguage {
		for i, b := range d.r.frLangRects() {
			if image.Pt(x, y).In(b) {
				// The language already lit is left as it is: a tap to confirm keeps Match all, and the
				// clock chosen.
				if frLangs[i].Code != v.lang {
					d.firstRunLanguage(frLangs[i].Code)
				}
				return
			}
		}
	}
	if v.step == frName && !v.home && !v.renaming {
		for i, b := range d.r.frRoomRects() {
			if image.Pt(x, y).In(b) {
				d.firstRunRoom(frRooms[i])
				return
			}
		}
	}
	if frShowsBack(v) && image.Pt(x, y).In(d.r.frBackRect().Inset(-d.r.s(12))) {
		d.firstRunBack(v.step)
		return
	}
	buttons := firstRunButtons(v)
	for i, b := range d.r.frButtonRects(buttons) {
		if image.Pt(x, y).In(b) {
			d.firstRunDo(v.step, buttons[i].act)
			return
		}
	}
}

// firstRunRoom is a tap on one of the rooms: it is the room now, or Other opens the keyboard for one.
func (d *Display) firstRunRoom(room string) {
	if room == "Other" {
		d.firstRunType("room", config.Get().Home.Area)
		return
	}
	if err := firstrun.SetArea(room); err != nil {
		slog.Warn("saving the room failed", "err", err)
	}
	d.wake()
}

// firstRunType opens the keyboard for a name or a room, with what is there now in the field.
func (d *Display) firstRunType(what, now string) {
	d.mu.Lock()
	fr := &d.firstRun
	fr.typing, fr.text, fr.typeErr, fr.shifted, fr.symbols = what, now, "", false, false
	d.mu.Unlock()
	d.wake()
}

// firstRunKey is a key on first run's keyboard: Cancel puts it away, the last key saves.
func (d *Display) firstRunKey(k string) {
	d.mu.Lock()
	fr := &d.firstRun
	what, text := fr.typing, fr.text
	if k != "" {
		fr.typeErr = ""
	}
	switch k {
	case "":
	case "shift":
		fr.shifted = !fr.shifted
	case "symbols":
		fr.symbols = !fr.symbols
	case "space":
		fr.text += " "
	case "backspace":
		if _, n := utf8.DecodeLastRuneInString(fr.text); n > 0 {
			fr.text = fr.text[:len(fr.text)-n]
		}
	case "cancel":
		fr.typing, fr.text = "", ""
	case "join":
		d.mu.Unlock()
		d.firstRunSave(what, strings.TrimSpace(text))
		d.wake()
		return
	default:
		if fr.shifted && !fr.symbols {
			k = strings.ToUpper(k)
			fr.shifted = false
		}
		if utf8.RuneCountInString(fr.text) < frNameLimit {
			fr.text += k
		}
	}
	d.mu.Unlock()
	d.wake()
}

// firstRunSave saves what the keyboard typed. A name restarts the daemon to announce it (setup's
// rename), and first run comes back on this step with it.
func (d *Display) firstRunSave(what, text string) {
	if what == "room" {
		if text == "" {
			d.firstRunTypeError(tr("firstrun_type_a_room"))
			return
		}
		if err := firstrun.SetArea(text); err != nil {
			d.firstRunTypeError(tr("firstrun_room_not_saved"))
			return
		}
		d.mu.Lock()
		d.firstRun.typing, d.firstRun.text = "", ""
		d.mu.Unlock()
		return
	}
	if text == config.Get().Device.Name {
		d.mu.Lock()
		d.firstRun.typing, d.firstRun.text = "", ""
		d.mu.Unlock()
		return
	}
	if text == "" {
		// The one refusal this keyboard can bring about (it types one line, within the limit), in the
		// screen's words; the setup page's others stay its own.
		d.firstRunTypeError(tr("firstrun_needs_a_name"))
		return
	}
	if msg := firstrun.Rename(text); msg != "" {
		d.firstRunTypeError(capitalize(msg))
		return
	}
	d.mu.Lock()
	d.firstRun.typing, d.firstRun.text, d.firstRun.renaming = "", "", true
	d.mu.Unlock()
}

// firstRunTypeError says in the field why what was typed was not saved.
func (d *Display) firstRunTypeError(msg string) {
	d.mu.Lock()
	d.firstRun.typeErr = msg
	d.mu.Unlock()
}

// firstRunDo does what a step's button says.
func (d *Display) firstRunDo(step, act string) {
	switch act {
	case "next":
		d.firstRunGo(stepAfter(step))
	case "clock":
		d.firstRunClock()
	case "wifi":
		d.openWifi()
	case "zone":
		// The settings' own time zone list, over first run: closing it comes back here.
		d.mu.Lock()
		d.sheet, d.cat, d.picker, d.restartArm = true, catGeneral, "timezone", time.Time{}
		d.draft, d.cardScroll, d.pickScroll = nil, 0, 0
		d.mu.Unlock()
		d.wake()
	case "rename":
		d.firstRunType("name", config.Get().Device.Name)
	case "key":
		d.mu.Lock()
		d.firstRun.showKey = true
		d.mu.Unlock()
		d.wake()
	case "adopt":
		// The setup page's "Let a Home Assistant add this device" (api/adopt.go), from the device's own
		// screen: somebody is at it. For its fifteen minutes the device has no key, and the Home
		// Assistant that adds it gives it one; if none does, it shuts with a new key, which the key
		// view then shows.
		if _, err := firstrun.OpenAdoption(); err != nil {
			slog.Warn("opening the window for Home Assistant failed", "err", err)
		}
		d.mu.Lock()
		d.firstRun.showKey = false
		d.mu.Unlock()
		d.wake()
	case "auto":
		firstrun.SetAutoInstall(!firstrun.AutoInstall())
		d.wake()
	case "again":
		go firstrun.Chime()
	}
}

// firstRunLanguage makes lang the screen's language, and its usual clock; the rest of first run is in
// its words from the next frame.
func (d *Display) firstRunLanguage(lang string) {
	if err := firstrun.SetLanguage(lang); err != nil {
		slog.Warn("saving the screen language failed", "err", err)
	}
	setClockFormat(d.clock, config.Get().Screen.Clock24, config.Get().Screen.NoAMPM)
	d.wake()
}

// firstRunClock turns the clock between 12 and 24 hours.
func (d *Display) firstRunClock() {
	if err := firstrun.SetClock24(!config.Get().Screen.Clock24); err != nil {
		slog.Warn("saving the clock format failed", "err", err)
	}
	setClockFormat(d.clock, config.Get().Screen.Clock24, config.Get().Screen.NoAMPM)
	d.wake()
}
