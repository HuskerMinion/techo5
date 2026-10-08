//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
)

// deckView is what the deck page shows (deck.go fills it in).
type deckView struct {
	cols, rows  int
	page, pages int
	buttons     []deckButtonView

	// connected, problem and obsSet say how OBS is: a line at the foot of the page when it isn't
	// there, so a deck of grayed-out buttons says why.
	connected bool
	problem   string
	obsSet    bool
}

type deckButtonView struct {
	empty       bool
	label, icon string
	color       string
	lit, known  bool
	pressed     bool // a press going out
	failed      bool // a press that failed
}

// Sizes, in the layout's sizes (a Show 5 in landscape).
const (
	deckMargin = 14
	deckGap    = 12
	deckFoot   = 30 // the strip for the page dots and the OBS line
	deckRadius = 18
)

// deckColor is a button's color by name; empty is the theme's accent.
func deckColor(name string) color.RGBA {
	switch name {
	case "blue":
		return color.RGBA{0x3d, 0x8b, 0xe8, 0xff}
	case "green":
		return color.RGBA{0x3f, 0xb9, 0x6a, 0xff}
	case "red":
		return danger
	case "orange":
		return color.RGBA{0xf0, 0x8a, 0x2c, 0xff}
	case "purple":
		return color.RGBA{0x9b, 0x6b, 0xe0, 0xff}
	case "gray":
		return color.RGBA{0x9a, 0x9a, 0x9a, 0xff}
	}
	return amber
}

// deckPage draws the deck: the grid of buttons, the page dots under it, and a line about OBS when
// it isn't connected.
func (r *renderer) deckPage(s scene) {
	// (The zones are cleared in draw on every frame that doesn't draw the deck.)
	v := s.deck
	r.glassBackdrop()
	m, gap, foot := r.s(deckMargin), r.s(deckGap), r.s(deckFoot)
	area := image.Rect(m, m, r.w-m, r.h-foot)
	cols, rows := max(v.cols, 1), max(v.rows, 1)
	cw := (area.Dx() - (cols-1)*gap) / cols
	ch := (area.Dy() - (rows-1)*gap) / rows

	var zones []image.Rectangle
	for i, b := range v.buttons {
		c, row := i%cols, i/cols
		at := image.Pt(area.Min.X+c*(cw+gap), area.Min.Y+row*(ch+gap))
		box := image.Rectangle{Min: at, Max: at.Add(image.Pt(cw, ch))}
		zones = append(zones, box)
		r.deckButton(box, b)
	}
	r.zmu.Lock()
	r.deckZones = zones
	r.zmu.Unlock()

	// The page dots, centered in the foot, when there is more than one page.
	fy := r.h - foot/2
	if v.pages > 1 {
		dot, step := r.s(8), r.s(18)
		x := r.w/2 - (v.pages-1)*step/2
		for p := range v.pages {
			col := lerp(walnut, dim, 0.6)
			if p == v.page {
				col = cream
			}
			b := image.Rect(x-dot/2, fy-dot/2, x+dot/2, fy+dot/2)
			r.roundFill(b, float64(dot)/2, col, col)
			x += step
		}
	}

	// Why the buttons are gray, at the foot's left.
	var line string
	switch {
	case !v.obsSet:
		line = "OBS isn't set up: setup page → Screen & Photos → Deck"
	case !v.connected && v.problem != "":
		line = v.problem
	case !v.connected:
		line = "Connecting to OBS…"
	}
	if line != "" {
		fc := r.faces()
		r.text(fc.sub, r.fit(fc.sub, line, r.w/2-2*m), m, fy+r.s(7), dim)
	}
}

// deckButton draws one square as frosted glass: a see-through panel tinted with the button's color,
// bright along its top edge with a thin light rim, its icon in the color and its words in white. Lit,
// the color fills it and glows around it. An empty square is a faint rim, so the grid still reads.
func (r *renderer) deckButton(b image.Rectangle, v deckButtonView) {
	rad := r.sf(deckRadius)
	white := color.RGBA{0xff, 0xff, 0xff, 0xff}
	if v.empty {
		r.glassFill(b, rad, white, 0.025, 0.015)
		r.glassRim(b, rad, r.sf(1), white, 0.08)
		return
	}
	col := deckColor(v.color)
	text, icon := color.RGBA{0xf6, 0xf2, 0xec, 0xff}, lerp(col, white, 0.25)

	// It stands off the page: a soft shadow under it, which shrinks as a press pushes it down.
	black := color.RGBA{0, 0, 0, 0xff}
	lift := r.s(5)
	if v.pressed {
		lift = r.s(1)
		b = b.Add(image.Pt(0, r.s(3)))
	}
	shadow := b.Add(image.Pt(0, lift))
	r.glassGlow(shadow, rad, r.sf(12), black, 0.5)
	r.glassFill(shadow, rad, black, 0.30, 0.45)

	switch {
	case v.failed:
		r.glassGlow(b, rad, r.sf(14), danger, 0.35)
		r.glassFill(b, rad, danger, 0.70, 0.55)
		icon = white
	case !v.known:
		// OBS away, or something on the button that OBS doesn't have: clouded glass, no color.
		r.glassFill(b, rad, white, 0.05, 0.03)
		text, icon = lerp(dim, white, 0.15), lerp(dim, white, 0.15)
	case v.lit:
		r.glassGlow(b, rad, r.sf(18), col, 0.6)
		r.glassFill(b, rad, col, 0.82, 0.62)
		icon = white
	default:
		r.glassFill(b, rad, white, 0.09, 0.04)
		r.glassFill(b, rad, col, 0.04, 0.08)
	}
	if v.pressed {
		r.glassFill(b, rad, white, 0.22, 0.16)
	}
	// The light on a rounded face: brighter over the top, shaded toward the foot, a bevel along the
	// edge, and the rim.
	sheen := image.Rect(b.Min.X, b.Min.Y, b.Max.X, b.Min.Y+b.Dy()/2)
	r.glassFill(sheen, rad, white, 0.13, 0)
	foot := image.Rect(b.Min.X, b.Min.Y+b.Dy()/2, b.Max.X, b.Max.Y)
	r.glassFill(foot, rad, black, 0, 0.22)
	r.glassBevel(b, rad, r.sf(7), 0.45)
	r.glassRim(b, rad, r.sf(1)*1.2, white, 0.30)

	// The icon in the upper part, the words under it, both scaled to the button.
	unit := min(b.Dx(), b.Dy())
	isz := min(unit*42/100, b.Dy()*45/100) * r.sDenOr1() / r.sNumOr1()
	labelSize := max(min(unit*16/100, 30), 16) * r.sDenOr1() / r.sNumOr1()
	face := r.textFace(true, labelSize)
	words := r.fit(face, v.label, b.Dx()-r.s(16))
	lh := r.s(labelSize)
	content := r.s(isz) + r.s(14) + lh
	top := b.Min.Y + (b.Dy()-content)/2
	if v.icon != "" {
		r.mdiIcon(v.icon, b.Min.X+(b.Dx()-r.s(isz))/2, top, isz, icon)
	}
	r.text(face, words, b.Min.X+(b.Dx()-r.width(face, words))/2, top+r.s(isz)+r.s(14)+lh*8/10, text)
}

// sNumOr1 and sDenOr1 are the panel's scale against the layout's, 1:1 when unset: deckButton sizes
// its icon and words from the button's own pixels, and the drawing helpers scale again.
func (r *paint) sNumOr1() int { return max(r.sNum, 1) }
func (r *paint) sDenOr1() int { return max(r.sDen, 1) }

// deckHit is the button a tap at p landed on, in the deck last drawn.
func (r *renderer) deckHit(p image.Point) (int, bool) {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	for i, z := range r.deckZones {
		if p.In(z) {
			return i, true
		}
	}
	return 0, false
}
