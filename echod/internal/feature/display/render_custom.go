//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
)

// The custom screens' common top bar (custom_screens.go): a tab for each at the left, what the screen
// says about itself beside them, the time and the × at the right. A screen draws it as part of its own
// page, and adds the zones it returns to its own.

// Sizes, in the Show 5's pixels as the rest of the package's are.
const (
	customBarH = 60  // the top bar
	customIn   = 14  // the page's margin
	customTabW = 120 // a tab
)

// customBar draws the top bar: summary in the accent when hot; shaded is a dark band for a page that
// shows through it (a map), rather than the page's own ground.
func (r *renderer) customBar(s scene, summary string, hot, shaded bool) []customZone {
	fc := r.faces()
	barH := r.s(customBarH)
	if shaded {
		washOver(r.dst, image.Rect(0, 0, r.w, barH), color.RGBA{0, 0, 0, 160})
	}
	var zones []customZone

	// The tabs, the screen up raised, as the drawer's tabs are.
	tabs := offeredScreens()
	half := r.s(customTabW)
	seg := image.Rect(r.s(customIn), r.s(8), r.s(customIn)+len(tabs)*half, barH-r.s(8))
	segRad := float64(seg.Dy()) / 2
	r.roundFill(seg, segRad, surface(1), surface(1))
	r.roundStroke(seg, segRad, r.sf(1), ember)
	inset := r.s(4)
	for i, tab := range tabs {
		b := image.Rect(seg.Min.X+i*half+inset, seg.Min.Y+inset, seg.Min.X+(i+1)*half-inset, seg.Max.Y-inset)
		fg, face := lerp(dim, cream, 0.3), fc.nav
		if tab.id == s.custom.name {
			rad := float64(b.Dy()) / 2
			r.roundFill(b, rad, shift(amber, 14), shift(amber, -14))
			r.roundHighlight(b, rad)
			fg, face = onAccent(), fc.navBold
		}
		r.text(face, tab.label, b.Min.X+(b.Dx()-r.width(face, tab.label))/2, b.Min.Y+b.Dy()/2+r.s(9), fg)
		zones = append(zones, customZone{r: image.Rect(seg.Min.X+i*half, 0, seg.Min.X+(i+1)*half, barH+r.s(6)), kind: "tab", id: tab.id})
	}

	// The ×, round, at the right.
	side := barH - r.s(16)
	c := image.Rect(r.w-r.s(customIn)-side, r.s(8), r.w-r.s(customIn), r.s(8)+side)
	r.roundFill(c, float64(side)/2, surface(7), surface(5))
	r.roundHighlight(c, float64(side)/2)
	cx, cy := float64(c.Min.X)+float64(c.Dx())/2, float64(c.Min.Y)+float64(c.Dy())/2
	arm, pen := r.sf(7), r.sf(26)/10
	r.aaLine(cx-arm, cy-arm, cx+arm, cy+arm, pen, cream)
	r.aaLine(cx-arm, cy+arm, cx+arm, cy-arm, pen, cream)
	zones = append(zones, customZone{r: c.Inset(-r.s(10)), kind: "close"})

	// The time, left of the ×, and the summary between the tabs and it.
	t := clockText(s.now)
	tx := c.Min.X - r.s(18) - r.width(r.small, t)
	base := barH/2 + r.s(10)
	r.text(r.small, t, tx, base, dim)
	col := cream
	if hot {
		col = amber
	}
	room := tx - r.s(24) - (seg.Max.X + r.s(20))
	r.text(fc.value, r.fit(fc.value, summary, room), seg.Max.X+r.s(20), base, col)
	return zones
}
