//go:build spot

package display

import (
	"image"
	"image/color"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/bluetooth"
	"github.com/HuskerMinion/techo5/echod/internal/feature/btaudio"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
	"github.com/HuskerMinion/techo5/echod/internal/feature/sendspin"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// The Spot's settings screen, on the round panel, from the parts in sheet_widgets.go: the six
// categories as tiles, then a category as a round card whose rows scroll under a dragged finger, and
// lists of choices as a column in the middle of the circle.

// deviceModel is what the About row calls this device.
const deviceModel = "Echo Spot"

// themeRows are none: the Spot has its own colors and no themes.
func themeRows() []settingRow { return nil }

// deviceCard, devicePicker, deviceChoose and deviceRowTap are the Show's own cards, lists and rows;
// the Spot has none of its own.
func deviceCard(sheetView) (cardView, bool)             { return cardView{}, false }
func devicePicker(string, sheetView) (pickerView, bool) { return pickerView{}, false }
func (d *Display) deviceChoose(string, int)             {}

// deviceRowTap is a tap on one of the Spot's own rows: Forget, which asks twice.
func (d *Display) deviceRowTap(id string, _ part, _ int) bool {
	if id != "btforget" {
		return false
	}
	d.mu.Lock()
	armed := !d.forgetArm.IsZero() && time.Since(d.forgetArm) < restartWindow
	d.forgetArm = time.Time{}
	if !armed {
		d.forgetArm = time.Now()
	}
	d.mu.Unlock()
	if armed {
		go btaudio.Get().Forget()
	}
	return true
}

// swatchStrip is the custom colors row, which the Spot has no use for.
func (r *paint) swatchStrip(settingRow, int, int, int) {}

// openSettings puts the settings screen up on its six categories. Called with d.mu held.
func (d *Display) openSettings() {
	d.closeMenu()
	d.sheetOpen, d.sheetGrid, d.sheetAt = true, true, time.Now()
	d.picker, d.cardScroll, d.pickScroll, d.draft, d.dragging = "", 0, 0, nil, false
	d.followFingers(true)
}

// closeSheet takes the settings screen down, back to the face.
func (d *Display) closeSheet() {
	d.mu.Lock()
	d.sheetOpen, d.picker, d.draft, d.dragging = false, "", nil, false
	d.followFingers(false)
	d.mu.Unlock()
	d.wake()
}

// sheetBack is Back: out of a list, out of the alarm editor, out of a category to the six, and from
// there back to the ring menu.
func (d *Display) sheetBack() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cardScroll, d.dragging = 0, false
	switch {
	case d.picker != "":
		d.picker = ""
	case d.draft != nil:
		d.draft = nil
	case !d.sheetGrid:
		d.sheetGrid = true
	default:
		d.sheetOpen = false
		d.openMenu(modeMain, itemSettings)
	}
}

// showForecast puts the weather face up.
func (d *Display) showForecast() {
	d.mu.Lock()
	d.sheetOpen = false
	d.openMenu(modeWeather, "")
	d.weatherUntil = time.Now().Add(weatherIdle)
	d.mu.Unlock()
	d.wake()
}

// restartNow restarts the device.
func restartNow() { restartDevice() }

// nightRowLabel is what the Display card calls the night setting: the Spot dims at night rather
// than going dark.
const nightRowLabel = "Dim at night"

// hasNightLight is the Show's choice of a night light or a dark screen; the Spot's night only dims.
const hasNightLight = false

// hasCalendarPopups: not on the Spot yet.
const hasCalendarPopups = false

// adaptRows fits the shared rows to the round card, which is narrower than the Show's: shorter
// labels, a status under a row's name rather than beside its button, no second button beside a
// choice (Weather's Show: the forecast is on the dial), and Updates as two rows, its channel and
// its Check now or Install.
func adaptRows(rows []settingRow, sv sheetView) []settingRow {
	out := make([]settingRow, 0, len(rows)+1)
	for _, row := range rows {
		switch {
		case row.id == "mic":
			row.value = ""
		case row.id == "slideshow":
			row.sub = "" // its choice needs the room
		case row.id == "ringvol" && row.value == "Silent":
			row.sub = "No sound at all"
		case row.id == "ringvol":
			row.sub = "Not the music's"
		case row.id == "musicstrip":
			continue // the strip is the Show's; the round face has no clock page under it to share
		case row.id == "wakesens":
			row.label = "Sensitivity"
		case row.id == "sendspin":
			row.label = "Music Assistant"
		case row.id == "screenweb":
			row.label = "Screen on network"
		case row.id == "camweb":
			row.label = "Camera on network"
		case row.id == "e.days":
			row.label = ""
		case row.id == "updates":
			check := settingRow{id: "updatecheck", label: "Check for updates", sub: row.sub, kind: ctlButton, button: row.button}
			row.label, row.sub, row.button = "Update channel", "", ""
			out = append(out, row, check)
			continue
		case row.kind == ctlChoice:
			row.button = ""
		case row.kind == ctlButton && row.value != "":
			row.sub, row.value = row.value, ""
		}
		out = append(out, row)
		if row.id == "bt" {
			// The Spot could always forget its speaker from the screen; asks twice.
			forget := settingRow{id: "btforget", label: "Forget " + row.label, sub: "Asks twice", kind: ctlDanger, button: "Forget"}
			if !sv.st.forgetArm.IsZero() && sv.now.Sub(sv.st.forgetArm) < restartWindow {
				forget.sub, forget.button = "Tap again to forget it", "Confirm"
			}
			out = append(out, forget)
		}
	}
	return out
}

func init() {
	// The Spot's own colors, with its listening blue as the accent.
	walnut, amber, cream, dim, ember = colBackground, colListening, colText, colDim, colTrack
	// Empty is the default night here (defaultNight), so Never needs a value of its own, which
	// inNight reads as no night; the default is offered as a choice of its own.
	nightPresets = []string{"off", defaultNight, "22-6", "23-6", "0-7", "21-7", "23-8"}
	categoryBlurbs[catDisplay] = "Brightness, night, clock and photos"
	categoryBlurbs[catSecurity] = "Remote access and security"
}

// spotFaces are the settings screen's text sizes on the round panel, a size down from the Show's.
var spotFaces = sync.OnceValue(func() *sheetFaces {
	bold, _ := opentype.Parse(gobold.TTF)
	regular, _ := opentype.Parse(goregular.TTF)
	f := func(fn *opentype.Font, size float64) font.Face {
		fc, _ := opentype.NewFace(fn, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		return fc
	}
	return &sheetFaces{
		header: f(bold, 30), label: f(regular, 24), labelBold: f(bold, 24), sub: f(regular, 16),
		value: f(regular, 20), nav: f(regular, 20), navBold: f(bold, 20), button: f(bold, 19),
	}
})

// The round card's frame: rows between spotListTop and spotListBottom, their labels and controls
// between spotCardLeft and spotCardRight (where the circle is wide enough for both), and its
// buttons centered on spotButtonsY.
const (
	spotListTop    = 106
	spotListBottom = 404
	spotCardLeft   = 32
	spotCardRight  = 448
	spotButtonsY   = 433

	// sheetIdle closes the settings screen nobody is touching.
	sheetIdle = time.Minute
)

// sheetFace draws the settings screen: the six categories, a category's card, or a list of choices.
func (r *roundRenderer) sheetFace(s roundScene) {
	r.pending = r.pending[:0]
	cardMax, pickMax := 0, 0
	r.vgradient(r.dst.Rect, shift(walnut, 8), shift(walnut, -2))
	sv := s.sheet
	p, listing := pickerFor(sv.st.picker, sv)
	switch {
	case sv.st.picker != "" && listing:
		pickMax = r.roundList(p, sv.st.pickScroll)
	case s.sheetGrid:
		r.categoryTiles(sv.st.cat)
	default:
		cardMax = r.roundCard(categoryCard(sv))
	}
	r.zmu.Lock()
	r.zones, r.pending = r.pending, r.zones
	r.cardMax, r.pickMax = cardMax, pickMax
	r.zmu.Unlock()
}

// centeredText writes s centered across the panel.
func (r *roundRenderer) centeredText(face font.Face, s string, baseline int, c color.RGBA) {
	r.paint.text(face, s, center-r.paint.width(face, s)/2, baseline, c)
}

// categoryTiles is the settings screen's first page: a tile for each category, the last one opened
// raised in the accent, and Done.
func (r *roundRenderer) categoryTiles(sel category) {
	fc := r.faces()
	r.centeredText(fc.header, "Settings", 70, cream)
	const tw, th, gap = 142, 96, 12
	x0, y0 := center-tw-gap/2, 96
	for c := category(0); c < categories; c++ {
		col, row := int(c)%2, int(c)/2
		b := image.Rect(x0+col*(tw+gap), y0+row*(th+gap), x0+col*(tw+gap)+tw, y0+row*(th+gap)+th)
		fg := lerp(dim, cream, 0.45)
		if c == sel {
			r.roundShadow(b, 20, 14, 5, shadowAlpha())
			r.roundFill(b, 20, shift(amber, 16), shift(amber, -16))
			r.roundHighlight(b, 20)
			fg = onAccent()
		} else {
			r.roundShadow(b, 20, 10, 3, shadowAlpha()*0.6)
			r.roundFill(b, 20, surface(4), surface(2))
			r.roundHighlight(b, 20)
		}
		r.paint.icon(c, b.Min.X+tw/2, b.Min.Y+36, fg)
		name := categoryNames[c]
		r.paint.text(fc.navBold, name, b.Min.X+(tw-r.paint.width(fc.navBold, name))/2, b.Min.Y+78, fg)
		r.addZone(zone{r: b.Inset(-gap / 2), kind: zoneCat, cat: c})
	}
	done := image.Rect(center-62, 424, center+62, 460)
	r.roundFill(done, 18, surface(4), surface(3))
	r.roundStroke(done, 18, 1, ember)
	r.centeredText(fc.button, "Done", 449, cream)
	r.addZone(zone{r: done.Inset(-8), kind: zoneDone})
}

// roundCard is a category's card on the round panel: its title, its rows scrolling in the middle,
// and its buttons at the foot. It returns how far the rows can scroll.
func (r *roundRenderer) roundCard(v cardView) int {
	fc := r.faces()
	r.centeredText(fc.header, r.fit(fc.header, v.title, 340), 66, cream)
	r.centeredText(fc.sub, r.fit(fc.sub, v.blurb, 330), 90, dim)
	r.rule(90, 390, 104, 0.8)
	r.cardButtons(v.actions)
	under := slices.Clone(r.dst.Pix)

	card := image.Rect(spotCardLeft, 0, spotCardRight, side)
	list := image.Rect(spotCardLeft, spotListTop, spotCardRight, spotListBottom)
	if len(v.rows) == 0 && v.note != "" {
		y := list.Min.Y + 40
		for _, line := range r.wrap(fc.value, v.note, 330) {
			r.centeredText(fc.value, line, y, dim)
			y += 30
		}
	}
	return r.rowList(card, list, v.rows, v.scroll, lerp(shift(walnut, 8), shift(walnut, -2), 0.5), under)
}

// cardButtons are the card's buttons, side by side at its foot: Back, and the card's own action (Add
// on the Alarms card); the alarm editor has Cancel and Save instead.
func (r *roundRenderer) cardButtons(actions []headerAction) {
	buttons := []headerAction{{id: "back", label: "‹  Back", style: btnSecondary}}
	for _, a := range actions {
		if a.id == "cancel" {
			buttons = []headerAction{{id: "cancel", label: "Cancel", style: btnSecondary}, {id: "save", label: "Save", style: btnPrimary}}
			break
		}
	}
	if len(buttons) == 1 && len(actions) > 0 {
		a := actions[0]
		if a.id == "add" {
			a.label = "+  Add"
		}
		buttons = append(buttons, a)
	}
	fc := r.faces()
	const gap = 10
	total := -gap
	for _, b := range buttons {
		total += r.paint.width(fc.button, b.label) + 44 + gap
	}
	right := center + total/2
	for i := len(buttons) - 1; i >= 0; i-- {
		b := buttons[i]
		left := r.pillButton(right, spotButtonsY, b.label, b.style)
		r.addZone(zone{r: image.Rect(left-4, spotButtonsY-26, right+4, spotButtonsY+26), kind: zoneAction, id: b.id})
		right = left - gap
	}
}

// roundList is a list of choices on the round panel: one column in the middle of the circle,
// scrolling under a dragged finger, with Back at the foot. It returns how far it can scroll.
func (r *roundRenderer) roundList(p pickerView, scroll int) int {
	fc := r.faces()
	r.centeredText(fc.header, r.fit(fc.header, p.title, 340), 66, cream)
	r.cardButtons(nil)
	under := slices.Clone(r.dst.Pix)

	const optH, left, right = 54, center - 160, center + 160
	list := image.Rect(left-20, 90, right+20, spotListBottom)
	maxScroll := max(len(p.opts)*optH-list.Dy(), 0)
	scroll = min(max(scroll, 0), maxScroll)
	for i, o := range p.opts {
		y0 := list.Min.Y + i*optH - scroll
		if y0+optH <= list.Min.Y || y0 >= list.Max.Y {
			continue
		}
		b := image.Rect(left, y0+4, right, y0+optH-4)
		fg := cream
		if i == p.cur {
			r.roundFill(b, 16, shift(amber, 12), shift(amber, -12))
			r.roundHighlight(b, 16)
			fg = onAccent()
			cx, cy := float64(b.Max.X-24), float64(b.Min.Y+b.Dy()/2)
			r.aaLine(cx-7, cy, cx-2, cy+5, 2.6, fg)
			r.aaLine(cx-2, cy+5, cx+7, cy-5, 2.6, fg)
		} else {
			r.roundFill(b, 16, surface(5), surface(4))
		}
		r.paint.text(fc.value, r.fit(fc.value, o, b.Dx()-60), b.Min.X+20, b.Min.Y+b.Dy()/2+7, fg)
		r.addZone(zone{r: image.Rect(left, y0, right, y0+optH).Intersect(list), kind: zoneOption, opt: i})
	}
	if maxScroll > 0 {
		r.restore(under, image.Rect(list.Min.X, 0, list.Max.X, list.Min.Y))
		r.restore(under, image.Rect(list.Min.X, list.Max.Y, list.Max.X, side))
		r.scrollHints(list, scroll, maxScroll, lerp(shift(walnut, 8), shift(walnut, -2), 0.5))
	}
	return maxScroll
}

// sheetGesture is a finger on the settings screen: a tap on what it drew, and a drag scrolling the
// page or the list under the finger.
func (d *Display) sheetGesture(g touch.Gesture) {
	if d.r == nil {
		return
	}
	d.mu.Lock()
	d.sheetAt = time.Now()
	d.mu.Unlock()
	switch g.Kind {
	case touch.Hold:
		d.mu.Lock()
		d.dragging, d.dragFrom = true, g.Y
		d.dragScroll = d.cardScroll
		if d.picker != "" {
			d.dragScroll = d.pickScroll
		}
		d.mu.Unlock()
	case touch.Drag:
		cardMax, pickMax := d.r.scrollLimits()
		d.mu.Lock()
		if d.dragging {
			to := d.dragScroll + d.dragFrom - g.Y
			if d.picker != "" {
				d.pickScroll = min(max(to, 0), pickMax)
			} else {
				d.cardScroll = min(max(to, 0), cardMax)
			}
		}
		d.mu.Unlock()
	case touch.Release:
		d.mu.Lock()
		d.dragging = false
		d.mu.Unlock()
	case touch.Tap:
		z, ok := d.r.zoneAt(g.X, g.Y)
		if !ok {
			break
		}
		switch z.kind {
		case zoneCat:
			d.mu.Lock()
			d.cat, d.sheetGrid, d.picker, d.draft = z.cat, false, "", nil
			d.cardScroll, d.pickScroll, d.restartArm = 0, 0, time.Time{}
			d.mu.Unlock()
		case zoneDone:
			d.closeSheet()
		case zoneAction:
			d.actionTap(z.id)
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
	d.wake()
}

// sheetView is what the settings screen shows now. Cheap enough per frame: a few reads.
func (d *Display) sheetView(now time.Time) sheetView {
	d.mu.Lock()
	st := settings{cat: d.cat, picker: d.picker, cardScroll: d.cardScroll, pickScroll: d.pickScroll,
		brightness: d.ceiling, auto: d.autoOn, now: now, restartArm: d.restartArm, forgetArm: d.forgetArm, checking: d.checking,
		folder: d.folder, demo: now.Before(d.demoUntil)}
	var draft *alarmDraft
	if d.draft != nil {
		c := *d.draft
		draft = &c
	}
	d.mu.Unlock()
	if st.brightness == 0 {
		st.brightness = config.DefaultScreenBrightness
	}
	st.muted, _ = mute.Get().Muted()
	st.volume = media.Get().Volume()

	c := config.Get()
	st.name = cmpOr(c.Device.Name, layout.DefaultName)
	st.wakeWord = strings.ReplaceAll(c.Wake.Slot(0).ID, "_", " ")
	if m, ok := wake.Find(wake.Lib().Ours(), c.Wake.Slot(0).ID); ok && m.Phrase != "" {
		st.wakeWord = m.Phrase
	}
	st.wakeWord = cmpOr(st.wakeWord, "off")
	st.weather = home.Get().WeatherSource()
	st.version = layout.Version
	st.night = cmpOr(c.Screen.Night, defaultNight)
	st.address = deviceAddress()
	st.wifiName = "Connected"
	if st.address == "" || st.address == "-" {
		st.wifiName = "Not connected"
	}
	st.slot = bootedSlot()
	st.sendspin = sendspin.Get().Enabled()
	st.insecureTLS = c.Diag.InsecureTLS
	st.btProxy = bluetooth.Get().Enabled()
	if st.demo {
		st.name, st.address, st.weather = "Kitchen", "192.168.1.50", "Home"
	}
	sv := sheetView{st: st, bt: btaudio.Get().State(), draft: draft, snooze: c.Alarms.Snooze(), now: now,
		timers: timer.Get().List(now)}
	switch st.cat {
	case catAlarms:
		sv.alarms = alarm.Get().View(now)
	case catSecurity:
		sv.security = security.Get().State()
		if st.demo {
			for i := range sv.security.Keys {
				sv.security.Keys[i] = "laptop"
			}
		}
	}
	return sv
}

// OpenSheet puts the settings screen up, for a look from afar (/screen.png?sheet=): "settings" for
// its six categories, a category by name, or "off". It reports whether the name meant anything.
func (d *Display) OpenSheet(name string) bool {
	if strings.EqualFold(name, "off") {
		d.closeSheet()
		return true
	}
	cat, ok := catByName(name)
	grid := strings.EqualFold(name, "settings")
	if !ok && !grid {
		return false
	}
	d.mu.Lock()
	d.openSettings()
	if !grid {
		d.cat, d.sheetGrid = cat, false
	}
	d.mu.Unlock()
	d.wake()
	return true
}

// Demo puts placeholders in for the owner's details for a while, for screenshots to be published.
func (d *Display) Demo(for_ time.Duration) {
	d.mu.Lock()
	d.demoUntil = time.Now().Add(for_)
	d.mu.Unlock()
	d.wake()
}
