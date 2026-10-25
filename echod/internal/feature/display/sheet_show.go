//go:build !dot && !spot

package display

import (
	"image/color"
	"log/slog"
	"strconv"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// The Show's own parts of the settings screen: themes and their custom colors, the Wi-Fi pages, the
// drawer's station lists, and how it closes, shows the forecast and restarts.

// deviceModel is what the About row calls this device. One build serves the 1st gen Show 5, the 2nd
// gen Show 5 and the Show 8, so it is the board the bootloader named rather than a constant.
var deviceModel = layout.Model

const (
	// atNightSub is what the At night row says under its name.
	atNightSub = "Dark, a faint glow, or a clock alone until touched"

	// hasCalendarPopups is whether events pop up on this screen (calendar_popup.go).
	hasCalendarPopups = true

	// hasDimmest is the Dimmest row under Auto-brightness (display.go).
	hasDimmest = true
)

// atNightChoices are what the night does to a Show's screen: puts it out, leaves a night light, or
// leaves only the clock (render_night.go).
var atNightChoices = []atNightChoice{{"Screen off", false, false, false}, {"Night light", true, false, false}, {"Night clock", true, true, false}}

// settingsScreen draws the settings screen for the scene's category.
func (r *renderer) settingsScreen(s scene) {
	sv := s.view()
	var pick *pickerView
	if sv.st.picker != "" {
		if p, ok := pickerFor(sv.st.picker, sv); ok {
			pick = &p
		}
	}
	r.settingsPage(sv.st.cat, categoryCard(sv), pick, sv.st.pickScroll)
}

// view is what the scene gives the settings cards.
func (s scene) view() sheetView {
	return sheetView{st: s.sheet, security: s.security, bt: s.bt, alarms: s.alarms, draft: s.draft,
		snooze: s.snooze, now: s.now, radio: s.radio, timers: s.timers}
}

// themeRows are the Display card's rows for the theme. Under a skin they still change the theme, which
// comes back when the skin is taken off, and say so.
func themeRows() []settingRow {
	theme, colors := "", "Make the theme your own"
	if skin() != skinNone {
		theme, colors = skinKeeps, skinKeeps
	}
	return []settingRow{
		{id: "theme", label: themeRowLabel(), sub: theme, kind: ctlChoice, value: current().name},
		{id: "colors", label: "Custom colors", sub: colors, kind: ctlButton, button: "Edit"},
	}
}

// themeRowLabel names the theme the row changes: the one worn. In Dark mode that is simply Theme, as it
// always was; in Light or Auto it is the dark theme or the light one, whichever the screen wears now.
func themeRowLabel() string {
	switch {
	case themeModeIndex() == 0:
		return "Theme"
	case lightShown(config.Get().Screen):
		return "Light theme"
	}
	return "Dark theme"
}

// deviceCard is a card of the Show's own in place of the category's: the custom colors editor.
func deviceCard(sv sheetView) (cardView, bool) {
	if sv.st.cat == catDisplay && sv.st.colors {
		return colorsCard(), true
	}
	return cardView{}, false
}

// colorsCard is the custom colors editor: a strip of colors for each role of the theme. A tap
// on one makes the theme Custom with that color.
func colorsCard() cardView {
	v := cardView{
		title: "Custom colors", blurb: "Tap a color for each part; the theme becomes Custom",
		actions: []headerAction{{id: "colorsdone", label: "Done", style: btnPrimary}},
	}
	for role := range roles {
		v.rows = append(v.rows, settingRow{id: "role:" + strconv.Itoa(role), label: roleNames[role], kind: ctlSwatches, role: role})
	}
	return v
}

// adaptRows is the Show's rows as they are: its card is wide enough for all of them.
func adaptRows(rows []settingRow, _ sheetView) []settingRow { return rows }

// devicePicker is a list of the Show's own: themes, and the drawer's station lists.
func devicePicker(id string, sv sheetView) (pickerView, bool) {
	switch id {
	case "theme":
		p := pickerView{title: themeRowLabel(), cur: -1}
		name := shownName()
		for i, t := range themes {
			p.opts = append(p.opts, t.name)
			p.swatches = append(p.swatches, [2]color.RGBA{t.colors[roleGround], t.colors[roleAccent]})
			if t.name == current().name && name != customName {
				p.cur = i
			}
		}
		// Custom, once there is one to go back to.
		if c, ok := savedCustom(lightShown(config.Get().Screen)); ok {
			if name == customName {
				p.cur = len(p.opts)
			}
			p.opts = append(p.opts, customName)
			p.swatches = append(p.swatches, [2]color.RGBA{c.colors[roleGround], c.colors[roleAccent]})
		}
		return p, true
	case "radiosource":
		p := pickerView{title: "Stations", cur: -1}
		for i, src := range home.RadioSources() {
			p.opts = append(p.opts, home.SourceLabel(src))
			if src == sv.radio.Source {
				p.cur = i
			}
		}
		return p, len(p.opts) > 0
	}
	return lookPicker(id)
}

// deviceChoose puts a choice from one of the Show's own lists in force.
func (d *Display) deviceChoose(id string, i int) {
	if d.lookChoose(id, i) || id != "theme" {
		return
	}
	switch {
	case i < len(themes):
		d.SetTheme(themes[i].name)
	case i == len(themes):
		if err := config.Set().Screen().ThemeFor(lightShown(config.Get().Screen), customName); err != nil {
			slog.Warn("saving the theme failed", "err", err)
		}
	}
}

// deviceRowTap is a tap on one of the Show's own rows; it reports whether it was one.
func (d *Display) deviceRowTap(id string, p part, opt int) bool {
	if n, ok := strings.CutPrefix(id, "role:"); ok {
		if role, err := strconv.Atoi(n); err == nil && role >= 0 && role < roles && p == partDay && opt < swatchCount {
			setRole(role, swatch(role, opt))
		}
		return true
	}
	switch id {
	case "theme":
		d.openPicker(id)
	case "colors":
		d.mu.Lock()
		d.colors, d.cardScroll = true, 0
		d.mu.Unlock()
	case "wifi":
		if wifi.Available() {
			d.showSheet(false)
			d.openWifi()
		}
	case "address":
		// The page before the sheet goes, so the settings lock never sees neither open.
		if wifi.Available() {
			d.openAddress()
			d.showSheet(false)
		}
	case "camtest":
		// The same way round as the address page.
		if camTestAvailable() {
			d.openCameraTest()
			d.showSheet(false)
		}
	default:
		return d.lookRowTap(id)
	}
	return true
}

// closeSheet takes the settings screen down.
func (d *Display) closeSheet() { d.showSheet(false) }

// sheetUp is whether the settings sheet is showing.
func (d *Display) sheetUp() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sheet
}

// sheetBack is a Back the Show's screen has no button for.
func (d *Display) sheetBack() {}

// showForecast puts the forecast up.
func (d *Display) showForecast() { d.ShowWeather(false) }

// restartNow restarts the device.
func restartNow() { restart() }
