//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/lib/mdi"
)

// The Custom card plugin (github.com/stofle/tessera-plugin-custom-card 0.7.0, MIT), drawn as its
// custom_card.cpp lays its parts out: a template sensor's attribute `elements` lists the parts, one text
// each, fields split by "|" (kind, value, unit, label, color, icon, and a bar's or a ring's least and most):
//
//	value|21.4|°C|Temperature|blue|thermometer
//	bar|820|ppm|CO2|yellow|molecule-co2|400|2000
//	text|Door open||||door-open
//	row|20.8|°C|Kitchen
//	ring|46|%|Humidity|green|water-percent|0|100
//	image|5358|count_scanned|Scanned
//
// The card takes the largest face for its values that fits, tries the parts side by side (a tile one row
// high and two or more wide), stacked, and stacked two to a row, and keeps the tile's name and the labels
// only where they cost the values nothing. A part's color marks it (its icon, a dot, its bar, its ring),
// never its words. An image stands as its mark: this screen takes no plugin pictures. Its words are the
// plugin's (English).

const (
	ccMaxParts = 8
	ccMaxInRow = 4
)

type ccKind int

const (
	ccValue ccKind = iota
	ccBar
	ccText
	ccRow
	ccRing
	ccImage
)

// ccPart is one part of a card.
type ccPart struct {
	kind               ccKind
	value, unit, label string
	color              color.RGBA // zero for the card's own paint
	colored            bool
	icon               string // an mdi name, "" for none
	badge              string // a badge's words, "" for none
	min, max           float64
}

// valued is a value, or an image standing as its mark, both laid out as values.
func (p ccPart) valued() bool { return p.kind == ccValue || p.kind == ccImage }

func ccNumber(s string) (float64, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", "."))
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil && !math.IsInf(v, 0) && !math.IsNaN(v)
}

// ccParse is a part from its text, false for a kind the card does not know.
func ccParse(line string) (ccPart, bool) {
	f := strings.Split(line, "|")
	for i := range f {
		f[i] = strings.TrimSpace(f[i])
	}
	at := func(i int) string {
		if i < len(f) {
			return f[i]
		}
		return ""
	}
	p := ccPart{min: 0, max: 100}
	switch f[0] {
	case "value":
		p.kind = ccValue
	case "bar":
		p.kind = ccBar
	case "text":
		p.kind = ccText
	case "row":
		p.kind = ccRow
	case "ring":
		p.kind = ccRing
	case "image":
		p.kind = ccImage // its mark as a value: no pictures here
	default:
		return p, false
	}
	p.value = at(1)
	if p.kind != ccImage {
		p.unit = at(2)
	}
	p.label = at(3)
	if c, ok := swatches[at(4)]; ok {
		p.color, p.colored = c, true
	}
	if icon := at(5); strings.HasPrefix(icon, "#") {
		p.badge = icon[1:]
		if len(p.badge) > 8 {
			p.badge = p.badge[:8]
		}
	} else if _, ok := mdi.Rune(icon); ok && icon != "" {
		p.icon = strings.TrimPrefix(icon, "mdi:")
	}
	if n, ok := ccNumber(at(6)); ok && p.kind != ccImage {
		p.min = n
	}
	if n, ok := ccNumber(at(7)); ok {
		p.max = n
	}
	return p, true
}

// ccParts are a card's parts from what the add-on sends of its sensor (its state, name and attributes), and
// the note it shows instead where there are none.
func ccParts(data map[string]any) (parts []ccPart, note string) {
	if wait, _ := data["wait"].(string); wait != "" {
		if wait == "wrong_entity" {
			return nil, "Choose a sensor that describes a card"
		}
		return nil, "Waiting for the sensor"
	}
	attrs, _ := data["attributes"].(map[string]any)
	state, _ := data["state"].(string)
	switch e := attrs["elements"].(type) {
	case []any:
		for _, line := range e {
			if s, ok := line.(string); ok && len(parts) < ccMaxParts {
				if p, ok := ccParse(s); ok {
					parts = append(parts, p)
				}
			}
		}
		if len(parts) == 0 {
			return nil, "No parts in elements"
		}
		return parts, ""
	case string:
		if p, ok := ccParse(e); ok {
			return []ccPart{p}, ""
		}
		return nil, "No parts in elements"
	}
	if state == "" {
		return nil, "Choose a sensor in the tile's settings"
	}
	unit, _ := attrs["unit_of_measurement"].(string)
	return []ccPart{{kind: ccValue, value: state, unit: unit, min: 0, max: 100}}, ""
}

// The faces of the plugin's set, by the rank Tessera gives them: DIGITS the largest, BODY the smallest.
type ccFont int

const (
	ccDigits ccFont = iota
	ccValueF
	ccHeadline
	ccTitle
	ccBodyLarge
	ccBody
)

var ccFonts = []ccFont{ccDigits, ccValueF, ccHeadline, ccTitle, ccBodyLarge, ccBody}

// ccLine is parts side by side in columns, or a bar, a text or a row alone across the width. bar is a bar
// that has nothing to say but its number (no label, no icon) standing under the line of values before it,
// its number at the right of theirs, -1 for none: the values keep the room a line of its own would take.
// This screen's own, not the plugin's.
type ccLine struct {
	first, count int
	across       bool
	bar          int
}

// ccDraw lays out and draws one card.
type ccDraw struct {
	r          *paint
	parts      []ccPart
	w, h       int
	flat       bool
	colored    bool
	perRow     int
	wholeTexts bool
	strictRows bool
	rings      bool
	words      ccFont
	ringMin    int
	ringFace   ccFont
	arranged   [3][]ccLine
	faces      map[ccFont]font.Face
}

func (d *ccDraw) face(f ccFont) font.Face {
	if fc, ok := d.faces[f]; ok {
		return fc
	}
	var fc font.Face
	switch f {
	case ccDigits:
		fc = d.r.textFace(false, 60)
	case ccValueF:
		fc = d.r.textFace(false, 42)
	case ccHeadline:
		fc = d.r.textFace(false, 30)
	case ccTitle:
		fc = d.r.textFace(true, 21)
	case ccBodyLarge:
		fc = d.r.textFace(false, 19)
	default:
		fc = d.r.textFace(false, 16)
	}
	d.faces[f] = fc
	return fc
}

func (d *ccDraw) lh(f ccFont) int           { return d.face(f).Metrics().Height.Ceil() }
func (d *ccDraw) tw(s string, f ccFont) int { return d.r.width(d.face(f), s) }
func (d *ccDraw) px(n int) int              { return d.r.s(n) }

// Icon sizes: the small one beside words, the tile's beside a value as tall.
const (
	ccIconSmall = 18
	ccIcon      = 28
)

// covers is whether the digits' face has every character of s: digits and the signs a number has.
func ccCovers(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789.,:-+% ", c) {
			return false
		}
	}
	return true
}

func ccShape(s string) string {
	return strings.Map(func(c rune) rune {
		if c >= '1' && c <= '9' {
			return '0'
		}
		return c
	}, s)
}

func (d *ccDraw) stacked(inRow int) []ccLine {
	var out []ccLine
	for i, p := range d.parts {
		value := p.valued() || p.kind == ccRing
		last := len(out) - 1
		switch {
		case value && last >= 0 && !out[last].across && out[last].bar < 0 && out[last].count < inRow:
			out[last].count++
		case ccBare(p) && last >= 0 && !out[last].across && out[last].bar < 0 && !d.ringsIn(out[last]):
			out[last].bar = i
		default:
			out = append(out, ccLine{i, 1, !value, -1})
		}
	}
	return out
}

// ccBare is a bar with nothing but its number and its color: no label, no icon, no badge.
func ccBare(p ccPart) bool { return p.kind == ccBar && p.label == "" && p.icon == "" && p.badge == "" }

// ccBarOnly is a bare bar without a unit as well: the bar alone, its number not written.
func ccBarOnly(p ccPart) bool { return ccBare(p) && p.unit == "" }

// ringsIn is whether a line has rings drawn as rings.
func (d *ccDraw) ringsIn(l ccLine) bool {
	for i := l.first; i < l.first+l.count; i++ {
		if d.meter(d.parts[i]) {
			return true
		}
	}
	return false
}

func (d *ccDraw) sideBySide() []ccLine {
	if d.flat && len(d.parts) > 0 && len(d.parts) <= ccMaxInRow {
		return []ccLine{{0, len(d.parts), false, -1}}
	}
	return nil
}

func ccColumns(all []ccLine) int {
	most := 1
	for _, l := range all {
		if !l.across {
			most = max(most, l.count)
		}
	}
	return most
}

func (d *ccDraw) digitsHold(all []ccLine) bool {
	for _, l := range all {
		if l.across {
			continue
		}
		for i := l.first; i < l.first+l.count; i++ {
			if d.parts[i].kind != ccText && !ccCovers(d.parts[i].value) {
				return false
			}
		}
	}
	return true
}

func (d *ccDraw) wordsRow() int { return max(d.lh(ccBody), d.px(ccIconSmall)) }

func (d *ccDraw) ringRow(l ccLine) int {
	for i := l.first; i < l.first+l.count; i++ {
		if d.parts[i].icon != "" {
			return d.wordsRow()
		}
	}
	return d.lh(ccBody)
}

func (d *ccDraw) wordsFace(f ccFont) ccFont {
	if f == ccBody {
		return ccBody
	}
	return d.words
}

func (d *ccDraw) hasMark(p ccPart) bool { return p.icon != "" || p.colored || p.badge != "" }

func (d *ccDraw) textLines(p ccPart, f ccFont) int {
	if p.kind != ccText || !d.wholeTexts {
		return 1
	}
	mark := 0
	if d.hasMark(p) {
		mark = d.markWidth(p, ccIconSmall) + d.px(6)
	}
	if mark+d.tw(p.value, d.wordsFace(f)) > d.w {
		return 2
	}
	return 1
}

func (d *ccDraw) textRow(p ccPart, f ccFont) int {
	words := d.lh(d.wordsFace(f)) * d.textLines(p, f)
	if d.hasMark(p) {
		return max(words, d.px(ccIconSmall))
	}
	return words
}

func (d *ccDraw) meter(p ccPart) bool { return p.kind == ccRing && d.rings }

func (d *ccDraw) ringThick(dia int) int { return max(d.px(4), dia/14) }

func (d *ccDraw) naturalDia(p ccPart, f ccFont) int {
	w := float64(d.tw(ccShape(p.value), f) + d.unitWidth(p))
	g := 0.62 * float64(d.lh(f))
	inner := int(math.Ceil(math.Sqrt(w*w+g*g))) + 2*d.px(4)
	return max(d.px(48), inner+2*max(d.px(4), inner/12))
}

func (d *ccDraw) innerFace(f ccFont) ccFont {
	if d.ringFace < f {
		return d.ringFace
	}
	return f
}

func (d *ccDraw) ringWordsAt(dia, row int, f ccFont) int {
	lh := d.lh(d.innerFace(f))
	return min(dia, max(3*dia/4, dia-row, (dia-lh)/2+lh))
}

func (d *ccDraw) besideValues(l ccLine) bool {
	for i := l.first; i < l.first+l.count; i++ {
		if d.parts[i].kind != ccRing {
			return true
		}
	}
	return false
}

func (d *ccDraw) lineRingDia(l ccLine, f ccFont) int {
	beside := d.besideValues(l)
	most := 3 * (d.wordsRow() + d.lh(f)) / 2
	dia := 0
	for i := l.first; i < l.first+l.count; i++ {
		if !d.meter(d.parts[i]) {
			continue
		}
		least := d.naturalDia(d.parts[i], ccTitle)
		if beside {
			dia = max(dia, max(least, min(d.ringMin, most)))
		} else {
			dia = max(dia, max(d.ringMin, d.naturalDia(d.parts[i], f)))
		}
	}
	return dia
}

func (d *ccDraw) ringFaceFor(p ccPart, dia int, f ccFont) ccFont {
	for _, g := range []ccFont{ccDigits, ccValueF, ccHeadline, ccTitle, ccBodyLarge} {
		if g < f || (g == ccDigits && !ccCovers(p.value)) {
			continue
		}
		if d.naturalDia(p, g) <= dia {
			return g
		}
	}
	return ccBody
}

func (d *ccDraw) barThickness() int { return d.px(10) }
func (d *ccDraw) dotSize() int      { return d.px(12) }
func (d *ccDraw) gap() int          { return d.px(8) }
func (d *ccDraw) columnGap() int    { return d.px(16) }
func (d *ccDraw) columnWidth(n int) int {
	return (d.w - (n-1)*d.columnGap()) / n
}

func (d *ccDraw) badgePad() int           { return d.px(8) }
func (d *ccDraw) badgeWidth(p ccPart) int { return d.tw(p.badge, ccTitle) + 2*d.badgePad() }
func (d *ccDraw) hasWords(l ccLine) bool {
	for i := l.first; i < l.first+l.count; i++ {
		if d.parts[i].label != "" || d.hasMark(d.parts[i]) {
			return true
		}
	}
	return false
}

func (d *ccDraw) inlineIcon(f ccFont) int {
	if d.px(ccIcon) <= d.lh(f) {
		return ccIcon
	}
	return ccIconSmall
}

func (d *ccDraw) markWidth(p ccPart, icon int) int {
	switch {
	case p.badge != "":
		return d.badgeWidth(p)
	case p.icon != "":
		return d.px(icon)
	case p.colored && !ccBare(p):
		return d.dotSize()
	}
	return 0
}

func (d *ccDraw) unitWidth(p ccPart) int {
	if p.unit == "" {
		return 0
	}
	return d.px(3) + d.tw(p.unit, ccBody)
}

func (d *ccDraw) lineHeight(l ccLine, f ccFont, labels bool) int {
	first := d.parts[l.first]
	if l.across {
		if ccBarOnly(first) {
			return d.barThickness()
		}
		if first.kind == ccBar {
			return d.lh(d.wordsFace(f)) + d.px(4) + d.barThickness()
		}
		return d.textRow(first, f)
	}
	words := 0
	if labels && d.hasWords(l) {
		words = d.wordsRow()
	}
	h := 0
	for i := l.first; i < l.first+l.count; i++ {
		p := d.parts[i]
		switch p.kind {
		case ccValue, ccImage, ccRow:
			h = max(h, words+d.lh(f))
		case ccRing:
			if d.meter(p) {
				dia := d.lineRingDia(l, f)
				if d.hasWords(l) {
					h = max(h, max(dia, d.ringWordsAt(dia, d.ringRow(l), f)+d.ringRow(l)))
				} else {
					h = max(h, dia)
				}
			} else {
				h = max(h, words+d.lh(f))
			}
		case ccBar:
			if ccBarOnly(p) {
				h = max(h, d.barThickness())
				continue
			}
			h = max(h, words+d.lh(f)+d.px(4)+d.barThickness())
		default:
			h = max(h, d.textRow(p, f))
		}
	}
	if l.bar >= 0 {
		h += d.px(4) + d.barThickness()
	}
	return h
}

func (d *ccDraw) fitsAcross(all []ccLine, f ccFont, labels bool) bool {
	column := d.columnWidth(ccColumns(all))
	for _, l := range all {
		if l.across {
			p := d.parts[l.first]
			if p.kind == ccRow {
				need := d.tw(p.value, d.wordsFace(f)) + d.unitWidth(p)
				if d.hasMark(p) {
					need += d.markWidth(p, ccIconSmall) + d.px(6)
				}
				if d.strictRows {
					need += d.px(8) + d.tw(p.label, d.wordsFace(f))
				}
				if need > d.w {
					return false
				}
			}
			continue
		}
		for i := l.first; i < l.first+l.count; i++ {
			p := d.parts[i]
			if d.meter(p) {
				if d.lineRingDia(l, f) > column {
					return false
				}
				continue
			}
			if p.kind == ccText {
				mark := d.markWidth(p, ccIconSmall)
				if d.hasMark(p) {
					mark += d.px(6)
				}
				if (d.wholeTexts || d.strictRows) && l.count > 1 && mark+d.tw(p.value, d.wordsFace(f)) > column {
					return false
				}
				continue
			}
			mark := 0
			if !labels && d.hasMark(p) {
				mark = d.markWidth(p, d.inlineIcon(f)) + d.px(6)
			}
			beside := 0
			if l.bar >= 0 && i == l.first+l.count-1 && !ccBarOnly(d.parts[l.bar]) {
				b := d.parts[l.bar]
				beside = d.px(12) + d.tw(ccShape(b.value), d.wordsFace(f)) + d.unitWidth(b)
			}
			if mark+d.tw(ccShape(p.value), f)+d.unitWidth(p)+beside > column {
				return false
			}
		}
	}
	return true
}

func (d *ccDraw) fits(all []ccLine, f ccFont, head, labels bool) bool {
	if !d.fitsAcross(all, f, labels) {
		return false
	}
	used := 0
	if head {
		used = d.lh(ccTitle) + d.gap()
	}
	for _, l := range all {
		used += d.lineHeight(l, f, labels)
	}
	used += d.gap() * (len(all) - 1)
	return used <= d.h
}

func (d *ccDraw) arrange() {
	ring, value := false, false
	for _, p := range d.parts {
		ring = ring || (p.kind == ccRing && d.rings)
		value = value || p.valued() || p.kind == ccBar
	}
	d.arranged = [3][]ccLine{}
	if d.perRow == 0 || d.perRow >= ccMaxInRow {
		d.arranged[0] = d.sideBySide()
	}
	in := ccMaxInRow
	if d.perRow != 0 {
		in = d.perRow
	}
	d.arranged[1] = d.stacked(in)
	if d.perRow == 0 && !(ring && value) {
		d.arranged[2] = d.stacked(2)
	}
}

type ccPick struct {
	found        bool
	face         ccFont
	head, labels bool
	whole        bool
	index        int
}

func (d *ccDraw) search(rings, named, unmarked bool) ccPick {
	d.rings = rings
	d.arrange()
	var best ccPick
	for _, labelled := range []bool{unmarked, !unmarked} {
		var pick ccPick
		for _, whole := range []bool{true, false} {
			d.wholeTexts = whole
			for index := range d.arranged {
				all := d.arranged[index]
				if len(all) == 0 {
					continue
				}
				for _, head := range []bool{named, false} {
					for _, f := range ccFonts {
						if f == ccDigits && !d.digitsHold(all) {
							continue
						}
						if !d.fits(all, f, head, labelled) {
							continue
						}
						if !pick.found || f < pick.face {
							pick = ccPick{true, f, head, labelled || d.fits(all, f, head, true), whole, index}
						}
						break
					}
					if pick.found {
						break
					}
				}
			}
			if pick.found {
				break
			}
		}
		if pick.found && (!best.found || (pick.face < best.face && (pick.whole || !best.whole) && (pick.head || !best.head))) {
			best = pick
		}
		if best.found && unmarked {
			return best
		}
	}
	return best
}

// ccTheme is the paint of a card: the look's words, the card, its track and the accent.
type ccTheme struct {
	ink, muted, card, track, accent color.RGBA
	colored                         bool // the tile's Values in their color
}

// customCard draws a Custom card's parts into b: name heads them where shown.
func (r *paint) customCard(b image.Rectangle, name string, parts []ccPart, note string, flat bool, o map[string]any, pal dashPal, card color.RGBA) {
	th := ccTheme{ink: pal.text, muted: pal.sub, card: card, track: lerp(card, pal.sub, 0.22), accent: pal.accent}
	if v, ok := o["coloured_values"].(bool); ok {
		th.colored = v
	}
	d := &ccDraw{r: r, parts: parts, w: b.Dx(), h: b.Dy(), flat: flat, faces: map[ccFont]font.Face{}}
	switch o["per_row"] {
	case "all":
		d.perRow = ccMaxInRow
	case "two":
		d.perRow = 2
	}
	if show, ok := o["show_name"].(bool); ok && !show {
		name = ""
	}
	if note != "" || len(parts) == 0 {
		f := d.face(ccBody)
		r.text(f, r.fit(f, note, b.Dx()), b.Min.X+(b.Dx()-r.width(f, r.fit(f, note, b.Dx())))/2, b.Min.Y+(b.Dy()+f.Metrics().Ascent.Ceil())/2, th.muted)
		return
	}
	d.words, d.ringMin, d.ringFace = ccBodyLarge, 0, ccBody
	named := name != ""
	unmarked, rings, values := false, false, false
	for _, p := range parts {
		marked := d.hasMark(p)
		if p.kind == ccRing {
			marked = p.icon != ""
		}
		if p.kind != ccText && p.label != "" && !marked {
			unmarked = true
		}
		rings = rings || p.kind == ccRing
		values = values || p.valued() || p.kind == ccBar
	}
	pick := d.search(true, named, unmarked)
	ringsChosen := true
	nameless := named && pick.found && !pick.head
	if rings && (!pick.found || values || nameless) {
		plain := d.search(false, named, unmarked)
		if plain.found && (!pick.found || plain.face < pick.face || (nameless && plain.head)) {
			pick, ringsChosen = plain, false
		}
	}
	if !pick.found {
		ringsChosen = false
	}
	d.rings = ringsChosen
	d.arrange()
	face := pick.face
	if !pick.found {
		face = ccBody
	}
	lines := d.arranged[pick.index]
	if !pick.found {
		lines = nil
	}
	d.wholeTexts = pick.whole
	if pick.found && (!values || ccHeadline >= face) {
		d.strictRows = true
		d.words = ccHeadline
		if !d.fits(lines, face, pick.head, pick.labels) {
			d.words = ccBodyLarge
		}
		d.strictRows = false
	}
	meters := false
	for _, p := range parts {
		meters = meters || d.meter(p)
	}
	if pick.found && meters {
		lo, hi := 0, d.w
		for lo < hi {
			d.ringMin = (lo + hi + 1) / 2
			if d.fits(lines, face, pick.head, pick.labels) {
				lo = d.ringMin
			} else {
				hi = d.ringMin - 1
			}
		}
		d.ringMin = lo
		digits := true
		for _, p := range parts {
			if d.meter(p) && !ccCovers(p.value) {
				digits = false
			}
		}
		for _, f := range ccFonts {
			if f >= face || (f == ccDigits && !digits) {
				continue
			}
			held := true
			for _, l := range lines {
				for i := l.first; i < l.first+l.count; i++ {
					if d.meter(parts[i]) && d.naturalDia(parts[i], f) > d.lineRingDia(l, face) {
						held = false
					}
				}
			}
			d.ringFace = f
			if held && d.fits(lines, face, pick.head, pick.labels) {
				break
			}
			d.ringFace = ccBody
		}
	}
	if !pick.found {
		for _, in := range []int{2, 1} {
			lines = d.stacked(in)
			if d.fitsAcross(lines, face, false) {
				break
			}
		}
	}

	y := 0
	if pick.head {
		f := d.face(ccTitle)
		r.text(f, r.fit(f, name, d.w), b.Min.X, b.Min.Y+f.Metrics().Ascent.Ceil(), th.ink)
		y = d.lh(ccTitle) + d.gap()
	}
	content := 0
	for _, l := range lines {
		content += d.lineHeight(l, face, pick.labels)
	}
	spare := max(0, d.h-y-content)
	n := len(lines)
	cols := ccColumns(lines)
	between := 0
	if n > 1 {
		between = max(d.gap(), spare/(n+1))
	}
	y += max(0, (spare-between*(n-1))/2)
	for _, l := range lines {
		h := d.lineHeight(l, face, pick.labels)
		if !pick.found && y+h > d.h {
			break
		}
		d.place(b.Min, l, cols, face, pick.labels, y, th)
		y += h + between
	}
}

// markColor is a part's mark: its color, or the look's muted words without one.
func (th ccTheme) markColor(p ccPart) color.RGBA {
	if p.colored {
		return p.color
	}
	return th.muted
}

// The drawing, at o (the card's top left) plus the layout's own x and y.

func (d *ccDraw) text(o image.Point, f ccFont, s string, x, top, room int, c color.RGBA) {
	fc := d.face(f)
	d.r.text(fc, d.r.fit(fc, s, room), o.X+x, o.Y+top+fc.Metrics().Ascent.Ceil(), c)
}

// placeMark is a part's mark at (x, y), in the middle of a row h high; its width.
func (d *ccDraw) placeMark(o image.Point, p ccPart, x, y, h, icon int, th ccTheme) int {
	switch {
	case p.badge != "":
		w := d.badgeWidth(p)
		bh := min(h, d.lh(ccTitle)+d.px(6))
		box := p.color
		if !p.colored {
			box = th.accent
		}
		rect := image.Rect(o.X+x, o.Y+y+(h-bh)/2, o.X+x+w, o.Y+y+(h-bh)/2+bh)
		d.r.roundFill(rect, float64(d.px(6)), box, box)
		words := th.card
		if ccContrast(th.ink, box) > ccContrast(words, box) {
			words = th.ink
		}
		fc := d.face(ccTitle)
		d.r.text(fc, p.badge, rect.Min.X+(w-d.r.width(fc, p.badge))/2, rect.Min.Y+(bh-fc.Metrics().Height.Ceil())/2+fc.Metrics().Ascent.Ceil(), words)
		return w
	case p.icon != "":
		side := d.px(icon)
		d.r.mdiIconIn(p.icon, image.Rect(o.X+x, o.Y+y+(h-side)/2, o.X+x+side, o.Y+y+(h-side)/2+side), icon, th.markColor(p))
		return side
	case p.colored && !ccBare(p): // a bare bar's color is in its bar
		s := d.dotSize()
		d.r.aaDisc(float64(o.X+x)+float64(s)/2, float64(o.Y+y)+float64(h)/2, float64(s)/2, p.color)
		return s
	}
	return 0
}

func (d *ccDraw) placeWords(o image.Point, p ccPart, x, y, h, room int, f ccFont, text string, lines int, c color.RGBA, th ccTheme) {
	mark := d.placeMark(o, p, x, y, min(h, d.lh(f)), ccIconSmall, th)
	left := x
	if mark > 0 {
		left += mark + d.px(6)
	}
	if text == "" || x+room-left <= 0 {
		return
	}
	fc := d.face(f)
	if lines > 1 {
		ls := d.r.wrapLines(fc, text, x+room-left)
		if len(ls) > lines {
			ls[lines-1] = d.r.fit(fc, strings.Join(ls[lines-1:], " "), x+room-left)
			ls = ls[:lines]
		}
		top := y + (h-d.lh(f)*len(ls))/2
		for _, l := range ls {
			d.text(o, f, l, left, top, x+room-left, c)
			top += d.lh(f)
		}
		return
	}
	d.text(o, f, text, left, y+(h-d.lh(f))/2, x+room-left, c)
}

func (d *ccDraw) valueColor(p ccPart, th ccTheme) color.RGBA {
	if th.colored && p.colored && p.kind != ccText {
		return p.color
	}
	return th.ink
}

// placeValue is a value and its unit after it, on the value's baseline.
func (d *ccDraw) placeValue(o image.Point, p ccPart, f ccFont, x, y, room int, th ccTheme) {
	uw := d.unitWidth(p)
	fc := d.face(f)
	v := d.r.fit(fc, p.value, max(1, room-uw))
	base := o.Y + y + fc.Metrics().Ascent.Ceil()
	d.r.text(fc, v, o.X+x, base, d.valueColor(p, th))
	if p.unit != "" {
		d.r.text(d.face(ccBody), p.unit, o.X+x+d.r.width(fc, v)+d.px(3), base, th.muted)
	}
}

func (d *ccDraw) share(p ccPart) float64 {
	v, ok := ccNumber(p.value)
	if !ok || p.max <= p.min {
		return 0
	}
	return math.Min(1, math.Max(0, (v-p.min)/(p.max-p.min)))
}

func (d *ccDraw) placeBar(o image.Point, p ccPart, x, y, width int, th ccTheme) {
	t := d.barThickness()
	track := image.Rect(o.X+x, o.Y+y, o.X+x+width, o.Y+y+t)
	d.r.roundFill(track, float64(t)/2, th.track, th.track)
	if s := d.share(p); s > 0 {
		fill := p.color
		if !p.colored {
			fill = th.accent
		}
		w := max(t, int(math.Round(s*float64(width))))
		d.r.roundFill(image.Rect(track.Min.X, track.Min.Y, track.Min.X+w, track.Max.Y), float64(t)/2, fill, fill)
	}
}

// placeRing is a ring as Tessera's meter draws one, 240 degrees open at the bottom, its value in its middle
// and its icon and label under it.
func (d *ccDraw) placeRing(o image.Point, p ccPart, l ccLine, x, y, column, h int, f ccFont, words bool, th ccTheme) {
	dia := d.lineRingDia(l, f)
	thick := d.ringThick(dia)
	cx := x + column/2
	row := 0
	at := dia
	if words {
		row = d.ringRow(l)
		at = d.ringWordsAt(dia, row, f)
	}
	top := y + max(0, (h-max(dia, at+row))/2)
	rad := float64(dia-thick) / 2
	ccx, ccy := float64(o.X+cx), float64(o.Y+top)+float64(dia)/2
	a0 := 150 * math.Pi / 180
	d.r.aaRing(ccx, ccy, rad, float64(thick), a0, a0+240*math.Pi/180, th.track)
	if s := d.share(p); s > 0 {
		mark := p.color
		if !p.colored {
			mark = th.accent
		}
		d.r.aaRing(ccx, ccy, rad, float64(thick), a0, a0+s*240*math.Pi/180, mark)
	}
	inner := d.innerFace(f)
	if d.besideValues(l) {
		inner = d.ringFaceFor(p, dia, f)
	}
	vw := min(dia-2*thick, d.tw(p.value, inner)+d.unitWidth(p))
	d.placeValue(o, p, inner, cx-vw/2, top+(dia-d.lh(inner))/2, vw, th)
	if !words {
		return
	}
	mark := 0
	if p.icon != "" {
		mark = d.px(ccIconSmall)
	}
	space := 0
	if mark > 0 && p.label != "" {
		space = d.px(6)
	}
	lw := 0
	if p.label != "" {
		lw = d.tw(p.label, ccBody)
	}
	w := min(column, mark+space+lw)
	lx, ly := cx-w/2, top+at
	if mark > 0 {
		d.placeMark(o, p, lx, ly, row, ccIconSmall, th)
	}
	if p.label != "" && w-mark-space > 0 {
		d.text(o, ccBody, p.label, lx+mark+space, ly+(row-d.lh(ccBody))/2, w-mark-space, th.muted)
	}
}

func (d *ccDraw) place(o image.Point, l ccLine, cols int, f ccFont, labels bool, y int, th ccTheme) {
	h := d.lineHeight(l, f, labels)
	if l.across {
		p := d.parts[l.first]
		words := d.wordsFace(f)
		switch p.kind {
		case ccText:
			d.placeWords(o, p, 0, y, h, d.w, words, p.value, d.textLines(p, f), d.valueColor(p, th), th)
		case ccRow:
			right := d.tw(p.value, words) + d.unitWidth(p)
			d.placeValue(o, p, words, d.w-right, y+(h-d.lh(words))/2, right, th)
			d.placeWords(o, p, 0, y, h, max(0, d.w-right-d.px(8)), words, p.label, 1, th.muted, th)
		default: // a bar across: its words at the left, its value at the right, the bar under them
			if ccBarOnly(p) {
				d.placeBar(o, p, 0, y, d.w, th)
				return
			}
			row := d.lh(words)
			right := d.tw(p.value, words) + d.unitWidth(p)
			d.placeValue(o, p, words, d.w-right, y, right, th)
			if !ccBare(p) { // a bare bar's color is in the bar: no dot for it
				d.placeWords(o, p, 0, y, row, max(0, d.w-right-d.px(8)), ccBody, p.label, 1, th.muted, th)
			}
			d.placeBar(o, p, 0, y+row+d.px(4), d.w, th)
		}
		return
	}
	column := d.columnWidth(cols)
	words := labels && d.hasWords(l)
	for k := 0; k < l.count; k++ {
		p := d.parts[l.first+k]
		x := k * (column + d.columnGap())
		if d.meter(p) {
			d.placeRing(o, p, l, x, y, column, h, f, d.hasWords(l), th)
			continue
		}
		if p.kind == ccText {
			t := d.textRow(p, f)
			d.placeWords(o, p, x, y+(h-t)/2, t, column, d.wordsFace(f), p.value, 1, th.ink, th)
			continue
		}
		if ccBarOnly(p) {
			t := d.barThickness()
			d.placeBar(o, p, x, y+(h-t)/2, column, th)
			continue
		}
		own := d.lh(f)
		if words {
			own += d.wordsRow()
		}
		if p.kind == ccBar || l.bar >= 0 {
			own += d.px(4) + d.barThickness()
		}
		top := y + max(0, (h-own)/2)
		vy, room := top, 0
		if words {
			d.placeWords(o, p, x, top, d.wordsRow(), column, ccBody, p.label, 1, th.muted, th)
			vy += d.wordsRow()
		} else if d.hasMark(p) {
			room = d.placeMark(o, p, x, top, d.lh(f), d.inlineIcon(f), th) + d.px(6)
		}
		d.placeValue(o, p, f, x+room, vy, column-room, th)
		if p.kind == ccBar {
			d.placeBar(o, p, x, vy+d.lh(f)+d.px(4), column, th)
		}
		if l.bar >= 0 && k == l.count-1 {
			// The bar's number at the right, on the values' baseline, and the bar under the whole line.
			b, wf := d.parts[l.bar], d.wordsFace(f)
			if !ccBarOnly(b) {
				bw := d.tw(b.value, wf) + d.unitWidth(b)
				d.placeValue(o, b, wf, d.w-bw, vy+d.face(f).Metrics().Ascent.Ceil()-d.face(wf).Metrics().Ascent.Ceil(), bw, th)
			}
			d.placeBar(o, b, 0, vy+d.lh(f)+d.px(4), d.w, th)
		}
	}
}

// ccContrast is WCAG's contrast of two colors, 1 to 21.
func ccContrast(a, b color.RGBA) float64 {
	lum := func(c color.RGBA) float64 {
		ch := func(v uint8) float64 {
			f := float64(v) / 255
			if f <= 0.04045 {
				return f / 12.92
			}
			return math.Pow((f+0.055)/1.055, 2.4)
		}
		return 0.2126*ch(c.R) + 0.7152*ch(c.G) + 0.0722*ch(c.B)
	}
	x, y := lum(a), lum(b)
	return (math.Max(x, y) + 0.05) / (math.Min(x, y) + 0.05)
}
