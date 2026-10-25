//go:build !dot

package display

import (
	"image"
	"image/color"
	"math"
	"slices"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// The settings screen's parts, shared by the Show and the Spot: its categories, rows and their
// controls, lists of choices, scrolling, the zones taps are matched to, and the anti-aliased shapes
// they are drawn from. Every shape is a rounded rectangle, circle or stroke with soft shadows,
// drawn from the five palette colors, so it follows whichever theme is in force.

// The palette the settings screen draws with: the ground, the accent, text, dim text and rules. The
// Show sets them from its theme (applyTheme); the Spot from its own colors.
var (
	walnut = color.RGBA{0x1c, 0x15, 0x11, 0xff}
	amber  = color.RGBA{0xe9, 0xa2, 0x3b, 0xff}
	cream  = color.RGBA{0xe8, 0xdc, 0xc8, 0xff}
	dim    = color.RGBA{0x8a, 0x7d, 0x6c, 0xff}
	ember  = color.RGBA{0x3a, 0x2c, 0x22, 0xff}
)

// paint is a canvas the settings parts draw on, with the zones taps are matched to. The Show's and
// the Spot's renderers both carry one.
type paint struct {
	dst  *image.RGBA
	w, h int

	// zones are where taps mean something in the frame last drawn, read by the touch goroutine under
	// zmu; pending is the frame being drawn. cardMax and pickMax are how far the card and an open
	// list could scroll in it.
	zmu              sync.Mutex
	zones, pending   []zone
	cardMax, pickMax int

	// fc is the text sizes, when a device sets its own; round is a round panel, whose scroll
	// indicator follows the circle's edge.
	fc    *sheetFaces
	round bool

	// marked is a mark in a color that means something drawn in the frame being drawn: the header's
	// "microphone off" pill, Talk red while live. The Show takes the skin's filter off for that frame
	// (unskinned), so those keep their colors.
	marked bool
	// footAt is where the controls along the page's foot begin in the frame being drawn - the music
	// strip, the Now Playing transport, the Tessera page's dots - and 0 with none there: a cue's
	// captions sit above them (captions), so a tap on them does what it does.
	footAt int

	// font is the screen font the faces in use were made in (font.go), "" for Go, and fontGen the
	// fonts of the owner's own when they were (lib/fonts.Generation).
	font    string
	fontGen uint64

	// The drawn dashboard's tiles where they were last drawn, and how far it can scroll; bigTiles is
	// the Dashboard tiles setting asking for them larger, for the frame being drawn.
	dashTiles   []dashTile
	dashContent int
	bigTiles    bool

	// The color sheet's parts and the sheet itself where they were last drawn, under zmu: empty while
	// no sheet is up (color_sheet.go).
	colorZones []colorZone
	colorCard  image.Rectangle
	// The media sheet's likewise (media_sheet.go).
	mediaZones        []mediaZone
	mediaCard         image.Rectangle
	mediaPages        int // the pages of favorites the sheet has
	mediaSpeakerPages int // and of speakers

	// captionsAt is where a cue turn's captions were drawn in the frame last drawn, under zmu: empty
	// while there are none (turn_cue.go).
	captionsAt image.Rectangle

	// sNum and sDen scale this screen's fixed sizes against the panel the layout was drawn for. The
	// Echo Show 5 is that panel and stays 1:1; the Show 8 is 4:3 of it across.
	//
	// Positions taken from w and h need no scaling and must not be given any — they already land in
	// the right place on any panel, and that is most of this layout. What needs it is everything
	// absolute: a padding, a row height, a corner radius, the thickness of a line, the size of a
	// piece of text. Zero means 1:1, so a paint that never sets these behaves exactly as before.
	sNum, sDen int

	// over is drawing words over a photo (readable.go).
	over overPhoto
}

// s scales a fixed size to this screen. Every size written as a literal in this package is in the
// Show 5's pixels, because that is the panel the layout was drawn on and measured against.
func (p *paint) s(n int) int {
	if p.sDen == 0 || p.sNum == p.sDen {
		return n
	}
	if n < 0 {
		return -((-n*p.sNum + p.sDen/2) / p.sDen)
	}
	return (n*p.sNum + p.sDen/2) / p.sDen
}

// The settings screen's fixed sizes, scaled to the screen in hand. The Base values beside them are
// the Show 5's, which is the panel every number in this package was chosen on.
func (p *paint) railW() int       { return p.s(railWBase) }
func (p *paint) navTop() int      { return p.s(navTopBase) }
func (p *paint) navH() int        { return p.s(navHBase) }
func (p *paint) navGap() int      { return p.s(navGapBase) }
func (p *paint) cardIn() int      { return p.s(cardInBase) }
func (p *paint) cardRad() float64 { return float64(p.s(cardRadBase)) }
func (p *paint) headerH() int     { return p.s(headerHBase) }
func (p *paint) rowH() int        { return p.s(rowHBase) }
func (p *paint) rowIn() int       { return p.s(rowInBase) }

// rowHeight is how tall row is drawn: a heading is shorter than a row with something on it.
func (p *paint) rowHeight(row settingRow) int {
	if row.kind == ctlHeading {
		return p.s(headHBase)
	}
	return p.rowH()
}

// rowsHeight is how tall rows are, one under another.
func (p *paint) rowsHeight(rows []settingRow) int {
	h := 0
	for _, row := range rows {
		h += p.rowHeight(row)
	}
	return h
}

// scaled reports whether this screen differs from the one the layout was drawn for.
// sf is s for the float sizes the rounded-shape helpers take: radii, blurs and stroke widths.
func (p *paint) sf(n int) float64 { return float64(p.s(n)) }

// sk is the scale as a factor, for the float sizes that are not whole pixels: a stroke's width, a
// point of a line drawing. Exactly 1 on the Show 5, so what it multiplies is drawn as before there.
func (p *paint) sk() float64 { return p.sf(100) / 100 }

func (p *paint) scaled() bool { return p.sDen != 0 && p.sNum != p.sDen }

// faces is the text sizes the settings parts draw with: the device's own, or the Show's.
func (r *paint) faces() sheetFaces {
	if r.fc != nil {
		return *r.fc
	}
	return facesFor(fontKey{r.font, r.fontGen})
}

func (r *paint) text(face font.Face, s string, x, baseline int, c color.Color) {
	if face == nil {
		return
	}
	if r.over.record {
		if face.Metrics().Height.Ceil() <= r.s(scrimTallest) {
			r.over.noteText(face, s, x, baseline)
		}
		return
	}
	if r.over.photo != nil {
		r.over.halo(r.dst, face, s, x, baseline)
		if _, chosen := c.(chosenColor); !chosen && face.Metrics().Height.Ceil() <= r.s(scrimTallest) {
			c = photoGold
		}
	}
	d := &font.Drawer{Dst: r.dst, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, baseline)}
	d.DrawString(s)
}

func (r *paint) width(face font.Face, s string) int {
	if face == nil {
		return 0
	}
	return (&font.Drawer{Face: face}).MeasureString(s).Ceil()
}

// wrap breaks text into lines no wider than maxW, on spaces; a single word wider than the line is
// left to overflow rather than split.
func (r *paint) wrap(face font.Face, s string, maxW int) []string {
	var lines []string
	var line string
	for _, word := range strings.Fields(s) {
		try := word
		if line != "" {
			try = line + " " + word
		}
		if line != "" && r.width(face, try) > maxW {
			lines = append(lines, line)
			line = word
			continue
		}
		line = try
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// shift lightens (positive) or darkens (negative) a color by d per channel.
func shift(c color.RGBA, d int) color.RGBA {
	f := func(v uint8) uint8 {
		n := int(v) + d
		if n < 0 {
			n = 0
		}
		if n > 255 {
			n = 255
		}
		return uint8(n)
	}
	return color.RGBA{f(c.R), f(c.G), f(c.B), c.A}
}

// dark reports whether the palette is a dark one, which decides which way surfaces are lit.
func dark() bool {
	return int(walnut.R)+int(walnut.G)+int(walnut.B) < 384
}

// The settings screen regrouped by category: a rail of categories on the left with the chosen one
// raised out of it, and that category's settings on a card floating over the ground. Every shape is
// an anti-aliased rounded rectangle, circle or stroke with soft shadows, drawn from the theme's five
// colors, so it follows whichever theme is chosen.

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
	"Brightness, night, theme, home screen and weather",
	"Volume, voice, quiet hours and music",
	"Alarms on this device and how they ring",
	"Wi-Fi, Bluetooth and the Bluetooth proxy",
	"Remote access and how this device is reached",
	"Name, weather, updates and restart",
}

// controlKind is what sits at the right of a settings row.
type controlKind int

const (
	ctlValue    controlKind = iota // a value to read, nothing to press
	ctlToggle                      // a switch
	ctlStepper                     // − value +
	ctlChoice                      // a value that opens a list
	ctlButton                      // an action
	ctlDanger                      // an action to think twice about
	ctlDays                        // the seven days of the week, each on or off
	ctlSwatches                    // a strip of colors for one role of the theme
	ctlHeading                     // a group's name over the rows under it: shorter, nothing to press
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
	locked     bool   // behind the settings lock while it is on: a padlock after its name
}

// part is which piece of a row a tap landed on.
type part int

const (
	partMain  part = iota // the switch, the choice, the button, or the row around a switch or choice
	partMinus             // a stepper's −
	partPlus              // a stepper's +
	partExtra             // the second button a choice can carry (Updates' Check now)
	partRow               // the row itself, away from its control, on a row with rowTap
	partDay               // a day of ctlDays, or a color of ctlSwatches; the zone's opt is which
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

	label string // a row's name, for the PIN pad when the row is behind the settings lock
}

// pickerView is a list of choices open over the card: one is picked, or a tap elsewhere puts it away.
type pickerView struct {
	title    string
	opts     []string
	cur      int             // the choice in force, or -1
	swatches [][2]color.RGBA // optional: a ground and accent dot beside each choice (themes)
}

// zoneAt is what a tap at (x, y) landed on in the frame last drawn: the topmost zone holding it, or
// failing that the nearest within a finger's slop of it.
func (r *paint) zoneAt(x, y int) (zone, bool) {
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

func (r *paint) addZone(z zone) { r.pending = append(r.pending, z) }

// These are the settings screen's fixed sizes in the Show 5's pixels, which is the panel this
// layout was drawn on. Read them through the methods below, which scale them to the screen in
// hand; nothing should use a Base value directly.
const (
	railWBase   = 244
	navTopBase  = 20
	navHBase    = 54
	navGapBase  = 8
	cardInBase  = 14 // the card's margin from the screen's edges
	cardRadBase = 22
	headerHBase = 82
	rowHBase    = 60
	headHBase   = 44 // a ctlHeading row
	rowInBase   = 30 // text inset inside the card
)

// sheetFaces are the settings screen's own sizes of the font the rest of the screen uses.
type sheetFaces struct{ header, label, labelBold, sub, value, nav, navBold, button font.Face }

// sheetFacesAt builds them in the font named through a scale: 1:1 for the panel these sizes were
// chosen on (facesFor keeps those), or a renderer's own for another. Not cached: a renderer builds its
// own once per font.
func sheetFacesAt(name string, s func(int) int) sheetFaces {
	f := func(bold bool, size int) font.Face { return fontFace(name, bold, float64(s(size))) }
	return sheetFaces{
		header: f(true, 36), label: f(false, 29), labelBold: f(true, 29), sub: f(false, 21),
		value: f(false, 25), nav: f(false, 24), navBold: f(true, 24), button: f(true, 23),
	}
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
func (r *paint) scrollLimits() (card, pick int) {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	return r.cardMax, r.pickMax
}

// rowList draws rows down list, inside card, scrolled up by scroll pixels, and returns how far they
// can scroll. Rows are drawn whole, then what lies above and below the list inside the card's width
// is put back from under (the frame as it was before the rows), which clips them without clipping
// every primitive; bg is the card's color at the list's edges, for the fades.
func (r *paint) rowList(card, list image.Rectangle, rows []settingRow, scroll int, bg color.RGBA, under []uint8) int {
	maxScroll := max(r.rowsHeight(rows)-list.Dy(), 0)
	scroll = min(max(scroll, 0), maxScroll)
	mark := len(r.pending)
	top := list.Min.Y - scroll
	for i, row := range rows {
		h := r.rowHeight(row)
		if top+h > list.Min.Y && top < list.Max.Y {
			r.settingRow(card, top, row)
			// A rule between rows, but none to set a heading off from the rows it names.
			if i < len(rows)-1 && row.kind != ctlHeading && rows[i+1].kind != ctlHeading {
				r.rule(card.Min.X+r.rowIn(), card.Max.X-r.rowIn(), top+h, 0.6)
			}
		}
		top += h
	}
	if maxScroll > 0 {
		r.restore(under, image.Rect(card.Min.X, 0, card.Max.X, list.Min.Y-1))
		r.restore(under, image.Rect(card.Min.X, list.Max.Y, card.Max.X, r.h))
		// A row half off the end of the list keeps only the part inside it; one clipped away
		// entirely is dropped, since a zone with no area is a tap target nobody can hit.
		kept := r.pending[:mark]
		for _, z := range r.pending[mark:] {
			if z.r = z.r.Intersect(list); !z.r.Empty() {
				kept = append(kept, z)
			}
		}
		r.pending = kept
		r.scrollHints(list, scroll, maxScroll, bg)
	}
	return maxScroll
}

// restore puts the pixels of under, a frame the size of the screen, back over b.
func (r *paint) restore(under []uint8, b image.Rectangle) {
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
func (r *paint) scrollHints(list image.Rectangle, scroll, maxScroll int, bg color.RGBA) {
	fade := r.s(26)
	for i := range fade {
		a := 1 - float64(i)/float64(fade)
		for x := list.Min.X + r.s(8); x < list.Max.X-r.s(8); x++ {
			if scroll > 0 {
				r.blendAt(x, list.Min.Y+i, bg, a*0.9)
			}
			if scroll < maxScroll {
				r.blendAt(x, list.Max.Y-1-i, bg, a*0.9)
			}
		}
	}
	total := float64(list.Dy() + maxScroll)
	if r.round {
		// An arc just inside the circle's right edge, a quarter of it long: the thumb is where the view
		// sits in the whole, over a faint track.
		cx, cy := float64(r.w)/2, float64(r.h)/2
		rad := float64(r.w)/2 - 12
		const a0, span = -math.Pi / 4, math.Pi / 2
		thumb := max(span*float64(list.Dy())/total, 0.18)
		at := a0 + (span-thumb)*float64(scroll)/float64(maxScroll)
		r.aaRing(cx, cy, rad, 3, a0, a0+span, lerp(walnut, dim, 0.25))
		r.aaRing(cx, cy, rad, 3, at, at+thumb, lerp(dim, cream, 0.3))
		return
	}
	k := r.sk()
	h := max(float64(list.Dy())*float64(list.Dy())/total, r.sf(30))
	y0 := float64(list.Min.Y+r.s(4)) + (float64(list.Dy()-r.s(8))-h)*float64(scroll)/float64(maxScroll)
	x := float64(list.Max.X - r.s(10))
	r.aaLine(x, y0+k*2, x, y0+h-k*2, k*4, lerp(dim, cream, 0.2))
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
func (r *paint) picker(p pickerView, scroll int) int {
	fc := r.faces()
	r.addZone(zone{r: r.dst.Rect, kind: zoneDismiss})
	r.dimAll(0.5)

	optH, pad, titleH, gap := r.s(54), r.s(18), r.s(62), r.s(8)
	k := r.sk()
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
	optW := r.s(300)
	switch {
	case scrolls:
		optW = r.s(640)
	case cols == 3:
		optW = r.s(220)
	case cols == 2:
		optW = r.s(250)
	}
	if !scrolls {
		for _, o := range p.opts {
			optW = max(optW, min(r.width(fc.value, o)+r.s(90), (r.w-r.s(80))/cols-gap))
		}
	}
	if tw := r.width(fc.header, p.title) + r.s(12); cols*optW+(cols-1)*gap < tw {
		optW = (tw - (cols-1)*gap + cols - 1) / cols // the title sets the width: the choices share it
	}
	shown := per
	if scrolls {
		shown = pickRows
	}
	w := cols*optW + (cols-1)*gap + 2*pad
	h := titleH + shown*optH + pad
	card := image.Rect((r.w-w)/2, (r.h-h)/2, (r.w+w)/2, (r.h+h)/2)
	list := image.Rect(card.Min.X+r.s(4), card.Min.Y+titleH, card.Max.X+r.s(2), card.Min.Y+titleH+shown*optH)
	maxScroll := 0
	if scrolls {
		maxScroll = per*optH - list.Dy()
		scroll = min(max(scroll, 0), maxScroll)
	} else {
		scroll = 0
	}

	r.roundShadow(card, r.cardRad(), r.sf(26), r.s(10), shadowAlpha()*1.2)
	r.roundFill(card, r.cardRad(), surface(4), surface(3))
	r.roundHighlight(card, r.cardRad())
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
		b := image.Rect(x0, y0+r.s(3), x0+optW, y0+optH-r.s(3))
		fg := cream
		if i == p.cur {
			r.roundFill(b, r.sf(14), shift(amber, 12), shift(amber, -12))
			r.roundHighlight(b, r.sf(14))
			fg = onAccent()
			cx, cy := float64(b.Max.X-r.s(26)), float64(b.Min.Y+b.Dy()/2)
			r.aaLine(cx-k*8, cy, cx-k*3, cy+k*6, k*2.8, fg)
			r.aaLine(cx-k*3, cy+k*6, cx+k*8, cy-k*6, k*2.8, fg)
		} else {
			r.roundFill(b, r.sf(14), surface(6), surface(5))
		}
		tx := b.Min.X + r.s(18)
		if i < len(p.swatches) {
			sw := p.swatches[i]
			cx, cy := float64(b.Min.X+r.s(30)), float64(b.Min.Y+b.Dy()/2)
			r.aaDisc(cx, cy, k*13, sw[0])
			r.aaRing(cx, cy, k*13, k*1.4, 0, 2*math.Pi, lerp(sw[0], color.RGBA{255, 255, 255, 255}, 0.3))
			r.aaDisc(cx, cy, k*6.5, sw[1])
			tx = b.Min.X + r.s(54)
		}
		r.text(fc.value, r.fit(fc.value, o, b.Max.X-r.s(44)-tx), tx, b.Min.Y+b.Dy()/2+r.s(9), fg)
		zr := image.Rect(x0-gap/2, y0, x0+optW+gap/2, y0+optH)
		if scrolls {
			zr = zr.Intersect(list)
		}
		r.addZone(zone{r: zr, kind: zoneOption, opt: i, label: p.title})
	}
	if scrolls {
		r.restore(under, image.Rect(card.Min.X, card.Min.Y-optH, card.Max.X, list.Min.Y))
		r.restore(under, image.Rect(card.Min.X, list.Max.Y, card.Max.X, card.Max.Y+optH))
		r.scrollHints(list, scroll, maxScroll, surface(3))
	}
	r.text(fc.header, p.title, card.Min.X+pad+r.s(6), card.Min.Y+r.s(44), cream)
	return maxScroll
}

// fit shortens text to room pixels, with an ellipsis where it was cut.
func (r *paint) fit(face font.Face, text string, room int) string {
	if text == "" || r.width(face, text) <= room {
		return text
	}
	return clipRunes(text, 1, func(line string) bool { return r.width(face, line) <= room })
}

// dimAll darkens the whole frame, for something drawn over it.
func (r *paint) dimAll(by float64) {
	k := uint32((1 - by) * 256)
	p := r.dst.Pix
	for i := 0; i+3 < len(p); i += 4 {
		p[i] = uint8(uint32(p[i]) * k >> 8)
		p[i+1] = uint8(uint32(p[i+1]) * k >> 8)
		p[i+2] = uint8(uint32(p[i+2]) * k >> 8)
	}
}

func (r *paint) settingRow(card image.Rectangle, top int, row settingRow) {
	fc := r.faces()
	if row.kind == ctlHeading {
		r.text(fc.button, r.fit(fc.button, row.label, card.Dx()-2*r.rowIn()), card.Min.X+r.rowIn(), top+r.s(headHBase)-r.s(12), amber)
		return
	}
	face := fc.label
	if row.bold {
		face = fc.labelBold
	}
	right, cy := card.Max.X-r.s(26), top+r.rowH()/2
	var labelEnd int
	row.label, row.sub, labelEnd = r.rowWords(card, row, face)
	base := top + r.s(40)
	if row.sub == "" {
		r.text(face, row.label, card.Min.X+r.rowIn(), base, cream)
	} else {
		base = top + r.s(30)
		r.text(face, row.label, card.Min.X+r.rowIn(), base, cream)
		r.text(fc.sub, row.sub, card.Min.X+r.rowIn(), top+r.s(53), dim)
	}
	if row.locked {
		x := card.Min.X + r.rowIn() + r.width(face, row.label) + r.s(lockGap)
		r.padlock(x+r.s(lockW)/2, base-r.s(9), dim)
		labelEnd = max(labelEnd, x+r.s(lockW))
	}

	whole := image.Rect(card.Min.X+r.s(8), top, card.Max.X-r.s(8), top+r.rowH())
	// A value never runs into the label: it keeps its start and loses its end to an ellipsis.
	fit := func(text string, rightEdge int) string { return r.fit(fc.value, text, rightEdge-labelEnd-r.s(rowGap)) }
	add := func(r0 image.Rectangle, p part) {
		if row.id != "" {
			r.addZone(zone{r: r0, kind: zoneRow, id: row.id, part: p, label: row.label})
		}
	}
	switch row.kind {
	case ctlValue:
		v := fit(row.value, right)
		r.text(fc.value, v, right-r.width(fc.value, v), cy+r.s(9), dim)
	case ctlToggle:
		x := r.toggle(right, cy, row.on)
		if v := fit(row.value, x-r.s(16)); v != "" {
			r.text(fc.value, v, x-r.s(16)-r.width(fc.value, v), cy+r.s(9), dim)
		}
		if row.rowTap {
			add(whole, partRow)
			add(image.Rect(x-r.s(14), top, card.Max.X-r.s(8), top+r.rowH()), partMain)
			break
		}
		add(whole, partMain) // the whole row flips the switch: a small target otherwise
	case ctlSwatches:
		r.swatchStrip(row, right, cy, top)
	case ctlDays:
		chipW, chipGap := r.dayChip()
		x0 := right - 7*chipW - 6*chipGap
		for i, name := range []string{"S", "M", "T", "W", "T", "F", "S"} {
			b := image.Rect(x0+i*(chipW+chipGap), cy-r.s(21), x0+i*(chipW+chipGap)+chipW, cy+r.s(21))
			ink, rad := cream, r.sf(21)
			if row.days&(1<<i) != 0 {
				r.roundFill(b, rad, shift(amber, 12), shift(amber, -12))
				r.roundHighlight(b, rad)
				ink = onAccent()
			} else {
				r.roundFill(b, rad, surface(5), surface(4))
				r.roundStroke(b, rad, r.sk(), ember)
			}
			r.text(fc.button, name, b.Min.X+(chipW-r.width(fc.button, name))/2, cy+r.s(8), ink)
			if row.id != "" {
				r.addZone(zone{r: image.Rect(b.Min.X-chipGap/2, top, b.Max.X+chipGap/2, top+r.rowH()), kind: zoneRow, id: row.id, part: partDay, opt: i, label: row.label})
			}
		}
	case ctlStepper:
		minus, plus := r.stepper(right, cy, row.value)
		add(minus.Inset(-r.s(6)), partMinus)
		add(plus.Inset(-r.s(6)), partPlus)
	case ctlChoice:
		extra := 0
		if row.button != "" {
			extra = r.width(fc.button, row.button) + r.s(44+12)
		}
		x := r.choice(right, cy, fit(row.value, right-r.s(58)-extra))
		if row.button != "" {
			bx := r.pillButton(x-r.s(12), cy, row.button, btnSecondary)
			add(whole, partMain)
			add(image.Rect(bx-r.s(6), top, x-r.s(6), top+r.rowH()), partExtra)
			break
		}
		add(whole, partMain)
	case ctlButton, ctlDanger:
		style := btnSecondary
		if row.kind == ctlDanger {
			style = btnDanger
		}
		x := r.pillButton(right, cy, row.button, style)
		if v := fit(row.value, x-r.s(16)); v != "" {
			r.text(fc.value, v, x-r.s(16)-r.width(fc.value, v), cy+r.s(9), dim)
		}
		if row.rowTap {
			add(whole, partMain) // the whole row does what its button does
		}
		add(image.Rect(x-r.s(8), top+r.s(4), card.Max.X-r.s(12), top+r.rowH()-r.s(4)), partMain)
	}
}

// rowGap is the space a row keeps between its words and its value or control.
const rowGap = 24

// lockW is the padlock after a locked row's name, and lockGap the space before it.
const (
	lockW   = 14
	lockGap = 10
)

// padlock is the small padlock after the name of a row behind the settings lock, centered on (cx, cy).
func (r *paint) padlock(cx, cy int, col color.RGBA) {
	k := float64(r.s(100)) / 100
	x, y, w := float64(cx), float64(cy), 1.8*k
	r.roundStrokeF(x-6*k, y-1*k, x+6*k, y+7*k, 2*k, w, col)
	r.aaRing(x, y-4*k, 3.6*k, w, math.Pi, 2*math.Pi, col)
	r.aaLine(x-3.6*k, y-4*k, x-3.6*k, y-1*k, w, col)
	r.aaLine(x+3.6*k, y-4*k, x+3.6*k, y-1*k, w, col)
}

// rowWords is a row's label and the line under it as drawn, and where the longer of them ends. They
// keep clear of the control: on a narrow card they give up their ends to an ellipsis rather than run
// under it. The room left to them is the room the value is fitted beside, so a row whose line is cut
// still shows its value whole.
func (r *paint) rowWords(card image.Rectangle, row settingRow, face font.Face) (label, sub string, end int) {
	fc := r.faces()
	right := card.Max.X - r.s(26)
	room := right - r.controlWidth(row) - r.s(rowGap) - (card.Min.X + r.rowIn())
	if row.locked {
		room -= r.s(lockGap + lockW) // the label gives way to its padlock, which follows it
	}
	label, sub = r.fit(face, row.label, room), r.fit(fc.sub, row.sub, room)
	return label, sub, card.Min.X + r.rowIn() + max(r.width(face, label), r.width(fc.sub, sub))
}

// dayChip is the width of a ctlDays row's day and the gap between them: smaller on a round panel,
// whose rows are narrower.
func (r *paint) dayChip() (w, gap int) {
	if r.round {
		return r.s(38), r.s(6)
	}
	return r.s(50), r.s(8)
}

// controlWidth is how much of a row its control takes, from the right, before any value beside it.
func (r *paint) controlWidth(row settingRow) int {
	fc := r.faces()
	switch row.kind {
	case ctlToggle:
		return r.s(66)
	case ctlStepper:
		return r.s(44) + max(r.width(fc.value, row.value), r.s(80)) + r.s(24+44)
	case ctlChoice:
		w := r.width(fc.value, row.value) + r.s(58)
		if row.button != "" {
			w += r.width(fc.button, row.button) + r.s(44+12)
		}
		return w
	case ctlButton, ctlDanger:
		return r.width(fc.button, row.button) + r.s(44)
	case ctlDays:
		w, gap := r.dayChip()
		return 7*w + 6*gap
	}
	return 0
}

// toggle draws a switch ending at right, centered on cy, and returns its left edge.
func (r *paint) toggle(right, cy int, on bool) int {
	half, rad := r.s(17), r.sf(17)
	track := image.Rect(right-r.s(66), cy-half, right, cy+half)
	if on {
		r.roundFill(track, rad, shift(amber, 10), shift(amber, -14))
	} else {
		r.roundFill(track, rad, surface(6), surface(5))
		r.roundStroke(track, rad, r.sk(), ember)
	}
	kx := float64(track.Min.X + half)
	if on {
		kx = float64(track.Max.X - half)
	}
	kh := r.s(13)
	knob := image.Rect(int(kx)-kh, cy-kh, int(kx)+kh, cy+kh)
	r.roundShadow(knob, r.sf(13), r.sf(5), r.s(2), 0.45)
	r.roundFill(knob, r.sf(13), shift(cream, 12), shift(cream, -6))
	return track.Min.X
}

// stepper draws − value + ending at right, and returns where its two buttons are.
func (r *paint) stepper(right, cy int, value string) (minusAt, plusAt image.Rectangle) {
	fc := r.faces()
	bw, half, gap := r.s(44), r.s(20), r.s(24)
	plus := image.Rect(right-bw, cy-half, right, cy+half)
	vw := max(r.width(fc.value, value), r.s(80))
	minus := image.Rect(plus.Min.X-vw-gap-bw, cy-half, plus.Min.X-vw-gap, cy+half)
	for _, b := range []image.Rectangle{minus, plus} {
		r.roundShadow(b, r.sf(12), r.sf(6), r.s(2), shadowAlpha()*0.7)
		r.roundFill(b, r.sf(12), surface(7), surface(5))
		r.roundHighlight(b, r.sf(12))
	}
	k := r.sk()
	mx, px := float64(minus.Min.X+r.s(22)), float64(plus.Min.X+r.s(22))
	r.aaLine(mx-k*8, float64(cy), mx+k*8, float64(cy), k*2.6, cream)
	r.aaLine(px-k*8, float64(cy), px+k*8, float64(cy), k*2.6, cream)
	r.aaLine(px, float64(cy)-k*8, px, float64(cy)+k*8, k*2.6, cream)
	r.text(fc.value, value, minus.Max.X+r.s(12)+(vw-r.width(fc.value, value))/2, cy+r.s(9), cream)
	return minus, plus
}

// choice draws a value in a pill with a chevron, the way to a list of options; returns its left edge.
func (r *paint) choice(right, cy int, value string) int {
	fc := r.faces()
	w := r.width(fc.value, value) + r.s(58)
	pill := image.Rect(right-w, cy-r.s(20), right, cy+r.s(20))
	k := r.sk()
	r.roundFill(pill, r.sf(20), surface(5), surface(4))
	r.roundStroke(pill, r.sf(20), k, ember)
	r.text(fc.value, value, pill.Min.X+r.s(20), cy+r.s(9), cream)
	x, y := float64(pill.Max.X-r.s(22)), float64(cy)
	r.aaLine(x-k*4, y-k*7, x+k*3, y, k*2.4, dim)
	r.aaLine(x+k*3, y, x-k*4, y+k*7, k*2.4, dim)
	return pill.Min.X
}

type buttonStyle int

const (
	btnPrimary   buttonStyle = iota // the page's main action, filled in the accent
	btnSecondary                    // a row's action, quiet
	btnDanger                       // an action to think twice about
)

// danger is the color of an action to think twice about, whatever the theme.
var danger = color.RGBA{0xe5, 0x48, 0x4d, 0xff}

// roundButton fills a rounded button in one color, lit and shadowed the way every raised thing on
// these screens is. Separate from buttonFace because a few answers carry a color of their own that
// means something — the phone's red and green — rather than taking the theme's accent.
func (r *paint) roundButton(b image.Rectangle, rad float64, fill color.RGBA) {
	r.roundShadow(b, rad, r.sf(8), r.s(3), shadowAlpha()*0.8)
	r.roundFill(b, rad, shift(fill, 14), shift(fill, -14))
	r.roundHighlight(b, rad)
}

// buttonFace draws a button's shape and returns the color its label should be drawn in.
//
// Size and radius are the caller's, because a button is the same thing whether it is the pill on a
// settings row or one of the two large answers on the setup page. Drawing them from one place is
// what keeps them looking like the same control: the Show's answers used to be square beveled
// boxes and looked like they belonged to another program.
func (r *paint) buttonFace(b image.Rectangle, rad float64, style buttonStyle) color.RGBA {
	switch style {
	case btnPrimary:
		r.roundButton(b, rad, amber)
		return onAccent()
	case btnSecondary:
		r.roundShadow(b, rad, r.sf(6), r.s(2), shadowAlpha()*0.5)
		r.roundFill(b, rad, surface(6), surface(4))
		r.roundStroke(b, rad, r.sk(), ember)
		r.roundHighlight(b, rad)
	case btnDanger:
		r.roundFill(b, rad, surface(4), surface(3))
		r.roundStroke(b, rad, r.sk()*1.6, danger)
		return danger
	}
	return cream
}

// pillButton draws an action ending at right and returns its left edge.
func (r *paint) pillButton(right, cy int, label string, style buttonStyle) int {
	fc := r.faces()
	w := r.width(fc.button, label) + r.s(44)
	half := r.s(20)
	b := image.Rect(right-w, cy-half, right, cy+half)
	fg := r.buttonFace(b, float64(half), style)
	r.text(fc.button, label, b.Min.X+r.s(22), cy+r.s(8), fg)
	return b.Min.X
}

// labelButton draws the program's button in b, a pill as tall as the box, with label centered in it.
// For a box a page has measured for itself; pillButton measures its own.
func (r *paint) labelButton(b image.Rectangle, label string, style buttonStyle) {
	r.buttonLabel(b, label, r.buttonFace(b, float64(b.Dy())/2, style))
}

// buttonLabel writes label centered in b in the sheet's button face, on pillButton's baseline.
func (r *paint) buttonLabel(b image.Rectangle, label string, col color.RGBA) {
	face := r.faces().button
	r.text(face, label, b.Min.X+(b.Dx()-r.width(face, label))/2, b.Min.Y+b.Dy()/2+r.s(8), col)
}

// icon is a category's line drawing, about 26 Show 5 pixels across (k scales it to the panel),
// centered on (cx, cy).
func (r *paint) icon(c category, cx, cy int, col color.RGBA) {
	x, y, k := float64(cx), float64(cy), r.sk()
	w := k * 2.6
	switch c {
	case catDisplay: // the sun of brightness
		r.aaRing(x, y, k*5.5, w, 0, 2*math.Pi, col)
		for i := range 8 {
			a := float64(i) * math.Pi / 4
			r.aaLine(x+k*9*math.Cos(a), y+k*9*math.Sin(a), x+k*12.5*math.Cos(a), y+k*12.5*math.Sin(a), w, col)
		}
	case catSound: // a microphone
		r.roundStrokeF(x-k*5, y-k*12, x+k*5, y+k*4, k*5, w, col)
		r.aaRing(x, y-k*2, k*9, w, 0.12*math.Pi, 0.88*math.Pi, col)
		r.aaLine(x, y+k*7, x, y+k*12, w, col)
		r.aaLine(x-k*5, y+k*12.5, x+k*5, y+k*12.5, w, col)
	case catAlarms: // a clock
		r.aaRing(x, y, k*11.5, w, 0, 2*math.Pi, col)
		r.aaLine(x, y, x, y-k*6.5, w, col)
		r.aaLine(x, y, x+k*5, y+k*2, w, col)
	case catConnections: // Wi-Fi
		for _, rad := range []float64{5, 10.5, 16} {
			r.aaRing(x, y+k*9, k*rad, w, -0.75*math.Pi, -0.25*math.Pi, col)
		}
		r.aaDisc(x, y+k*9, k*2.4, col)
	case catSecurity: // a padlock
		r.roundStrokeF(x-k*10, y-k*2, x+k*10, y+k*12, k*3.5, w, col)
		r.aaRing(x, y-k*6, k*6, w, math.Pi, 2*math.Pi, col)
		r.aaLine(x-k*6, y-k*6, x-k*6, y-k*2, w, col)
		r.aaLine(x+k*6, y-k*6, x+k*6, y-k*2, w, col)
	case catGeneral: // sliders
		for i, knob := range []float64{4, -5, 2} {
			ly := y - k*8 + k*float64(i)*8
			r.aaLine(x-k*12, ly, x+k*12, ly, w-k*0.4, col)
			r.aaDisc(x+k*knob, ly, k*3.4, col)
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

// onAccent is text that reads on the accent: the ground on a light accent, the text color on a dark one.
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
func (r *paint) blendAt(x, y int, c color.RGBA, a float64) {
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

func (r *paint) vgradient(b image.Rectangle, top, bottom color.RGBA) {
	for y := b.Min.Y; y < b.Max.Y; y++ {
		r.span(y, b.Min.X, b.Max.X, lerp(top, bottom, float64(y-b.Min.Y)/float64(max(b.Dy()-1, 1))))
	}
}

// span paints x0 up to x1 on row y in c, solid.
func (r *paint) span(y, x0, x1 int, c color.RGBA) {
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
func (r *paint) roundFill(b image.Rectangle, rad float64, top, bottom color.RGBA) {
	x0, y0, x1, y1 := rectF(b)
	// Only the corners are worked out a pixel at a time; between them each row is solid. The rows
	// just outside the top and bottom edges are left alone: no pixel center there is covered.
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
func (r *paint) roundStroke(b image.Rectangle, rad, w float64, c color.RGBA) {
	x0, y0, x1, y1 := rectF(b)
	r.roundStrokeF(x0, y0, x1, y1, rad, w, c)
}

func (r *paint) roundStrokeF(x0, y0, x1, y1, rad, w float64, c color.RGBA) {
	for y := int(y0) - 1; y <= int(y1)+1; y++ {
		for x := int(x0) - 1; x <= int(x1)+1; x++ {
			d := rrDist(float64(x)+0.5, float64(y)+0.5, x0, y0, x1, y1, rad)
			r.blendAt(x, y, c, clamp01(0.5-(math.Abs(d+w/2)-w/2)))
		}
	}
}

// roundHighlight is the light catching a raised surface's top edge.
func (r *paint) roundHighlight(b image.Rectangle, rad float64) {
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
func (r *paint) roundShadow(b image.Rectangle, rad, blur float64, dy int, alpha float64) {
	r.roundShadowIn(b, rad, blur, dy, alpha, r.dst.Rect, nil)
}

// roundShadowIn is roundShadow drawn only inside clip, and where mask is given, only as much as it
// says at each pixel (0 to 1): the shadow laid on one thing drawn after it.
func (r *paint) roundShadowIn(b image.Rectangle, rad, blur float64, dy int, alpha float64, clip image.Rectangle, mask func(x, y int) float64) {
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

// rule is a thin horizontal line in the rules color.
func (r *paint) rule(x0, x1, y int, alpha float64) {
	for x := x0; x < x1; x++ {
		r.blendAt(x, y, ember, alpha)
	}
}

// aaLine is a stroke of width w with round ends.
func (r *paint) aaLine(x0, y0, x1, y1, w float64, c color.RGBA) {
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
func (r *paint) aaRing(cx, cy, rad, w, a0, a1 float64, c color.RGBA) {
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

func (r *paint) aaDisc(cx, cy, rad float64, c color.RGBA) {
	for y := int(cy - rad - 1); y <= int(cy+rad+1); y++ {
		for x := int(cx - rad - 1); x <= int(cx+rad+1); x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) - rad
			r.blendAt(x, y, c, clamp01(0.5-d))
		}
	}
}
