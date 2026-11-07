//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"time"
)

// firstRun draws first run's step (firstrun.go): the welcome card at either end, and between them a
// card with a title, a few lines and the step's buttons along its foot. The name step lays the rooms
// out over the lines' foot, and the Home Assistant step's key view puts the key's QR code at the right.
func (r *renderer) firstRun(s scene, v firstRunView) {
	switch v.step {
	case frHello:
		r.welcomeCard("Tap anywhere to begin")
		return
	case frDone:
		r.welcomeCard("Tap anywhere to finish")
		return
	}
	r.useFont()
	if v.typing != "" {
		fillCanvas(r.dst, walnut)
		title, goLabel := "Name this device", "Save"
		if v.typing == "room" {
			title = "The room it is in"
		}
		shown, message := v.text, false
		if v.typeErr != "" {
			shown, message = v.typeErr, true
		}
		r.keyboard(title, shown, message, v.shifted, v.symbols, goLabel)
		return
	}
	r.bevel(image.Rect(r.margin, r.s(40), r.w-r.margin, r.h-r.s(40)), shift(walnut, 10), true)
	left := r.margin + r.s(40)
	room := r.w - 2*r.margin - r.s(80)
	keyView := v.step == frHome && !v.home && v.adopt.IsZero() && v.showKey && v.key != "" && v.wifi.Address != ""
	var code image.Rectangle
	if keyView {
		// The QR code holds the key and nothing else: a phone reads it and pastes it into Home
		// Assistant's box. The typed key stays beside it.
		side := r.s(200)
		code = image.Rect(r.w-r.margin-r.s(40)-side, r.s(140), r.w-r.margin-r.s(40), r.s(140)+side)
		room = code.Min.X - left - r.s(24)
	}
	title, lines, note := firstRunText(v, s.now)
	r.text(r.title, title, left, r.s(118), amber)
	count := fmt.Sprintf("Step %d of %d", v.number, v.of)
	r.text(r.small, count, r.w-r.margin-r.s(40)-r.width(r.small, count), r.s(110), dim)
	y := r.s(170)
	for _, para := range lines {
		for _, line := range r.wrap(r.small, para, room) {
			r.text(r.small, line, left, y, cream)
			y += r.s(40)
		}
	}
	if note != "" {
		for _, line := range r.wrap(r.tiny, note, room) {
			r.text(r.tiny, line, left, y, dim)
			y += r.s(32)
		}
	}
	if keyView {
		// Large, in as many pieces as it takes to fit: it is read off the screen and typed.
		y += r.s(6)
		face, step := r.body, r.s(44)
		parts := splitToFit(v.key, func(p string) bool { return r.width(face, p) <= room })
		if len(parts) > 2 {
			face, step = r.small, r.s(40)
			parts = splitToFit(v.key, func(p string) bool { return r.width(face, p) <= room })
		}
		for _, part := range parts {
			r.text(face, part, left, y, amber)
			y += step
		}
		r.qrCode(v.key, code)
	}
	if v.step == frName && !v.home && !v.renaming {
		r.keyRow(r.frRoomRects(), frRooms, func(i int) bool {
			return frRooms[i] == v.room || (frRooms[i] == "Other" && v.room != "" && !isListedRoom(v.room))
		})
	}
	buttons := firstRunButtons(v)
	for i, b := range r.frButtonRects(buttons) {
		last := i == len(buttons)-1
		fill, ink := shift(walnut, 30), cream
		if last {
			fill, ink = amber, walnut
		}
		r.bevel(b, fill, true)
		label := buttons[i].label
		r.text(r.body, label, b.Min.X+(b.Dx()-r.width(r.body, label))/2, b.Min.Y+b.Dy()/2+r.s(12), ink)
	}
}

// isListedRoom is whether room is one of the rooms offered, rather than one typed under Other.
func isListedRoom(room string) bool {
	for _, k := range frRooms[:len(frRooms)-1] {
		if k == room {
			return true
		}
	}
	return false
}

// frRoomRects are where the name step's rooms go: two rows of four, over the buttons.
func (r *renderer) frRoomRects() []image.Rectangle {
	left := r.margin + r.s(40)
	room := r.w - 2*r.margin - r.s(80)
	gap, h := r.s(14), r.s(50)
	per := (len(frRooms) + 1) / 2
	w := (room - (per-1)*gap) / per
	top := r.h - r.s(64) - r.s(72) - r.s(16) - 2*h - gap
	out := make([]image.Rectangle, len(frRooms))
	for i := range frRooms {
		x, y := left+(i%per)*(w+gap), top+(i/per)*(h+gap)
		out[i] = image.Rect(x, y, x+w, y+h)
	}
	return out
}

// frButtonRects are where a step's buttons go: along the card's foot, from the right, the last one
// rightmost, each as wide as its label needs. Taps are read against the same rectangles.
func (r *renderer) frButtonRects(buttons []frButton) []image.Rectangle {
	r.useFont()
	bottom := r.h - r.s(64)
	top := bottom - r.s(72)
	gap := r.s(24)
	right := r.w - r.margin - r.s(40)
	out := make([]image.Rectangle, len(buttons))
	for i := len(buttons) - 1; i >= 0; i-- {
		w := max(r.s(180), r.width(r.body, buttons[i].label)+r.s(56))
		out[i] = image.Rect(right-w, top, right, bottom)
		right -= w + gap
	}
	return out
}

// firstRunText is a step's title, its lines, and a smaller note under them.
func firstRunText(v firstRunView, now time.Time) (string, []string, string) {
	switch v.step {
	case frWifi:
		if !v.wifi.Connected {
			return "Wi-Fi", []string{
				"Not on a network yet. Choose your Wi-Fi to connect " + v.name + ".",
				"Everything after this needs it: Home Assistant, the weather, the radio and updates.",
			}, ""
		}
		lines := []string{"Connected to " + v.wifi.SSID + "."}
		if v.wifi.Address != "" {
			lines = append(lines, "Its address on the network is "+v.wifi.Address+".")
		}
		return "Wi-Fi", lines, ""
	case frZone:
		at := now.Format("3:04 PM")
		if clock24.Load() {
			at = now.Format("15:04")
		}
		return "Time zone", []string{
			"Time zone: " + zoneValue() + ".",
			"The clock says " + at + ". Change the zone if that is not the time here.",
		}, ""
	case frName:
		// One line: the rooms take the rest of the card.
		switch {
		case v.renaming:
			return "Name and room", []string{"Name: " + v.name + ".", "Restarting to use the new name…"}, ""
		case v.home:
			return "Name and room", []string{"Name: " + v.name + ".", "Its room is set in Home Assistant."}, ""
		case v.room == "":
			return "Name and room", []string{"Name: " + v.name + ". Which room is it in?"}, ""
		}
		return "Name and room", []string{"Name: " + v.name + ". Room: " + v.room + "."}, ""
	case frHome:
		switch {
		case v.homeJust:
			return "Home Assistant", []string{"Home Assistant has added it. Going on…"}, ""
		case v.home:
			return "Home Assistant", []string{"Home Assistant has added it."}, ""
		case v.wifi.Address == "":
			return "Home Assistant", []string{"Not on a network yet, so Home Assistant cannot reach it."}, ""
		case v.unkeyed:
			return "Home Assistant", []string{
				"Add it in Home Assistant: it appears under Settings, Devices & services. Nothing to type.",
			}, "Home Assistant sends the device its key unencrypted, so add it on a network you trust. Needs Home Assistant 2026.9 or later."
		case !v.adopt.IsZero():
			left := max(1, int(v.adopt.Sub(now).Minutes()+0.999))
			return "Home Assistant", []string{
				"Add it in Home Assistant: it appears under Settings, Devices & services. Nothing to type.",
				fmt.Sprintf("Open for %d more minutes.", left),
			}, "Home Assistant sends the device its new key unencrypted, so do this only on a network you trust. Needs Home Assistant 2026.9 or later."
		case v.showKey && v.key != "":
			return "Home Assistant", []string{
				"Add ESPHome with " + v.wifi.Address + ", port 6053, and this key:",
			}, ""
		}
		return "Home Assistant", []string{
			"Add it to Home Assistant with nothing to type, or type its key.",
			"Or use it on its own: alarms, timers, the radio and the clock work without Home Assistant.",
		}, ""
	case frUpdates:
		var lines []string
		switch {
		case !v.checked:
			lines = append(lines, "Looking for an update…")
		case v.offered == "":
			lines = append(lines, "It is up to date.")
		case v.auto:
			lines = append(lines, "TECHO5 "+v.offered+" is ready. It installs tonight, after setup.")
		default:
			lines = append(lines, "TECHO5 "+v.offered+" is ready: install it from Settings, General.")
		}
		if v.auto {
			lines = append(lines, "Updates install by themselves overnight (2 to 5 AM), while nothing plays.")
		} else {
			lines = append(lines, "Updates wait for you to install them.")
		}
		return "Updates", lines, ""
	case frSound:
		return "Sound check", []string{"A short sound just played. Did you hear it?"}, ""
	}
	return "", nil, ""
}
