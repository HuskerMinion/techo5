//go:build spot

package display

import (
	"fmt"
	"image"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
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
	case frLanguage:
		return []frButton{{frLangName(v.lang), "lang"}, frClockButton(v.clock24), {tr("firstrun_next"), "next"}}
	case frWifi:
		return []frButton{{tr("firstrun_next"), "next"}}
	case frZone:
		return []frButton{{tr("firstrun_on_a_phone"), "phone"}, {tr("firstrun_next"), "next"}}
	case frName:
		switch {
		case v.renaming:
			return nil
		case v.home:
			return []frButton{{tr("firstrun_on_a_phone"), "phone"}, {tr("firstrun_next"), "next"}}
		}
		room := tr("firstrun_choose_a_room")
		if v.room != "" {
			room = fmt.Sprintf(tr("firstrun_room_s"), v.room)
		}
		return []frButton{{room, "room"}, {tr("firstrun_on_a_phone"), "phone"}, {tr("firstrun_next"), "next"}}
	case frHome:
		switch {
		case v.showKey && v.key != "":
			return nil // a tap anywhere puts the key away
		case v.home:
			return []frButton{{tr("firstrun_next"), "next"}}
		case v.wifi.Address == "":
			return []frButton{{tr("firstrun_use_it_on_its_own"), "next"}}
		case !v.adopt.IsZero() || v.unkeyed:
			return []frButton{{tr("firstrun_skip_for_now"), "next"}}
		}
		return []frButton{{tr("firstrun_on_its_own"), "next"}, {tr("firstrun_show_the_key"), "key"}, {tr("firstrun_add_it"), "adopt"}}
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
	if spotShowsBack(v) && image.Pt(x, y).In(spotBackRect) {
		d.firstRunBack(v.step)
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
		if v.step == frLanguage {
			d.firstRunSavePick()
		}
		d.firstRunGo(stepAfter(v.step))
	case "phone":
		// The setup page, opened as Privacy opens it: the phone asks, and this face asks for the press.
		setup.Get().Open()
		d.mu.Lock()
		d.firstRun.phone = true // the face says where to go
		d.mu.Unlock()
	case "lang":
		d.mu.Lock()
		fr := &d.firstRun
		fr.picking, fr.pickLang = true, nextLang(v.lang)
		if fr.pickedClock {
			fr.pickClock24 = v.clock24
		} else {
			fr.pickClock24 = firstrun.Clock24For(fr.pickLang)
		}
		d.mu.Unlock()
	case "clock":
		d.mu.Lock()
		fr := &d.firstRun
		fr.picking, fr.pickedClock = true, true
		fr.pickLang, fr.pickClock24 = v.lang, !v.clock24
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

// firstRunSavePick saves the language and clock picked on the language step, if any were, and lets
// the pick go.
func (d *Display) firstRunSavePick() {
	d.mu.Lock()
	fr := &d.firstRun
	picking, lang, on := fr.picking, fr.pickLang, fr.pickClock24
	fr.picking, fr.pickedClock = false, false
	d.mu.Unlock()
	if !picking {
		return
	}
	if err := firstrun.SaveLanguage(lang, on); err != nil {
		slog.Warn("saving the screen language failed", "err", err)
	}
	setClockFormat(d.clock, config.Get().Screen.Clock24, config.Get().Screen.NoAMPM)
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

// nextLang is the language after lang, round again after the last.
func nextLang(lang string) string {
	for i, l := range frLangs {
		if l.Code == lang {
			return frLangs[(i+1)%len(frLangs)].Code
		}
	}
	return frLangs[0].Code
}

// frLangName is lang in its own words.
func frLangName(lang string) string {
	for _, l := range frLangs {
		if l.Code == lang {
			return frLangLabel(l)
		}
	}
	return lang
}

// spotBackRect is where Back is tapped: over the step's count, at the top of the circle, apart from
// the buttons stacked at its foot, which have no room for a fourth.
var spotBackRect = image.Rect(center-100, 14, center+100, 58)

// spotShowsBack is whether the step shows Back: on the steps that have one (firstrun.HasBack), and
// not while a rename restarts the daemon or the key is up (a tap anywhere puts it away).
func spotShowsBack(v firstRunView) bool {
	return firstrun.HasBack(v.step) && !v.renaming && !(v.step == frHome && v.showKey && v.key != "")
}

// firstRunFaceSpot draws the step.
func (r *roundRenderer) firstRunFaceSpot(v firstRunView, now time.Time) {
	if spotShowsBack(v) {
		r.centered(r.tiny, "‹ "+tr("back"), 44, colDim)
	}
	if n, of := v.number, v.of; n > 0 {
		r.centered(r.tiny, fmt.Sprintf(tr("firstrun_step_d_of_d"), n, of), 74, colDim)
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
		r.centered(r.tiny, tr("firstrun_tap_to_go_back"), 412, colDim)
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
		for _, line := range r.wrapLines(r.small, para, 340) { // Japanese has no spaces to break on
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

// spotFirstRunText is a step's title and lines on the Spot: shorter than a Show's, for the circle, and
// in the screen's language.
func spotFirstRunText(v firstRunView, now time.Time) (string, []string) {
	phone := ""
	if v.phone && v.wifi.Address != "" {
		phone = fmt.Sprintf(tr("firstrun_phone_at"), v.wifi.Address, web.Port)
	}
	with := func(lines ...string) []string {
		if phone != "" {
			lines = append(lines, phone)
		}
		return lines
	}
	switch v.step {
	case frHello:
		return tr("firstrun_hello"), []string{
			fmt.Sprintf(tr("firstrun_this_is_s"), v.name), tr("firstrun_spot_steps"), tr("firstrun_spot_tap_to_begin"),
		}
	case frDone:
		return tr("firstrun_title_done"), []string{
			fmt.Sprintf(tr("firstrun_spot_done"), v.name), tr("firstrun_spot_menu"), tr("firstrun_spot_tap_to_finish"),
		}
	case frLanguage:
		return tr("firstrun_title_language"), []string{tr("firstrun_spot_language"), frClockLine(now, v.clock24)}
	case frWifi:
		if v.wifi.Connected {
			return tr("firstrun_title_wifi"), []string{
				fmt.Sprintf(tr("firstrun_spot_on"), v.wifi.SSID), fmt.Sprintf(tr("firstrun_spot_at"), v.wifi.Address),
			}
		}
		return tr("firstrun_title_wifi"), []string{tr("firstrun_spot_no_network"), tr("firstrun_spot_install_again")}
	case frZone:
		at := now.Format("3:04 PM")
		if clock24.Load() {
			at = now.Format("15:04")
		}
		return tr("firstrun_title_zone"), with(fmt.Sprintf(tr("firstrun_spot_zone"), zoneValue()), fmt.Sprintf(tr("firstrun_spot_clock"), at))
	case frName:
		if v.renaming {
			return tr("firstrun_title_name_only"), []string{tr("firstrun_spot_restarting"), tr("firstrun_spot_new_name")}
		}
		return tr("firstrun_title_name"), with(fmt.Sprintf(tr("firstrun_spot_name"), v.name))
	case frHome:
		switch {
		case v.homeJust:
			return "Home Assistant", []string{tr("firstrun_home_added"), tr("firstrun_going_on")}
		case v.home:
			return "Home Assistant", []string{tr("firstrun_home_added")}
		case v.wifi.Address == "":
			return "Home Assistant", []string{tr("firstrun_spot_home_no_network")}
		case v.unkeyed:
			return "Home Assistant", []string{tr("firstrun_spot_home_add"), tr("firstrun_spot_trust")}
		case !v.adopt.IsZero():
			left := max(1, int(v.adopt.Sub(now).Minutes()+0.999))
			return "Home Assistant", []string{tr("firstrun_spot_home_add"), fmt.Sprintf(tr("firstrun_spot_open_d"), left)}
		}
		return "Home Assistant", []string{tr("firstrun_spot_home_choose"), tr("firstrun_spot_home_choose_key")}
	case frUpdates:
		var line string
		switch {
		case !v.checked:
			line = tr("firstrun_updates_looking")
		case v.offered == "":
			line = tr("firstrun_updates_current")
		case v.auto:
			line = fmt.Sprintf(tr("firstrun_spot_tonight"), v.offered)
		default:
			line = fmt.Sprintf(tr("firstrun_spot_ready"), v.offered)
		}
		auto := tr("firstrun_spot_manual")
		if v.auto {
			auto = tr("firstrun_spot_auto")
		}
		return tr("firstrun_title_updates"), []string{line, auto}
	case frSound:
		return tr("firstrun_title_sound"), []string{tr("firstrun_spot_hear")}
	}
	return "", nil
}
