//go:build !dot && !spot

package display

import (
	"image"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// glanceStrip draws the chips centered along the foot of the clock page, above the footer's line: a
// pill each, the entity's icon in the accent and its line in the text color. The small face is tried
// first and the tiny one when that does not fit them all; then as many as fit are drawn, in order.
func (r *renderer) glanceStrip(chips []home.Chip, callButton bool) {
	const icon = 24 // mdiIcon scales it
	h := r.s(46)
	bottom := r.h - r.s(50)
	gap, pad, iconGap := r.s(12), r.s(16), r.s(10)
	room := r.w - 2*r.margin
	if callButton {
		// The Call button has the bottom-left corner: the strip keeps as clear of it on the right as on
		// the left, so it stays centered.
		room = r.w - 2*(r.callButtonRect().Max.X+gap)
	}

	face, lift := r.small, r.s(10)
	fit := func(f font.Face) (widths []int, total int) {
		for _, c := range chips {
			w := pad + r.s(icon) + iconGap + r.width(f, c.Text) + pad
			next := total + w
			if len(widths) > 0 {
				next += gap
			}
			if next > room {
				break
			}
			widths, total = append(widths, w), next
		}
		return widths, total
	}
	widths, total := fit(face)
	if len(widths) < len(chips) {
		face, lift = r.tiny, r.s(8)
		widths, total = fit(face)
	}
	x := (r.w - total) / 2
	for i, w := range widths {
		c := chips[i]
		box := image.Rect(x, bottom-h, x+w, bottom)
		r.roundFill(box, float64(h)/2, ember, ember)
		r.mdiIcon(c.Icon, x+pad, bottom-h+(h-r.s(icon))/2, icon, amber)
		r.text(face, c.Text, x+pad+r.s(icon)+iconGap, bottom-h/2+lift, cream)
		x += w + gap
	}
}
