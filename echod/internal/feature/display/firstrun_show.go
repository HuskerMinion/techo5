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
	case frWifi:
		if v.wifi.Connected {
			return []frButton{{"Change", "wifi"}, {"Next", "next"}}
		}
		return []frButton{{"Not now", "next"}, {"Choose Wi-Fi", "wifi"}}
	case frZone:
		return []frButton{{"Change", "zone"}, {"Next", "next"}}
	case frName:
		if v.renaming {
			return nil
		}
		return []frButton{{"Change name", "rename"}, {"Next", "next"}}
	case frHome:
		switch {
		case v.home:
			return []frButton{{"Next", "next"}}
		case !v.adopt.IsZero() || v.unkeyed:
			return []frButton{{"Skip for now", "next"}}
		case v.showKey || v.wifi.Address == "":
			if v.wifi.Address == "" {
				return []frButton{{"Use it on its own", "next"}}
			}
			return []frButton{{"On its own", "next"}, {"Add it instead", "adopt"}}
		}
		return []frButton{{"On its own", "next"}, {"Type the key", "key"}, {"Add it", "adopt"}}
	case frUpdates:
		if v.auto {
			return []frButton{{"Leave them off", "auto"}, {"Next", "next"}}
		}
		return []frButton{{"Turn them on", "auto"}, {"Next", "next"}}
	case frSound:
		return []frButton{{"Play again", "again"}, {"Yes", "next"}}
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
	if v.step == frName && !v.home && !v.renaming {
		for i, b := range d.r.frRoomRects() {
			if image.Pt(x, y).In(b) {
				d.firstRunRoom(frRooms[i])
				return
			}
		}
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
			d.firstRunTypeError("Type a room, or Cancel")
			return
		}
		if err := firstrun.SetArea(text); err != nil {
			d.firstRunTypeError("Could not save the room")
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
