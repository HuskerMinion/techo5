//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
)

// The Show's settings screen: a rail of categories on the left with the chosen one raised out of it,
// and that category's settings on a card floating over the ground, from the parts in
// sheet_widgets.go.

// settingsPage draws the whole settings screen: sel is the category on the rail, v the card, and pick
// a list of choices open over it (scrolled by pickScroll pixels when long), or nil.
func (r *renderer) settingsPage(sel category, v cardView, pick *pickerView, pickScroll int) {
	fc := r.faces()
	r.pending = r.pending[:0]
	r.settingsBase(sel, v.title, v.blurb)
	for c := category(0); c < categories; c++ {
		top := r.navTop() + int(c)*(r.navH()+r.navGap())
		r.addZone(zone{r: image.Rect(0, top-r.navGap()/2, r.railW(), top+r.navH()+r.navGap()/2), kind: zoneCat, cat: c})
	}
	r.addZone(zone{r: image.Rect(0, r.h-74, r.railW(), r.h), kind: zoneDone})

	card := r.nextCard(r.w, r.h)
	list := image.Rect(card.Min.X, card.Min.Y+r.headerH(), card.Max.X, card.Max.Y-8)
	if len(v.rows) == 0 && v.note != "" {
		y := list.Min.Y + 50
		for _, line := range r.wrap(fc.value, v.note, card.Dx()-2*r.rowIn()) {
			r.text(fc.value, line, card.Min.X+r.rowIn(), y, dim)
			y += 36
		}
	}

	maxScroll := r.rowList(card, list, v.rows, v.scroll, surface(1), r.base.Pix)
	x := card.Max.X - 26
	for _, a := range v.actions {
		left := r.pillButton(x, card.Min.Y+r.headerH()/2-2, a.label, a.style)
		r.addZone(zone{r: image.Rect(left-4, card.Min.Y+8, x+4, card.Min.Y+r.headerH()-8), kind: zoneAction, id: a.id})
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

// nextCard is where the settings card sits on a w by h screen. A method because the rail's width
// and the card's margin are scaled to the panel in hand.
func (r *paint) nextCard(w, h int) image.Rectangle {
	return image.Rect(r.railW(), r.cardIn(), w-r.cardIn(), h-r.cardIn())
}

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
	fc := r.faces()
	r.settingsShell()

	// The rail: each category a label with its icon, the chosen one raised in the accent.
	for c := category(0); c < categories; c++ {
		top := r.navTop() + int(c)*(r.navH()+r.navGap())
		pill := image.Rect(12, top, r.railW()-10, top+r.navH())
		fg, face := lerp(dim, cream, 0.3), fc.nav
		if c == sel {
			r.roundShadow(pill, 14, 14, 5, shadowAlpha())
			r.roundFill(pill, 14, shift(amber, 16), shift(amber, -16))
			r.roundHighlight(pill, 14)
			// The card floats above the rail: its shadow falls across the raised one's end, which the
			// kept shell (drawn before the rail) cannot hold, so that sliver is laid on again here.
			px0, py0, px1, py1 := rectF(pill)
			r.roundShadowIn(r.nextCard(r.w, r.h), r.cardRad(), 22, 8, shadowAlpha(), pill.Inset(-1), func(x, y int) float64 {
				return clamp01(0.5 - rrDist(float64(x)+0.5, float64(y)+0.5, px0, py0, px1, py1, 14))
			})
			fg, face = onAccent(), fc.navBold
		}
		r.icon(c, 12+30, top+r.navH()/2, fg)
		r.text(face, categoryNames[c], 12+58, top+r.navH()/2+9, fg)
	}
	card := r.nextCard(r.w, r.h)
	r.text(fc.header, title, card.Min.X+r.rowIn(), card.Min.Y+46, cream)
	r.text(fc.sub, blurb, card.Min.X+r.rowIn(), card.Min.Y+72, dim)
	r.rule(card.Min.X+22, card.Max.X-22, card.Min.Y+r.headerH()-2, 1)

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
	fc := r.faces()
	r.vgradient(r.dst.Bounds(), shift(walnut, 7), shift(walnut, -3))
	done := image.Rect(12, r.h-66, r.railW()-10, r.h-16)
	r.roundShadow(done, 25, 8, 3, shadowAlpha()*0.6)
	r.roundFill(done, 25, surface(3), surface(2))
	r.roundStroke(done, 25, 1.2, ember)
	r.text(fc.button, "Done", done.Min.X+(done.Dx()-r.width(fc.button, "Done"))/2, done.Min.Y+33, cream)

	// The card, floating over the ground.
	card := r.nextCard(r.w, r.h)
	r.roundShadow(card, r.cardRad(), 22, 8, shadowAlpha())
	r.roundFill(card, r.cardRad(), surface(2), surface(1))
	r.roundHighlight(card, r.cardRad())

	if r.shell == nil || r.shell.Rect != r.dst.Rect {
		r.shell = image.NewRGBA(r.dst.Rect)
	}
	copy(r.shell.Pix, r.dst.Pix)
	r.shellKey = key
}
