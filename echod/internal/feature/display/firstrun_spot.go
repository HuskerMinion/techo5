//go:build spot

package display

import (
	"fmt"
	"image"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/firstrun"
	"github.com/HuskerMinion/techo5/echod/internal/feature/setup"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
)

// First run on the Spot's round screen: the same steps as a Show's, a face a step, the buttons
// stacked where the circle is widest. The Spot has no keyboard and no Wi-Fi page of its own, so what
// has to be typed (another network, a name, a zone from the whole list) is done on a phone: "On a
// phone" opens the setup page, whose first run has the same steps as forms, and the Spot's face asks
// for the press that lets the phone in, as it always does.

// firstRunActive is whether first run has the screen.
func (d *Display) firstRunActive() bool { return firstrun.Pending() }

// spotFirstRunButtons are a step's buttons, top to bottom, the one to press last.
func spotFirstRunButtons(v firstRunView) []frButton {
	switch v.step {
	case frWifi:
		return []frButton{{"Next", "next"}}
	case frZone:
		return []frButton{{"On a phone", "phone"}, {"Next", "next"}}
	case frName:
		switch {
		case v.renaming:
			return nil
		case v.home:
			return []frButton{{"On a phone", "phone"}, {"Next", "next"}}
		}
		room := "Choose a room"
		if v.room != "" {
			room = "Room: " + v.room
		}
		return []frButton{{room, "room"}, {"On a phone", "phone"}, {"Next", "next"}}
	case frHome:
		switch {
		case v.showKey && v.key != "":
			return nil // a tap anywhere puts the key away
		case v.home:
			return []frButton{{"Next", "next"}}
		case v.wifi.Address == "":
			return []frButton{{"Use it on its own", "next"}}
		case !v.adopt.IsZero() || v.unkeyed:
			return []frButton{{"Skip for now", "next"}}
		}
		return []frButton{{"On its own", "next"}, {"Show the key", "key"}, {"Add it", "adopt"}}
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

// spotFirstRunRects are where n buttons go, stacked up from the circle's foot.
func spotFirstRunRects(n int) []image.Rectangle {
	const w, h, pitch, last = 280, 46, 58, 404
	out := make([]image.Rectangle, n)
	for i := range n {
		cy := last - (n-1-i)*pitch
		out[i] = image.Rect(center-w/2, cy-h/2, center+w/2, cy+h/2)
	}
	return out
}

// firstRunTapSpot is a tap while first run has the screen.
func (d *Display) firstRunTapSpot(x, y int) {
	v := d.firstRunView(time.Now())
	switch v.step {
	case frHello:
		d.firstRunGo(stepAfter(v.step))
		return
	case frDone:
		d.firstRunFinish()
		return
	}
	if v.step == frHome && v.showKey && v.key != "" {
		d.mu.Lock()
		d.firstRun.showKey = false
		d.mu.Unlock()
		d.wake()
		return
	}
	buttons := spotFirstRunButtons(v)
	for i, b := range spotFirstRunRects(len(buttons)) {
		if image.Pt(x, y).In(b.Inset(-6)) {
			d.firstRunDoSpot(v, buttons[i].act)
			return
		}
	}
}

// firstRunDoSpot does what a step's button says.
func (d *Display) firstRunDoSpot(v firstRunView, act string) {
	switch act {
	case "next":
		d.firstRunGo(stepAfter(v.step))
	case "phone":
		// The setup page, opened as Privacy opens it: the phone asks, and this face asks for the press.
		setup.Get().Open()
		d.mu.Lock()
		d.firstRun.phone = true // the face says where to go
		d.mu.Unlock()
	case "room":
		if err := firstrun.SetArea(nextRoom(v.room)); err != nil {
			slog.Warn("saving the room failed", "err", err)
		}
	case "key":
		d.mu.Lock()
		d.firstRun.showKey = true
		d.mu.Unlock()
	case "adopt":
		if _, err := firstrun.OpenAdoption(); err != nil {
			slog.Warn("opening the window for Home Assistant failed", "err", err)
		}
	case "auto":
		firstrun.SetAutoInstall(!firstrun.AutoInstall())
	case "again":
		go firstrun.Chime()
	}
	d.wake()
}

// nextRoom is the listed room after room, round again after the last; Other is the setup page's.
func nextRoom(room string) string {
	listed := frRooms[:len(frRooms)-1]
	for i, k := range listed {
		if k == room {
			return listed[(i+1)%len(listed)]
		}
	}
	return listed[0]
}

// firstRunFaceSpot draws the step.
func (r *roundRenderer) firstRunFaceSpot(v firstRunView, now time.Time) {
	if n, of := v.number, v.of; n > 0 {
		r.centered(r.tiny, fmt.Sprintf("Step %d of %d", n, of), 74, colDim)
	}
	title, lines := spotFirstRunText(v, now)
	r.centered(r.title, title, 126, colText)
	if v.step == frHome && v.showKey && v.key != "" {
		// The QR code in the middle, the key under it in two lines, and a tap anywhere to go back.
		side := 176
		r.qrCode(v.key, image.Rect(center-side/2, 146, center+side/2, 146+side))
		half := (len(v.key) + 1) / 2
		r.centered(r.tiny, v.key[:half], 352, colText)
		r.centered(r.tiny, v.key[half:], 378, colText)
		r.centered(r.tiny, "Tap to go back", 412, colDim)
		return
	}
	buttons := spotFirstRunButtons(v)
	rects := spotFirstRunRects(len(buttons))
	limit := 440
	if len(rects) > 0 {
		limit = rects[0].Min.Y - 8
	}
	y := 170
	for _, para := range lines {
		for _, line := range r.wrap(r.small, para, 340) {
			if y > limit {
				break
			}
			r.centered(r.small, line, y, colText)
			y += 30
		}
	}
	for i, b := range rects {
		style := btnSecondary
		if i == len(rects)-1 {
			style = btnPrimary
		}
		r.labelButton(b, buttons[i].label, style)
	}
}

// spotFirstRunText is a step's title and lines on the Spot: shorter than a Show's, for the circle.
func spotFirstRunText(v firstRunView, now time.Time) (string, []string) {
	phone := ""
	if v.phone && v.wifi.Address != "" {
		phone = fmt.Sprintf("On a phone: %s:%d/setup", v.wifi.Address, web.Port)
	}
	with := func(lines ...string) []string {
		if phone != "" {
			lines = append(lines, phone)
		}
		return lines
	}
	switch v.step {
	case frHello:
		return "Hello", []string{"This is " + v.name + ".", "A few steps set it up.", "Tap to begin."}
	case frDone:
		return "All set", []string{v.name + " is set up.", "Swipe down for settings.", "Tap to finish."}
	case frWifi:
		if v.wifi.Connected {
			return "Wi-Fi", []string{"On " + v.wifi.SSID + ".", "At " + v.wifi.Address + "."}
		}
		return "Wi-Fi", []string{"Not on a network.", "Install again with your Wi-Fi to put it on one."}
	case frZone:
		at := now.Format("3:04 PM")
		if clock24.Load() {
			at = now.Format("15:04")
		}
		return "Time zone", with(zoneValue()+".", "The clock says "+at+".")
	case frName:
		if v.renaming {
			return "Name", []string{"Restarting to use", "the new name…"}
		}
		return "Name and room", with("Name: " + v.name)
	case frHome:
		switch {
		case v.homeJust:
			return "Home Assistant", []string{"Home Assistant has added it.", "Going on…"}
		case v.home:
			return "Home Assistant", []string{"Home Assistant has added it."}
		case v.wifi.Address == "":
			return "Home Assistant", []string{"Not on a network yet."}
		case v.unkeyed:
			return "Home Assistant", []string{"Add it in Home Assistant: Devices & services. Nothing to type.", "Only on a network you trust."}
		case !v.adopt.IsZero():
			left := max(1, int(v.adopt.Sub(now).Minutes()+0.999))
			return "Home Assistant", []string{
				"Add it in Home Assistant: Devices & services. Nothing to type.",
				fmt.Sprintf("Open %d more minutes. Only on a network you trust.", left),
			}
		}
		return "Home Assistant", []string{"Add it with nothing to type,", "or with its key."}
	case frUpdates:
		var line string
		switch {
		case !v.checked:
			line = "Looking for an update…"
		case v.offered == "":
			line = "It is up to date."
		case v.auto:
			line = v.offered + " installs tonight."
		default:
			line = v.offered + " is ready."
		}
		auto := "Updates wait for you."
		if v.auto {
			auto = "Updates install overnight."
		}
		return "Updates", []string{line, auto}
	case frSound:
		return "Sound check", []string{"Did you hear that?"}
	}
	return "", nil
}
