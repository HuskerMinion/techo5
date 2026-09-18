//go:build !dot && !spot

package display

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/bluetooth"
	"github.com/HuskerMinion/techo5/echod/internal/feature/btaudio"
	"github.com/HuskerMinion/techo5/echod/internal/feature/firmware"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
	"github.com/HuskerMinion/techo5/echod/internal/feature/sendspin"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// The settings screen's contents: each category's rows from the scene, the lists its choices open,
// and what a tap on any of it does. The renderer records where each control landed as it draws, so
// a tap is matched to the frame on the screen rather than to geometry worked out a second time.

// settingsScreen draws the settings screen for the scene's category.
func (r *renderer) settingsScreen(s scene) {
	st := s.sheet
	var pick *pickerView
	if st.picker != "" {
		if p, ok := pickerFor(st.picker, s); ok {
			pick = &p
		}
	}
	r.settingsPage(st.cat, categoryCard(s), pick, st.pickScroll)
}

// categoryCard is the card for the open category.
func categoryCard(s scene) cardView {
	st := s.sheet
	var v cardView
	switch {
	case st.cat == catAlarms:
		v = alarmsCard(s)
	case st.cat == catDisplay && st.colours:
		v = coloursCard()
	default:
		rows, note := categoryRows(s)
		v = cardView{title: categoryTitles[st.cat], blurb: categoryBlurbs[st.cat], rows: rows, note: note}
	}
	v.scroll = st.cardScroll
	return v
}

// categoryRows is the open category's rows, and a note to show when it has none.
func categoryRows(s scene) (rows []settingRow, note string) {
	st := s.sheet
	switch st.cat {
	case catDisplay:
		return []settingRow{
			{id: "brightness", label: "Brightness", kind: ctlStepper, value: fmt.Sprintf("%d%%", st.brightness)},
			{id: "auto", label: "Auto-brightness", sub: "Follows the room's light", kind: ctlToggle, on: st.auto},
			{id: "night", label: "Screen off at night", kind: ctlChoice, value: nightText(st.night)},
			{id: "theme", label: "Theme", kind: ctlChoice, value: current().name},
			{id: "colours", label: "Custom colours", sub: "Make the theme your own", kind: ctlButton, button: "Edit"},
			{id: "clock", label: "Clock format", kind: ctlChoice, value: clockOptions[clockIndex()]},
			{id: "slideshow", label: "Slideshow", sub: "Photos from Home Assistant", kind: ctlChoice, value: slideshowOptions[slideshowIndex()]},
		}, ""
	case catSound:
		mic := "Listening"
		if st.muted {
			mic = "Muted"
		}
		return []settingRow{
			{id: "volume", label: "Volume", kind: ctlStepper, value: fmt.Sprintf("%d of %d", st.volume, sheetVolumeSteps)},
			{id: "mic", label: "Microphone", sub: "The mute button does this too", kind: ctlToggle, on: !st.muted, value: mic},
			{id: "wakeword", label: "Wake word", kind: ctlChoice, value: st.wakeWord},
			{id: "wakesens", label: "Wake word sensitivity", sub: "Higher wakes by mistake less often", kind: ctlStepper,
				value: fmt.Sprintf("%.2f", config.Get().Wake.Slot(0).Threshold)},
			{id: "waketone", label: "Wake sound", kind: ctlChoice, value: config.Get().Wake.Slot(0).Tone.Label()},
			{id: "sendspin", label: "Music Assistant player", sub: "Play music in sync with other rooms", kind: ctlToggle, on: st.sendspin},
		}, ""
	case catConnections:
		return connectionRows(s), ""
	case catSecurity:
		return securityRows(s), ""
	case catGeneral:
		return generalRows(s), ""
	}
	return nil, ""
}

// securityRows are the Privacy & Security card's: how the device can be reached, and how it reaches
// Home Assistant.
func securityRows(s scene) []settingRow {
	sec, st := s.security, s.sheet
	var rows []settingRow
	if !sec.SSHAvailable {
		rows = append(rows, settingRow{label: "SSH", kind: ctlValue, value: "Not managed here"})
	} else {
		sub := "Closed"
		switch {
		case sec.SSH && len(sec.Keys) == 0:
			sub = "On, but no key yet, so nothing listens"
		case sec.SSH && sec.SSHRunning:
			sub = "Port 22, keys only"
		case sec.SSH:
			sub = "Starting…"
		case sec.SSHRunning:
			sub = "Stopping…"
		}
		keys := "None"
		if len(sec.Keys) > 0 {
			keys = strings.Join(sec.Keys, ", ")
		}
		rows = append(rows,
			settingRow{id: "ssh", label: "SSH", sub: sub, kind: ctlToggle, on: sec.SSH},
			settingRow{label: "SSH keys", sub: "Sent from Home Assistant", kind: ctlValue, value: keys})
	}
	link := settingRow{label: "Home Assistant link", sub: "Encrypted with this device's key", kind: ctlValue, value: "Encrypted"}
	if !sec.Encrypted {
		link.sub, link.value = "Add the device in Home Assistant to encrypt it", "Not encrypted"
	}
	certs := settingRow{label: "Certificate checks", sub: "For updates and downloads", kind: ctlValue, value: "On"}
	if st.insecureTLS {
		certs.sub, certs.value = "Turned off in Home Assistant", "Off"
	}
	return append(rows,
		settingRow{id: "camweb", label: "Camera on the network", sub: "No login", kind: ctlToggle, on: sec.Camera},
		settingRow{id: "screenweb", label: "Screen on the network", sub: "No login", kind: ctlToggle, on: sec.Screen},
		link, certs)
}

// generalRows are the General card's: the device's name, weather, updates, what it is, and Restart.
func generalRows(s scene) []settingRow {
	st := s.sheet
	fw := firmware.Get()
	updates := settingRow{id: "updates", label: "Updates", sub: "This is " + st.version, kind: ctlChoice,
		value: capitalize(fw.Channel().Label()), button: "Check now"}
	switch {
	case st.checking:
		updates.sub = "Checking…"
	case fw.Offered() != "":
		updates.sub, updates.button = fw.Offered()+" is ready · this is "+st.version, "Install"
	}
	restart := settingRow{id: "restart", label: "Restart", sub: "Back in about a minute", kind: ctlDanger, button: "Restart"}
	if !st.restartArm.IsZero() && st.now.Sub(st.restartArm) < restartWindow {
		restart.sub, restart.button = "Tap again to restart now", "Confirm"
	}
	return []settingRow{
		{label: "Name", sub: "Set in Home Assistant", kind: ctlValue, value: st.name},
		{id: "weather", label: "Weather", sub: "Shown with the clock", kind: ctlChoice, value: st.weather, button: "Show"},
		updates,
		{label: "About", kind: ctlValue, value: "Echo Show 5 · slot " + st.slot},
		restart,
	}
}

// connectionRows are the Connections card's: Wi-Fi, Bluetooth audio, and the Bluetooth proxy.
func connectionRows(s scene) []settingRow {
	st, bt := s.sheet, s.bt
	wifiRow := settingRow{label: "Wi-Fi", sub: st.address, kind: ctlValue, value: st.wifiName}
	if st.wifiOK {
		wifiRow.id, wifiRow.kind, wifiRow.button = "wifi", ctlButton, "Change"
	}
	if st.address == "-" {
		wifiRow.sub = "No address yet"
	}
	rows := []settingRow{wifiRow}
	switch {
	case !bt.Available:
		rows = append(rows, settingRow{label: "Bluetooth audio", sub: "Not available on this build", kind: ctlValue})
	case bt.Connected != "":
		rows = append(rows, settingRow{id: "bt", label: bt.Connected, sub: cmpOr(bt.Status, "Bluetooth audio · Connected"), kind: ctlButton, button: "Disconnect"})
	case bt.Remembered != "":
		// What it is doing, while it does something; otherwise plainly not connected.
		row := settingRow{id: "bt", label: bt.Remembered, sub: "Bluetooth audio", kind: ctlButton, value: "Not connected", button: "Connect"}
		if bt.Status != "" {
			row.sub, row.value = bt.Status, ""
		}
		rows = append(rows, row)
	default:
		rows = append(rows, settingRow{label: "Bluetooth audio", sub: cmpOr(bt.Status, "Earbuds or a speaker"), kind: ctlValue, value: "None yet"})
	}
	if bt.Available {
		rows = append(rows, settingRow{id: "pair", label: "Pair a new device", sub: "Put it in pairing mode first", kind: ctlButton, button: "Pair"})
	}
	return append(rows, settingRow{id: "btproxy", label: "Bluetooth proxy", sub: "Lets Home Assistant hear nearby devices",
		kind: ctlToggle, on: st.btProxy})
}

// coloursCard is the custom colours editor: a strip of colours for each role of the theme. A tap
// on one makes the theme Custom with that colour.
func coloursCard() cardView {
	v := cardView{
		title: "Custom colours", blurb: "Tap a colour for each part; the theme becomes Custom",
		actions: []headerAction{{id: "coloursdone", label: "Done", style: btnPrimary}},
	}
	for role := range roles {
		v.rows = append(v.rows, settingRow{id: "role:" + strconv.Itoa(role), label: roleNames[role], kind: ctlSwatches, role: role})
	}
	return v
}

// catByName is a category from its rail name or card title in any case, for /screen.png?sheet= and
// Home Assistant; the old tab names still work.
func catByName(name string) (category, bool) {
	for c := category(0); c < categories; c++ {
		if strings.EqualFold(categoryNames[c], name) || strings.EqualFold(categoryTitles[c], name) {
			return c, true
		}
	}
	switch strings.ToLower(name) {
	case "device", "theme":
		return catDisplay, true
	case "bluetooth", "wifi", "wi-fi":
		return catConnections, true
	case "security":
		return catSecurity, true
	}
	return 0, false
}

var (
	clockOptions     = []string{clock12Label, clock24Label}
	slideshowOptions = []string{"Off", "Background", "Screensaver"}
	slideshowModes   = []string{"", config.SlideshowBackground, config.SlideshowScreensaver}
)

func clockIndex() int {
	if clock24.Load() {
		return 1
	}
	return 0
}

func slideshowIndex() int {
	mode := home.Get().SlideshowMode()
	for i, m := range slideshowModes {
		if m == mode {
			return i
		}
	}
	return 0
}

// nightText is a night window as the clock would say it: "10 PM – 6 AM", or "Never".
func nightText(v string) string {
	from, to, ok := nightHours(v)
	if !ok {
		return "Never"
	}
	return hourText(from) + " – " + hourText(to)
}

func hourText(h int) string {
	t := time.Date(2000, 1, 1, h, 0, 0, 0, time.UTC)
	if clock24.Load() {
		return t.Format("15:04")
	}
	return t.Format("3 PM")
}

// pickerFor is the list of choices a row opens.
func pickerFor(id string, s scene) (pickerView, bool) {
	switch id {
	case "alarmsound":
		p := pickerView{title: "Alarm sound", opts: speaker.AlarmSounds(), cur: -1}
		for i, o := range p.opts {
			if o == alarm.Get().Sound() {
				p.cur = i
			}
		}
		return p, true
	case "e.repeat":
		p := pickerView{title: "Repeat", opts: repeatNames, cur: -1}
		if s.draft != nil {
			for i, days := range repeats {
				if days == s.draft.alarm.Days {
					p.cur = i
				}
			}
		}
		return p, true
	case "night":
		p := pickerView{title: "Screen off at night", cur: -1}
		cur := config.Get().Screen.Night
		for i, n := range nightPresets {
			p.opts = append(p.opts, nightText(n))
			if n == cur {
				p.cur = i
			}
		}
		return p, true
	case "theme":
		p := pickerView{title: "Theme", cur: -1}
		name := config.Get().Screen.Theme
		for i, t := range themes {
			p.opts = append(p.opts, t.name)
			p.swatches = append(p.swatches, [2]color.RGBA{t.colors[roleGround], t.colors[roleAccent]})
			if t.name == current().name && name != customName {
				p.cur = i
			}
		}
		// Custom, once there is one to go back to.
		if c, ok := savedCustom(); ok {
			if name == customName {
				p.cur = len(p.opts)
			}
			p.opts = append(p.opts, customName)
			p.swatches = append(p.swatches, [2]color.RGBA{c.colors[roleGround], c.colors[roleAccent]})
		}
		return p, true
	case "wakeword":
		p := pickerView{title: "Wake word", cur: -1}
		cur := config.Get().Wake.Slot(0).ID
		for i, m := range wake.Lib().Ours() {
			p.opts = append(p.opts, cmpOr(m.Phrase, strings.ReplaceAll(m.ID, "_", " ")))
			if m.ID == cur {
				p.cur = i
			}
		}
		return p, len(p.opts) > 0
	case "waketone":
		p := pickerView{title: "Wake sound", opts: config.Labels(speaker.WakeTones()), cur: -1}
		cur := config.Get().Wake.Slot(0).Tone.Label()
		for i, o := range p.opts {
			if o == cur {
				p.cur = i
			}
		}
		return p, true
	case "weather":
		_, names, cur := home.Get().WeatherChoices()
		if s.sheet.demo {
			// Weather entities are often named for the street they are on.
			n := 0
			for i := 2; i < len(names); i++ {
				if i == cur {
					names[i] = "Home"
					continue
				}
				n++
				names[i] = fmt.Sprintf("Weather %d", n)
			}
		}
		return pickerView{title: "Weather", opts: names, cur: cur}, len(names) > 0
	case "updates":
		p := pickerView{title: "Updates", cur: -1}
		for i, c := range update.Channels() {
			p.opts = append(p.opts, capitalize(c.Label()))
			if c == firmware.Get().Channel() {
				p.cur = i
			}
		}
		return p, true
	case "radiosource":
		p := pickerView{title: "Stations", cur: -1}
		for i, src := range home.RadioSources() {
			p.opts = append(p.opts, home.SourceLabel(src))
			if src == s.radio.Source {
				p.cur = i
			}
		}
		return p, len(p.opts) > 0
	case "clock":
		return pickerView{title: "Clock format", opts: clockOptions, cur: clockIndex()}, true
	case "slideshow":
		return pickerView{title: "Slideshow", opts: slideshowOptions, cur: slideshowIndex()}, true
	}
	return pickerView{}, false
}

// choose puts the i'th choice of a row's list in force.
func (d *Display) choose(id string, i int) {
	switch id {
	case "night":
		if i < len(nightPresets) {
			if err := config.Set().Screen().Night(nightPresets[i]); err != nil {
				slog.Warn("saving the night setting failed", "err", err)
			}
		}
	case "theme":
		switch {
		case i < len(themes):
			d.SetTheme(themes[i].name)
		case i == len(themes):
			if err := config.Set().Screen().Theme(customName); err != nil {
				slog.Warn("saving the theme failed", "err", err)
			}
		}
	case "clock":
		on := i == 1
		if err := config.Set().Screen().Clock24(on); err != nil {
			slog.Warn("saving the clock format failed", "err", err)
			return
		}
		setClock24(d.clock, on)
	case "wakeword":
		if models := wake.Lib().Ours(); i < len(models) {
			id := models[i].ID
			safe.Go("wake word from the screen", func() { voice.Get().ChooseWakeWord(id) })
		}
	case "waketone":
		if tones := config.Labels(speaker.WakeTones()); i < len(tones) {
			wakeword.Get().SetTone(0, tones[i])
		}
	case "weather":
		if entities, _, _ := home.Get().WeatherChoices(); i < len(entities) {
			e := entities[i]
			safe.Go("weather source from the screen", func() { home.Get().ChooseWeather(e) })
		}
	case "updates":
		if chans := update.Channels(); i < len(chans) {
			firmware.Get().SetChannel(chans[i].Label())
			d.checkUpdates()
		}
	case "alarmsound":
		if names := speaker.AlarmSounds(); i < len(names) {
			alarm.Get().SetSound(names[i], true)
		}
	case "e.repeat":
		if i < len(repeats) {
			d.editDraft(func(dr *alarmDraft) { dr.alarm.Days = repeats[i] })
		}
	case "slideshow":
		if i < len(slideshowModes) {
			home.Get().ChooseSlideshowMode(slideshowModes[i])
		}
	}
}

// nextTap is a finger on the settings screen.
func (d *Display) nextTap(x, y int) {
	z, ok := d.r.zoneAt(x, y)
	if !ok {
		return
	}
	switch z.kind {
	case zoneDone:
		d.showSheet(false)
	case zoneCat:
		d.mu.Lock()
		d.cat, d.picker, d.restartArm, d.draft, d.colours = z.cat, "", time.Time{}, nil, false
		d.cardScroll, d.pickScroll = 0, 0
		d.mu.Unlock()
	case zoneAction:
		d.actionTap(z.id)
	case zoneDismiss:
		d.mu.Lock()
		d.picker = ""
		d.mu.Unlock()
	case zoneOption:
		d.mu.Lock()
		id := d.picker
		d.picker = ""
		d.mu.Unlock()
		d.choose(id, z.opt)
	case zoneRow:
		d.rowTap(z.id, z.part, z.opt)
	}
}

// rowTap is a tap on a row's control.
func (d *Display) rowTap(id string, p part, opt int) {
	if d.alarmRowTap(id, p, opt) {
		return
	}
	if n, ok := strings.CutPrefix(id, "role:"); ok {
		if role, err := strconv.Atoi(n); err == nil && role >= 0 && role < roles && p == partDay && opt < swatchCount {
			setRole(role, swatch(role, opt))
		}
		return
	}
	switch id {
	case "brightness":
		switch p {
		case partMinus:
			d.stepBrightness(-25)
		case partPlus:
			d.stepBrightness(+25)
		}
	case "auto":
		d.mu.Lock()
		on := d.autoOn
		d.mu.Unlock()
		d.setAuto(!on, true)
	case "volume":
		switch p {
		case partMinus:
			media.Get().Adjust(-1)
		case partPlus:
			media.Get().Adjust(+1)
		}
	case "mic":
		mute.Get().Toggle()
	case "wakesens":
		v := config.Get().Wake.Slot(0).Threshold
		switch p {
		case partMinus:
			v -= 0.02
		case partPlus:
			v += 0.02
		default:
			return
		}
		wakeword.Get().SetThreshold(0, math.Round(min(max(v, 0.5), 0.99)*100)/100)
	case "sendspin":
		sp := sendspin.Get()
		sp.SetEnabled(!sp.Enabled())
	case "ssh":
		security.Get().SetSSH(!config.Get().Security.SSH)
	case "camweb":
		security.Get().SetCamera(!config.Get().Security.Camera)
	case "screenweb":
		security.Get().SetScreen(!config.Get().Security.Screen)
	case "weather":
		if p == partExtra {
			d.ShowWeather(false)
			return
		}
		d.openPicker(id)
	case "updates":
		switch {
		case p != partExtra:
			d.openPicker(id)
		case firmware.Get().Offered() != "":
			safe.Go("update install from the screen", func() { firmware.Get().Install(context.Background()) })
		default:
			d.checkUpdates()
		}
	case "restart":
		d.mu.Lock()
		armed := !d.restartArm.IsZero() && time.Since(d.restartArm) < restartWindow
		if !armed {
			d.restartArm = time.Now()
		}
		d.mu.Unlock()
		if armed {
			slog.Warn("restart asked for from the screen")
			restart()
		}
	case "wifi":
		if wifi.Available() {
			d.showSheet(false)
			d.openWifi()
		}
	case "bt":
		bt := btaudio.Get()
		switch st := bt.State(); {
		case st.Connected != "":
			bt.Disconnect()
		case st.Remembered != "":
			bt.Reconnect()
		}
	case "pair":
		d.showSheet(false)
		btaudio.Get().SetPairing(true)
	case "btproxy":
		p := bluetooth.Get()
		safe.Go("bluetooth proxy from the screen", func() { p.SetEnabled(!p.Enabled()) })
	case "colours":
		d.mu.Lock()
		d.colours, d.cardScroll = true, 0
		d.mu.Unlock()
	case "night", "theme", "clock", "slideshow", "wakeword", "waketone":
		d.openPicker(id)
	}
}

// openPicker opens a row's list of choices, from its start.
func (d *Display) openPicker(id string) {
	d.mu.Lock()
	d.picker, d.pickScroll = id, 0
	d.mu.Unlock()
}

// OpenList opens the settings screen's list of choices for a row, by its id, for a look from afar
// (/screen.png?list=); it reports whether the row has one.
func (d *Display) OpenList(id string) bool {
	if _, ok := pickerFor(id, scene{}); !ok {
		return false
	}
	d.openPicker(id)
	d.wake()
	return true
}

// sheetSwipe is a vertical swipe on the settings screen, a notch at a time: it scrolls an open list,
// or else the card, following the finger. The swipe that opened the screen is still reporting
// notches until its finger lifts; those are known by where they started and do nothing.
func (d *Display) sheetSwipe(g touch.Gesture) {
	if d.r == nil {
		return
	}
	by := notchPx
	if g.Kind == touch.SwipeDown {
		by = -notchPx
	}
	cardMax, pickMax := d.r.scrollLimits()
	d.mu.Lock()
	defer d.mu.Unlock()
	if image.Pt(g.X, g.Y) == d.openedBy {
		return
	}
	if d.picker != "" {
		d.pickScroll = min(max(d.pickScroll+by, 0), pickMax)
		return
	}
	d.cardScroll = min(max(d.cardScroll+by, 0), cardMax)
}

// notchPx is how far a list scrolls for each notch a swipe reports: the touch screen's notch, so
// the list keeps up with the finger.
const notchPx = 40

// checkUpdates looks for an update, showing Checking… on the Updates row until the answer is in.
func (d *Display) checkUpdates() {
	d.mu.Lock()
	if d.checking {
		d.mu.Unlock()
		return
	}
	d.checking = true
	d.mu.Unlock()
	safe.Go("update check from the screen", func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		firmware.Get().Check(ctx)
		d.mu.Lock()
		d.checking = false
		d.mu.Unlock()
		d.wake()
	})
}

// stepBrightness moves the brightness ceiling by pct, between a quarter and full.
func (d *Display) stepBrightness(by int) {
	pct := min(max(d.ceilingOrDefault()+by, 25), 100)
	d.apply(true, pct, true)
}
