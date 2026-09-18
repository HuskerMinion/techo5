//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"math"
	"slices"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
)

// The settings screen regrouped by category: a rail of categories on the left with the chosen one
// raised out of it, and that category's settings on a card floating over the ground. Every shape is
// an anti-aliased rounded rectangle, circle or stroke with soft shadows, drawn from the theme's five
// colours, so it follows whichever theme is chosen.

type category int

const (
	catDisplay category = iota
	catSound
	catAlarms
	catConnections
	catSecurity
	catGeneral
	categories
)

// categoryNames are the rail's short names; categoryTitles head the card.
var categoryNames = [categories]string{"Display", "Sound", "Alarms", "Connections", "Privacy", "General"}

var categoryTitles = [categories]string{"Display", "Sound & Voice", "Alarms & Timers", "Connections", "Privacy & Security", "General"}

var categoryBlurbs = [categories]string{
	"Brightness, night hours, theme and clock",
	"Volume, microphone and wake word",
	"Alarms on this device and how they ring",
	"Wi-Fi, Bluetooth and the Bluetooth proxy",
	"Remote access and how this device is reached",
	"Name, weather, updates and restart",
}

// controlKind is what sits at the right of a settings row.
type controlKind int

const (
	ctlValue   controlKind = iota // a value to read, nothing to press
	ctlToggle                     // a switch
	ctlStepper                    // − value +
	ctlChoice                     // a value that opens a list
	ctlButton                     // an action
	ctlDanger                     // an action to think twice about
	ctlDays                       // the seven days of the week, each on or off
	ctlSwatches                   // a strip of colours for one role of the theme
)

type settingRow struct {
	id         string // what a tap on it means to the display; empty for rows only to read
	label, sub string
	bold       bool
	kind       controlKind
	value      string // beside the control, or alone for ctlValue
	on         bool   // a switch's state
	button     string // an action's label
	days       uint8  // ctlDays: bit 0 Sunday to bit 6 Saturday
	role       int    // ctlSwatches: which role of the theme
	rowTap     bool   // a tap on the row beside its control means something of its own (partRow)
}

// part is which piece of a row a tap landed on.
type part int

const (
	partMain  part = iota // the switch, the choice, the button, or the row around a switch or choice
	partMinus             // a stepper's −
	partPlus              // a stepper's +
	partExtra             // the second button a choice can carry (Updates' Check now)
	partRow               // the row itself, away from its control, on a row with rowTap
	partDay               // a day of ctlDays, or a colour of ctlSwatches; the zone's opt is which
)

type zoneKind int

const (
	zoneRow     zoneKind = iota // a row's control
	zoneCat                     // a category on the rail
	zoneDone                    // Done
	zoneAction                  // the card header's button
	zoneOption                  // a choice in an open list
	zoneDismiss                 // anywhere else while a list is open
	zoneTab                     // one side of a segmented switch; opt is which
)

// zone is a place on the screen a tap means something, recorded as it is drawn so taps always
// match what is showing.
type zone struct {
	r    image.Rectangle
	kind zoneKind
	cat  category
	id   string
	part part
	opt  int
}

// pickerView is a list of choices open over the card: one is picked, or a tap elsewhere puts it away.
type pickerView struct {
	title    string
	opts     []string
	cur      int            // the choice in force, or -1
	swatches [][2]color.RGBA // optional: a ground and accent dot beside each choice (themes)
}

// zoneAt is what a tap at (x, y) landed on in the frame last drawn: the topmost zone holding it, or
// failing that the nearest within a finger's slop of it.
func (r *renderer) zoneAt(x, y int) (zone, bool) {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	p := image.Pt(x, y)
	for i := len(r.zones) - 1; i >= 0; i-- {
		if p.In(r.zones[i].r) {
			if r.zones[i].kind == zoneDismiss {
				break // under an open list: only its choices, then the slop pass
			}
			return r.zones[i], true
		}
	}
	const slop = 12
	for i := len(r.zones) - 1; i >= 0; i-- {
		z := r.zones[i]
		if z.kind != zoneDismiss && p.In(z.r.Inset(-slop)) {
			return z, true
		}
		if z.kind == zoneDismiss && p.In(z.r) {
			return z, true
		}
	}
	return zone{}, false
}

func (r *renderer) addZone(z zone) { r.pending = append(r.pending, z) }

const (
	railW   = 244
	navTop  = 20
	navH    = 54
	navGap  = 8
	cardIn  = 14 // the card's margin from the screen's edges
	cardRad = 22
	headerH = 82
	rowH    = 60
	rowIn   = 30 // text inset inside the card
)

type sheetFaces struct{ header, label, labelBold, sub, value, nav, navBold, button font.Face }

var (
	facesOnce sync.Once
	sheetFace       sheetFaces
)

// faces are the settings screen's own sizes of the Go fonts the rest of the screen uses.
func faces() sheetFaces {
	facesOnce.Do(func() {
		bold, _ := opentype.Parse(gobold.TTF)
		regular, _ := opentype.Parse(goregular.TTF)
		f := func(fn *opentype.Font, size float64) font.Face {
			fc, _ := opentype.NewFace(fn, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
			return fc
		}
		sheetFace = sheetFaces{
			header: f(bold, 36), label: f(regular, 29), labelBold: f(bold, 29), sub: f(regular, 21),
			value: f(regular, 25), nav: f(regular, 24), navBold: f(bold, 24), button: f(bold, 23),
		}
	})
	return sheetFace
}

// headerAction is a button in the card's header, drawn from the right in order.
type headerAction struct {
	id    string
	label string
	style buttonStyle
}

// cardView is what the card shows: its heading, rows (scrolled up by scroll pixels when they do not
// all fit), header buttons, and a note for a card with no rows.
type cardView struct {
	title, blurb string
	rows         []settingRow
	actions      []headerAction
	note         string
	scroll       int
}

// scrollLimits are how far the card and an open list could scroll in the frame last drawn, so a
// swipe can be held to what there is.
func (r *renderer) scrollLimits() (card, pick int) {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	return r.cardMax, r.pickMax
}

// settingsPage draws the whole settings screen: sel is the category on the rail, v the card, and pick
// a list of choices open over it (scrolled by pickScroll pixels when long), or nil.
func (r *renderer) settingsPage(sel category, v cardView, pick *pickerView, pickScroll int) {
	fc := faces()
	r.pending = r.pending[:0]
	r.settingsBase(sel, v.title, v.blurb)
	for c := category(0); c < categories; c++ {
		top := navTop + int(c)*(navH+navGap)
		r.addZone(zone{r: image.Rect(0, top-navGap/2, railW, top+navH+navGap/2), kind: zoneCat, cat: c})
	}
	r.addZone(zone{r: image.Rect(0, r.h-74, railW, r.h), kind: zoneDone})

	card := nextCard(r.w, r.h)
	list := image.Rect(card.Min.X, card.Min.Y+headerH, card.Max.X, card.Max.Y-8)
	if len(v.rows) == 0 && v.note != "" {
		y := list.Min.Y + 50
		for _, line := range r.wrap(fc.value, v.note, card.Dx()-2*rowIn) {
			r.text(fc.value, line, card.Min.X+rowIn, y, dim)
			y += 36
		}
	}

	maxScroll := r.rowList(card, list, v.rows, v.scroll, surface(1), r.base.Pix)
	x := card.Max.X - 26
	for _, a := range v.actions {
		left := r.pillButton(x, card.Min.Y+headerH/2-2, a.label, a.style)
		r.addZone(zone{r: image.Rect(left-4, card.Min.Y+8, x+4, card.Min.Y+headerH-8), kind: zoneAction, id: a.id})
		x = left - 12
	}

	pickMax := 0
	if pick != nil {
		pickMax = r.picker(*pick, pickScroll)
	}

	r.zmu.Lock()
	r.zones, r.pending = r.pending, r.zones
	r.cardMax, r.pickMax = maxScroll, pickMax
	r.zmu.Unlock()
}

// rowList draws rows down list, inside card, scrolled up by scroll pixels, and returns how far they
// can scroll. Rows are drawn whole, then what lies above and below the list inside the card's width
// is put back from under (the frame as it was before the rows), which clips them without clipping
// every primitive; bg is the card's colour at the list's edges, for the fades.
func (r *renderer) rowList(card, list image.Rectangle, rows []settingRow, scroll int, bg color.RGBA, under []uint8) int {
	maxScroll := max(len(rows)*rowH-list.Dy(), 0)
	scroll = min(max(scroll, 0), maxScroll)
	mark := len(r.pending)
	for i, row := range rows {
		top := list.Min.Y + i*rowH - scroll
		if top+rowH <= list.Min.Y || top >= list.Max.Y {
			continue
		}
		r.settingRow(card, top, row)
		if i < len(rows)-1 {
			r.rule(card.Min.X+rowIn, card.Max.X-rowIn, top+rowH, 0.6)
		}
	}
	if maxScroll > 0 {
		r.restore(under, image.Rect(card.Min.X, 0, card.Max.X, list.Min.Y-1))
		r.restore(under, image.Rect(card.Min.X, list.Max.Y, card.Max.X, r.h))
		for i := mark; i < len(r.pending); i++ {
			r.pending[i].r = r.pending[i].r.Intersect(list)
		}
		r.scrollHints(list, scroll, maxScroll, bg)
	}
	return maxScroll
}

// restore puts the pixels of under, a frame the size of the screen, back over b.
func (r *renderer) restore(under []uint8, b image.Rectangle) {
	b = b.Intersect(r.dst.Rect)
	if len(under) != len(r.dst.Pix) || b.Empty() {
		return
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		i := r.dst.PixOffset(b.Min.X, y)
		copy(r.dst.Pix[i:i+4*b.Dx()], under[i:i+4*b.Dx()])
	}
}

// scrollHints fades a scrolled list into its edges where there is more, and draws a thin bar where
// the view sits in the whole.
func (r *renderer) scrollHints(list image.Rectangle, scroll, maxScroll int, bg color.RGBA) {
	const fade = 26
	for i := range fade {
		a := 1 - float64(i)/fade
		for x := list.Min.X + 8; x < list.Max.X-8; x++ {
			if scroll > 0 {
				r.blendAt(x, list.Min.Y+i, bg, a*0.9)
			}
			if scroll < maxScroll {
				r.blendAt(x, list.Max.Y-1-i, bg, a*0.9)
			}
		}
	}
	total := float64(list.Dy() + maxScroll)
	h := max(float64(list.Dy())*float64(list.Dy())/total, 30)
	y0 := float64(list.Min.Y+4) + (float64(list.Dy()-8)-h)*float64(scroll)/float64(maxScroll)
	x := float64(list.Max.X - 10)
	r.aaLine(x, y0+2, x, y0+h-2, 4, lerp(dim, cream, 0.2))
}

// nextCard is where the settings card sits on a w by h screen.
func nextCard(w, h int) image.Rectangle { return image.Rect(railW, cardIn, w-cardIn, h-cardIn) }

// baseKey is what the settings screen's unchanging part depends on: the category, the card's
// heading and the palette.
type baseKey struct {
	sel            category
	title, blurb   string
	g, a, t, d, rl color.RGBA
	w, h           int
}

// settingsBase paints the ground, the rail and the empty card with its heading. It is the costly
// part, soft shadows and gradients a pixel at a time, and the same every frame until the category, the
// heading or the theme changes, so it is kept and copied.
func (r *renderer) settingsBase(sel category, title, blurb string) {
	key := baseKey{sel, title, blurb, walnut, amber, cream, dim, ember, r.w, r.h}
	if r.base != nil && r.baseKey == key {
		copy(r.dst.Pix, r.base.Pix)
		return
	}
	fc := faces()
	r.settingsShell()

	// The rail: each category a label with its icon, the chosen one raised in the accent.
	for c := category(0); c < categories; c++ {
		top := navTop + int(c)*(navH+navGap)
		pill := image.Rect(12, top, railW-10, top+navH)
		fg, face := lerp(dim, cream, 0.3), fc.nav
		if c == sel {
			r.roundShadow(pill, 14, 14, 5, shadowAlpha())
			r.roundFill(pill, 14, shift(amber, 16), shift(amber, -16))
			r.roundHighlight(pill, 14)
			// The card floats above the rail: its shadow falls across the raised one's end, which the
			// kept shell (drawn before the rail) cannot hold, so that sliver is laid on again here.
			px0, py0, px1, py1 := rectF(pill)
			r.roundShadowIn(nextCard(r.w, r.h), cardRad, 22, 8, shadowAlpha(), pill.Inset(-1), func(x, y int) float64 {
				return clamp01(0.5 - rrDist(float64(x)+0.5, float64(y)+0.5, px0, py0, px1, py1, 14))
			})
			fg, face = onAccent(), fc.navBold
		}
		r.icon(c, 12+30, top+navH/2, fg)
		r.text(face, categoryNames[c], 12+58, top+navH/2+9, fg)
	}
	card := nextCard(r.w, r.h)
	r.text(fc.header, title, card.Min.X+rowIn, card.Min.Y+46, cream)
	r.text(fc.sub, blurb, card.Min.X+rowIn, card.Min.Y+72, dim)
	r.rule(card.Min.X+22, card.Max.X-22, card.Min.Y+headerH-2, 1)

	if r.base == nil || r.base.Rect != r.dst.Rect {
		r.base = image.NewRGBA(r.dst.Rect)
	}
	copy(r.base.Pix, r.dst.Pix)
	r.baseKey = key
}

// settingsShell paints what every category shares, the ground, Done and the empty card, from a copy
// kept until the theme changes: its gradients and the card's shadow are most of the drawing.
func (r *renderer) settingsShell() {
	key := baseKey{g: walnut, a: amber, t: cream, d: dim, rl: ember, w: r.w, h: r.h}
	if r.shell != nil && r.shellKey == key {
		copy(r.dst.Pix, r.shell.Pix)
		return
	}
	fc := faces()
	r.vgradient(r.dst.Bounds(), shift(walnut, 7), shift(walnut, -3))
	done := image.Rect(12, r.h-66, railW-10, r.h-16)
	r.roundShadow(done, 25, 8, 3, shadowAlpha()*0.6)
	r.roundFill(done, 25, surface(3), surface(2))
	r.roundStroke(done, 25, 1.2, ember)
	r.text(fc.button, "Done", done.Min.X+(done.Dx()-r.width(fc.button, "Done"))/2, done.Min.Y+33, cream)

	// The card, floating over the ground.
	card := nextCard(r.w, r.h)
	r.roundShadow(card, cardRad, 22, 8, shadowAlpha())
	r.roundFill(card, cardRad, surface(2), surface(1))
	r.roundHighlight(card, cardRad)

	if r.shell == nil || r.shell.Rect != r.dst.Rect {
		r.shell = image.NewRGBA(r.dst.Rect)
	}
	copy(r.shell.Pix, r.dst.Pix)
	r.shellKey = key
}

// pickRows is how many choices a long list shows at once, and pickScrollOver how many choices make
// a list long: up to that many sit in columns, all in view.
const (
	pickRows       = 6
	pickScrollOver = 18
)

// picker draws a list of choices over the dimmed screen, on a card of its own, and returns how far it
// can scroll. Up to pickScrollOver choices run in columns, every one a tap away; a longer list is one
// wide column that scrolls with a swipe, with room for long names.
func (r *renderer) picker(p pickerView, scroll int) int {
	fc := faces()
	r.addZone(zone{r: r.dst.Rect, kind: zoneDismiss})
	r.dimAll(0.5)

	const optH, pad, titleH, gap = 54, 18, 62, 8
	n := len(p.opts)
	cols, scrolls := 1, n > pickScrollOver
	switch {
	case scrolls:
	case n > 12:
		cols = 3
	case n > 6:
		cols = 2
	}
	per := (n + cols - 1) / cols
	optW := 300
	switch {
	case scrolls:
		optW = 640
	case cols == 3:
		optW = 220
	case cols == 2:
		optW = 250
	}
	if !scrolls {
		for _, o := range p.opts {
			optW = max(optW, min(r.width(fc.value, o)+90, (r.w-80)/cols-gap))
		}
	}
	if tw := r.width(fc.header, p.title) + 12; cols*optW+(cols-1)*gap < tw {
		optW = (tw - (cols-1)*gap + cols - 1) / cols // the title sets the width: the choices share it
	}
	shown := per
	if scrolls {
		shown = pickRows
	}
	w := cols*optW + (cols-1)*gap + 2*pad
	h := titleH + shown*optH + pad
	card := image.Rect((r.w-w)/2, (r.h-h)/2, (r.w+w)/2, (r.h+h)/2)
	list := image.Rect(card.Min.X+4, card.Min.Y+titleH, card.Max.X+2, card.Min.Y+titleH+shown*optH)
	maxScroll := 0
	if scrolls {
		maxScroll = per*optH - list.Dy()
		scroll = min(max(scroll, 0), maxScroll)
	} else {
		scroll = 0
	}

	r.roundShadow(card, cardRad, 26, 10, shadowAlpha()*1.2)
	r.roundFill(card, cardRad, surface(4), surface(3))
	r.roundHighlight(card, cardRad)
	// The card before its choices go on, to clip a scrolled list back to its window after.
	var under []uint8
	if scrolls {
		under = slices.Clone(r.dst.Pix)
	}

	for i, o := range p.opts {
		col, row := i/per, i%per
		x0 := card.Min.X + pad + col*(optW+gap)
		y0 := card.Min.Y + titleH + row*optH - scroll
		if y0+optH <= list.Min.Y || y0 >= list.Max.Y {
			continue
		}
		b := image.Rect(x0, y0+3, x0+optW, y0+optH-3)
		fg := cream
		if i == p.cur {
			r.roundFill(b, 14, shift(amber, 12), shift(amber, -12))
			r.roundHighlight(b, 14)
			fg = onAccent()
			cx, cy := float64(b.Max.X-26), float64(b.Min.Y+b.Dy()/2)
			r.aaLine(cx-8, cy, cx-3, cy+6, 2.8, fg)
			r.aaLine(cx-3, cy+6, cx+8, cy-6, 2.8, fg)
		} else {
			r.roundFill(b, 14, surface(6), surface(5))
		}
		tx := b.Min.X + 18
		if i < len(p.swatches) {
			sw := p.swatches[i]
			cx, cy := float64(b.Min.X+30), float64(b.Min.Y+b.Dy()/2)
			r.aaDisc(cx, cy, 13, sw[0])
			r.aaRing(cx, cy, 13, 1.4, 0, 2*math.Pi, lerp(sw[0], color.RGBA{255, 255, 255, 255}, 0.3))
			r.aaDisc(cx, cy, 6.5, sw[1])
			tx = b.Min.X + 54
		}
		r.text(fc.value, r.fit(fc.value, o, b.Max.X-44-tx), tx, b.Min.Y+b.Dy()/2+9, fg)
		zr := image.Rect(x0-gap/2, y0, x0+optW+gap/2, y0+optH)
		if scrolls {
			zr = zr.Intersect(list)
		}
		r.addZone(zone{r: zr, kind: zoneOption, opt: i})
	}
	if scrolls {
		r.restore(under, image.Rect(card.Min.X, card.Min.Y-optH, card.Max.X, list.Min.Y))
		r.restore(under, image.Rect(card.Min.X, list.Max.Y, card.Max.X, card.Max.Y+optH))
		r.scrollHints(list, scroll, maxScroll, surface(3))
	}
	r.text(fc.header, p.title, card.Min.X+pad+6, card.Min.Y+44, cream)
	return maxScroll
}

// fit shortens text to room pixels, with an ellipsis where it was cut.
func (r *renderer) fit(face font.Face, text string, room int) string {
	if text == "" || r.width(face, text) <= room {
		return text
	}
	runes := []rune(text)
	for len(runes) > 1 && r.width(face, string(runes)+"…") > room {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

// dimAll darkens the whole frame, for something drawn over it.
func (r *renderer) dimAll(by float64) {
	k := uint32((1 - by) * 256)
	p := r.dst.Pix
	for i := 0; i+3 < len(p); i += 4 {
		p[i] = uint8(uint32(p[i]) * k >> 8)
		p[i+1] = uint8(uint32(p[i+1]) * k >> 8)
		p[i+2] = uint8(uint32(p[i+2]) * k >> 8)
	}
}

func (r *renderer) settingRow(card image.Rectangle, top int, row settingRow) {
	fc := faces()
	face := fc.label
	if row.bold {
		face = fc.labelBold
	}
	if row.sub == "" {
		r.text(face, row.label, card.Min.X+rowIn, top+40, cream)
	} else {
		r.text(face, row.label, card.Min.X+rowIn, top+30, cream)
		r.text(fc.sub, row.sub, card.Min.X+rowIn, top+53, dim)
	}

	right, cy := card.Max.X-26, top+rowH/2
	whole := image.Rect(card.Min.X+8, top, card.Max.X-8, top+rowH)
	// A value never runs into the label: it keeps its start and loses its end to an ellipsis.
	labelEnd := card.Min.X + rowIn + max(r.width(face, row.label), r.width(fc.sub, row.sub))
	fit := func(text string, rightEdge int) string { return r.fit(fc.value, text, rightEdge-labelEnd-24) }
	add := func(r0 image.Rectangle, p part) {
		if row.id != "" {
			r.addZone(zone{r: r0, kind: zoneRow, id: row.id, part: p})
		}
	}
	switch row.kind {
	case ctlValue:
		v := fit(row.value, right)
		r.text(fc.value, v, right-r.width(fc.value, v), cy+9, dim)
	case ctlToggle:
		x := r.toggle(right, cy, row.on)
		if v := fit(row.value, x-16); v != "" {
			r.text(fc.value, v, x-16-r.width(fc.value, v), cy+9, dim)
		}
		if row.rowTap {
			add(whole, partRow)
			add(image.Rect(x-14, top, card.Max.X-8, top+rowH), partMain)
			break
		}
		add(whole, partMain) // the whole row flips the switch: a small target otherwise
	case ctlSwatches:
		// The colours for the role, the one in force ringed in the text colour.
		const d, gap = 28, 6
		x0 := right - swatchCount*d - (swatchCount-1)*gap
		in := current().colors[row.role]
		for i := range swatchCount {
			c := swatch(row.role, i)
			cx, cyf := float64(x0+i*(d+gap)+d/2), float64(cy)
			r.aaDisc(cx, cyf+1.5, d/2, lerp(c, color.RGBA{0, 0, 0, 255}, 0.5)) // a little depth under it
			r.aaDisc(cx, cyf, d/2, c)
			r.aaRing(cx, cyf, d/2-0.5, 1, 0, 2*math.Pi, lerp(c, color.RGBA{255, 255, 255, 255}, 0.25))
			if c == in {
				r.aaRing(cx, cyf, d/2+4, 2.4, 0, 2*math.Pi, cream)
			}
			if row.id != "" {
				x := x0 + i*(d+gap)
				r.addZone(zone{r: image.Rect(x-gap/2, top, x+d+gap/2, top+rowH), kind: zoneRow, id: row.id, part: partDay, opt: i})
			}
		}
	case ctlDays:
		const chipW, chipGap = 50, 8
		x0 := right - 7*chipW - 6*chipGap
		for i, name := range []string{"S", "M", "T", "W", "T", "F", "S"} {
			b := image.Rect(x0+i*(chipW+chipGap), cy-21, x0+i*(chipW+chipGap)+chipW, cy+21)
			ink := cream
			if row.days&(1<<i) != 0 {
				r.roundFill(b, 21, shift(amber, 12), shift(amber, -12))
				r.roundHighlight(b, 21)
				ink = onAccent()
			} else {
				r.roundFill(b, 21, surface(5), surface(4))
				r.roundStroke(b, 21, 1, ember)
			}
			r.text(fc.button, name, b.Min.X+(chipW-r.width(fc.button, name))/2, cy+8, ink)
			if row.id != "" {
				r.addZone(zone{r: image.Rect(b.Min.X-chipGap/2, top, b.Max.X+chipGap/2, top+rowH), kind: zoneRow, id: row.id, part: partDay, opt: i})
			}
		}
	case ctlStepper:
		minus, plus := r.stepper(right, cy, row.value)
		add(minus.Inset(-6), partMinus)
		add(plus.Inset(-6), partPlus)
	case ctlChoice:
		extra := 0
		if row.button != "" {
			extra = r.width(fc.button, row.button) + 44 + 12
		}
		x := r.choice(right, cy, fit(row.value, right-58-extra))
		if row.button != "" {
			bx := r.pillButton(x-12, cy, row.button, btnSecondary)
			add(whole, partMain)
			add(image.Rect(bx-6, top, x-6, top+rowH), partExtra)
			break
		}
		add(whole, partMain)
	case ctlButton, ctlDanger:
		style := btnSecondary
		if row.kind == ctlDanger {
			style = btnDanger
		}
		x := r.pillButton(right, cy, row.button, style)
		if v := fit(row.value, x-16); v != "" {
			r.text(fc.value, v, x-16-r.width(fc.value, v), cy+9, dim)
		}
		if row.rowTap {
			add(whole, partMain) // the whole row does what its button does
		}
		add(image.Rect(x-8, top+4, card.Max.X-12, top+rowH-4), partMain)
	}
}

// toggle draws a switch ending at right, centred on cy, and returns its left edge.
func (r *renderer) toggle(right, cy int, on bool) int {
	track := image.Rect(right-66, cy-17, right, cy+17)
	if on {
		r.roundFill(track, 17, shift(amber, 10), shift(amber, -14))
	} else {
		r.roundFill(track, 17, surface(6), surface(5))
		r.roundStroke(track, 17, 1, ember)
	}
	kx := float64(track.Min.X + 17)
	if on {
		kx = float64(track.Max.X - 17)
	}
	knob := image.Rect(int(kx)-13, cy-13, int(kx)+13, cy+13)
	r.roundShadow(knob, 13, 5, 2, 0.45)
	r.roundFill(knob, 13, shift(cream, 12), shift(cream, -6))
	return track.Min.X
}

// stepper draws − value + ending at right, and returns where its two buttons are.
func (r *renderer) stepper(right, cy int, value string) (minusAt, plusAt image.Rectangle) {
	fc := faces()
	plus := image.Rect(right-44, cy-20, right, cy+20)
	vw := max(r.width(fc.value, value), 80)
	minus := image.Rect(plus.Min.X-vw-24-44, cy-20, plus.Min.X-vw-24, cy+20)
	for _, b := range []image.Rectangle{minus, plus} {
		r.roundShadow(b, 12, 6, 2, shadowAlpha()*0.7)
		r.roundFill(b, 12, surface(7), surface(5))
		r.roundHighlight(b, 12)
	}
	mx, px := float64(minus.Min.X+22), float64(plus.Min.X+22)
	r.aaLine(mx-8, float64(cy), mx+8, float64(cy), 2.6, cream)
	r.aaLine(px-8, float64(cy), px+8, float64(cy), 2.6, cream)
	r.aaLine(px, float64(cy)-8, px, float64(cy)+8, 2.6, cream)
	r.text(fc.value, value, minus.Max.X+12+(vw-r.width(fc.value, value))/2, cy+9, cream)
	return minus, plus
}

// choice draws a value in a pill with a chevron, the way to a list of options; returns its left edge.
func (r *renderer) choice(right, cy int, value string) int {
	fc := faces()
	w := r.width(fc.value, value) + 58
	pill := image.Rect(right-w, cy-20, right, cy+20)
	r.roundFill(pill, 20, surface(5), surface(4))
	r.roundStroke(pill, 20, 1, ember)
	r.text(fc.value, value, pill.Min.X+20, cy+9, cream)
	x, y := float64(pill.Max.X-22), float64(cy)
	r.aaLine(x-4, y-7, x+3, y, 2.4, dim)
	r.aaLine(x+3, y, x-4, y+7, 2.4, dim)
	return pill.Min.X
}

type buttonStyle int

const (
	btnPrimary   buttonStyle = iota // the page's main action, filled in the accent
	btnSecondary                    // a row's action, quiet
	btnDanger                       // an action to think twice about
)

// danger is the colour of an action to think twice about, whatever the theme.
var danger = color.RGBA{0xe5, 0x48, 0x4d, 0xff}

// pillButton draws an action ending at right and returns its left edge.
func (r *renderer) pillButton(right, cy int, label string, style buttonStyle) int {
	fc := faces()
	w := r.width(fc.button, label) + 44
	b := image.Rect(right-w, cy-20, right, cy+20)
	fg := cream
	switch style {
	case btnPrimary:
		r.roundShadow(b, 20, 8, 3, shadowAlpha()*0.8)
		r.roundFill(b, 20, shift(amber, 14), shift(amber, -14))
		r.roundHighlight(b, 20)
		fg = onAccent()
	case btnSecondary:
		r.roundShadow(b, 20, 6, 2, shadowAlpha()*0.5)
		r.roundFill(b, 20, surface(6), surface(4))
		r.roundStroke(b, 20, 1, ember)
		r.roundHighlight(b, 20)
	case btnDanger:
		r.roundFill(b, 20, surface(4), surface(3))
		r.roundStroke(b, 20, 1.6, danger)
		fg = danger
	}
	r.text(fc.button, label, b.Min.X+22, cy+8, fg)
	return b.Min.X
}

// icon is a category's line drawing, about 26 pixels across, centred on (cx, cy).
func (r *renderer) icon(c category, cx, cy int, col color.RGBA) {
	x, y := float64(cx), float64(cy)
	const w = 2.6
	switch c {
	case catDisplay: // the sun of brightness
		r.aaRing(x, y, 5.5, w, 0, 2*math.Pi, col)
		for i := range 8 {
			a := float64(i) * math.Pi / 4
			r.aaLine(x+9*math.Cos(a), y+9*math.Sin(a), x+12.5*math.Cos(a), y+12.5*math.Sin(a), w, col)
		}
	case catSound: // a microphone
		r.roundStrokeF(x-5, y-12, x+5, y+4, 5, w, col)
		r.aaRing(x, y-2, 9, w, 0.12*math.Pi, 0.88*math.Pi, col)
		r.aaLine(x, y+7, x, y+12, w, col)
		r.aaLine(x-5, y+12.5, x+5, y+12.5, w, col)
	case catAlarms: // a clock
		r.aaRing(x, y, 11.5, w, 0, 2*math.Pi, col)
		r.aaLine(x, y, x, y-6.5, w, col)
		r.aaLine(x, y, x+5, y+2, w, col)
	case catConnections: // Wi-Fi
		for _, rad := range []float64{5, 10.5, 16} {
			r.aaRing(x, y+9, rad, w, -0.75*math.Pi, -0.25*math.Pi, col)
		}
		r.aaDisc(x, y+9, 2.4, col)
	case catSecurity: // a padlock
		r.roundStrokeF(x-10, y-2, x+10, y+12, 3.5, w, col)
		r.aaRing(x, y-6, 6, w, math.Pi, 2*math.Pi, col)
		r.aaLine(x-6, y-6, x-6, y-2, w, col)
		r.aaLine(x+6, y-6, x+6, y-2, w, col)
	case catGeneral: // sliders
		for i, knob := range []float64{4, -5, 2} {
			ly := y - 8 + float64(i)*8
			r.aaLine(x-12, ly, x+12, ly, w-0.4, col)
			r.aaDisc(x+knob, ly, 3.4, col)
		}
	}
}

// surface is the ground raised by level steps: lighter on a dark theme, darker on a light one.
func surface(level int) color.RGBA {
	if dark() {
		return shift(walnut, 6*level)
	}
	return shift(walnut, -4*level)
}

func shadowAlpha() float64 {
	if dark() {
		return 0.6
	}
	return 0.22
}

// onAccent is text that reads on the accent: the ground on a light accent, the text colour on a dark one.
func onAccent() color.RGBA {
	if 0.299*float64(amber.R)+0.587*float64(amber.G)+0.114*float64(amber.B) > 128 {
		return shift(walnut, -8)
	}
	return cream
}

// ---- anti-aliased drawing ------------------------------------------------------------------------

// clamp01 and rrDist use the built-in min and max: math.Min and math.Max handle NaN and signed
// zeros, which these never see, at several times the cost, and they run for every pixel drawn.
func clamp01(v float64) float64 { return max(0, min(1, v)) }

func lerp(a, b color.RGBA, t float64) color.RGBA {
	f := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5) }
	return color.RGBA{f(a.R, b.R), f(a.G, b.G), f(a.B, b.B), 255}
}

// blendAt lays c over the pixel at (x, y) with coverage a.
func (r *renderer) blendAt(x, y int, c color.RGBA, a float64) {
	if a <= 0 || !(image.Point{x, y}).In(r.dst.Rect) {
		return
	}
	a = math.Min(a, 1)
	i := r.dst.PixOffset(x, y)
	p := r.dst.Pix[i : i+4 : i+4]
	p[0] = uint8(float64(p[0])*(1-a) + float64(c.R)*a + 0.5)
	p[1] = uint8(float64(p[1])*(1-a) + float64(c.G)*a + 0.5)
	p[2] = uint8(float64(p[2])*(1-a) + float64(c.B)*a + 0.5)
	p[3] = 255
}

// rrDist is the signed distance from (px, py) to the rounded rectangle x0..x1, y0..y1 of radius
// rad: negative inside.
func rrDist(px, py, x0, y0, x1, y1, rad float64) float64 {
	qx := math.Abs(px-(x0+x1)/2) - ((x1-x0)/2 - rad)
	qy := math.Abs(py-(y0+y1)/2) - ((y1-y0)/2 - rad)
	ox, oy := max(qx, 0), max(qy, 0)
	return math.Sqrt(ox*ox+oy*oy) + min(max(qx, qy), 0) - rad
}

func rectF(b image.Rectangle) (float64, float64, float64, float64) {
	return float64(b.Min.X), float64(b.Min.Y), float64(b.Max.X), float64(b.Max.Y)
}

func (r *renderer) vgradient(b image.Rectangle, top, bottom color.RGBA) {
	for y := b.Min.Y; y < b.Max.Y; y++ {
		r.span(y, b.Min.X, b.Max.X, lerp(top, bottom, float64(y-b.Min.Y)/float64(max(b.Dy()-1, 1))))
	}
}

// span paints x0 up to x1 on row y in c, solid.
func (r *renderer) span(y, x0, x1 int, c color.RGBA) {
	if y < r.dst.Rect.Min.Y || y >= r.dst.Rect.Max.Y {
		return
	}
	x0, x1 = max(x0, r.dst.Rect.Min.X), min(x1, r.dst.Rect.Max.X)
	if x0 >= x1 {
		return
	}
	row := r.dst.Pix[r.dst.PixOffset(x0, y):r.dst.PixOffset(x1, y)]
	for i := 0; i < len(row); i += 4 {
		row[i], row[i+1], row[i+2], row[i+3] = c.R, c.G, c.B, 255
	}
}

// roundFill fills a rounded rectangle with a vertical gradient from top to bottom.
func (r *renderer) roundFill(b image.Rectangle, rad float64, top, bottom color.RGBA) {
	x0, y0, x1, y1 := rectF(b)
	// Only the corners are worked out a pixel at a time; between them each row is solid. The rows
	// just outside the top and bottom edges are left alone: no pixel centre there is covered.
	corner := min(int(math.Ceil(rad))+1, b.Dx()/2)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		c := lerp(top, bottom, clamp01((float64(y)-y0)/(y1-y0)))
		for x := b.Min.X - 1; x < b.Min.X+corner; x++ {
			r.blendAt(x, y, c, clamp01(0.5-rrDist(float64(x)+0.5, float64(y)+0.5, x0, y0, x1, y1, rad)))
		}
		for x := b.Max.X - corner; x <= b.Max.X; x++ {
			r.blendAt(x, y, c, clamp01(0.5-rrDist(float64(x)+0.5, float64(y)+0.5, x0, y0, x1, y1, rad)))
		}
		r.span(y, b.Min.X+corner, b.Max.X-corner, c)
	}
}

// roundStroke draws a rounded rectangle's outline, width w, inside its edge.
func (r *renderer) roundStroke(b image.Rectangle, rad, w float64, c color.RGBA) {
	x0, y0, x1, y1 := rectF(b)
	r.roundStrokeF(x0, y0, x1, y1, rad, w, c)
}

func (r *renderer) roundStrokeF(x0, y0, x1, y1, rad, w float64, c color.RGBA) {
	for y := int(y0) - 1; y <= int(y1)+1; y++ {
		for x := int(x0) - 1; x <= int(x1)+1; x++ {
			d := rrDist(float64(x)+0.5, float64(y)+0.5, x0, y0, x1, y1, rad)
			r.blendAt(x, y, c, clamp01(0.5-(math.Abs(d+w/2)-w/2)))
		}
	}
}

// roundHighlight is the light catching a raised surface's top edge.
func (r *renderer) roundHighlight(b image.Rectangle, rad float64) {
	x0, y0, x1, y1 := rectF(b)
	hl := color.RGBA{255, 255, 255, 255}
	for y := b.Min.Y; y < b.Min.Y+int(rad)+2 && y < b.Max.Y; y++ {
		fall := 1 - clamp01((float64(y)-y0)/(rad+2))
		for x := b.Min.X; x < b.Max.X; x++ {
			d := rrDist(float64(x)+0.5, float64(y)+0.5, x0, y0, x1, y1, rad)
			edge := clamp01(0.5 - (math.Abs(d+0.6) - 0.6))
			r.blendAt(x, y, hl, edge*fall*0.22)
		}
	}
}

// roundShadow is the soft shadow a rounded rectangle casts, blur pixels wide and dropped dy. The
// rectangle is always filled over it after, so the shadow under its solid middle is not drawn.
func (r *renderer) roundShadow(b image.Rectangle, rad, blur float64, dy int, alpha float64) {
	r.roundShadowIn(b, rad, blur, dy, alpha, r.dst.Rect, nil)
}

// roundShadowIn is roundShadow drawn only inside clip, and where mask is given, only as much as it
// says at each pixel (0 to 1): the shadow laid on one thing drawn after it.
func (r *renderer) roundShadowIn(b image.Rectangle, rad, blur float64, dy int, alpha float64, clip image.Rectangle, mask func(x, y int) float64) {
	x0, y0, x1, y1 := rectF(b.Add(image.Pt(0, dy)))
	pad := int(blur) + 1
	black := color.RGBA{0, 0, 0, 255}
	covered := b.Inset(int(rad) + 2)
	area := image.Rect(b.Min.X-pad, b.Min.Y+dy-pad, b.Max.X+pad, b.Max.Y+dy+pad).Intersect(clip)
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			if y >= covered.Min.Y && y < covered.Max.Y && x == covered.Min.X {
				x = covered.Max.X // jump the covered middle of the row
			}
			d := rrDist(float64(x)+0.5, float64(y)+0.5, x0, y0, x1, y1, rad)
			f := 1 - clamp01((d+blur*0.3)/(blur*1.3))
			a := alpha * f * f
			if mask != nil {
				a *= mask(x, y)
			}
			r.blendAt(x, y, black, a)
		}
	}
}

// rule is a thin horizontal line in the rules colour.
func (r *renderer) rule(x0, x1, y int, alpha float64) {
	for x := x0; x < x1; x++ {
		r.blendAt(x, y, ember, alpha)
	}
}

// aaLine is a stroke of width w with round ends.
func (r *renderer) aaLine(x0, y0, x1, y1, w float64, c color.RGBA) {
	dx, dy := x1-x0, y1-y0
	l2 := dx*dx + dy*dy
	for y := int(math.Min(y0, y1) - w - 1); y <= int(math.Max(y0, y1)+w+1); y++ {
		for x := int(math.Min(x0, x1) - w - 1); x <= int(math.Max(x0, x1)+w+1); x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			t := 0.0
			if l2 > 0 {
				t = clamp01(((px-x0)*dx + (py-y0)*dy) / l2)
			}
			d := math.Hypot(px-(x0+t*dx), py-(y0+t*dy)) - w/2
			r.blendAt(x, y, c, clamp01(0.5-d))
		}
	}
}

// aaRing is an arc of a circle of radius rad and width w, from angle a0 to a1 (y points down).
func (r *renderer) aaRing(cx, cy, rad, w, a0, a1 float64, c color.RGBA) {
	full := a1-a0 >= 2*math.Pi-1e-9
	for y := int(cy - rad - w - 1); y <= int(cy+rad+w+1); y++ {
		for x := int(cx - rad - w - 1); x <= int(cx+rad+w+1); x++ {
			px, py := float64(x)+0.5-cx, float64(y)+0.5-cy
			if !full {
				a := math.Atan2(py, px)
				for a < a0 {
					a += 2 * math.Pi
				}
				for a > a0+2*math.Pi {
					a -= 2 * math.Pi
				}
				if a > a1 {
					continue
				}
			}
			d := math.Abs(math.Hypot(px, py)-rad) - w/2
			r.blendAt(x, y, c, clamp01(0.5-d))
		}
	}
}

func (r *renderer) aaDisc(cx, cy, rad float64, c color.RGBA) {
	for y := int(cy - rad - 1); y <= int(cy+rad+1); y++ {
		for x := int(cx - rad - 1); x <= int(cx+rad+1); x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) - rad
			r.blendAt(x, y, c, clamp01(0.5-d))
		}
	}
}
