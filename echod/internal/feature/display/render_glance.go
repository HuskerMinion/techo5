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

	widthsIn := func(f font.Face) []int {
		ws := make([]int, len(chips))
		for i, c := range chips {
			ws[i] = pad + r.s(icon) + iconGap + r.width(f, c.Text) + pad
		}
		return ws
	}
	// The small face when every chip fits in it; otherwise the tiny one, when that fits more.
	face, lift := r.small, r.s(10)
	widths := widthsIn(face)
	keep, total := fitChips(widths, gap, room)
	if len(keep) < len(chips) {
		tinyWidths := widthsIn(r.tiny)
		if k, t := fitChips(tinyWidths, gap, room); len(k) > len(keep) {
			face, lift, widths, keep, total = r.tiny, r.s(8), tinyWidths, k, t
		}
	}
	x := (r.w - total) / 2
	for _, i := range keep {
		c, w := chips[i], widths[i]
		box := image.Rect(x, bottom-h, x+w, bottom)
		r.roundFill(box, float64(h)/2, ember, ember)
		r.mdiIcon(c.Icon, x+pad, bottom-h+(h-r.s(icon))/2, icon, amber)
		r.text(face, c.Text, x+pad+r.s(icon)+iconGap, bottom-h/2+lift, cream)
		x += w + gap
	}
}

// fitChips is which chips fit in room, in order, given each one's width and the gap between them, and
// how wide they are together. A chip too wide for what is left is skipped rather than ending the strip,
// so a shorter one after it still shows.
func fitChips(widths []int, gap, room int) (keep []int, total int) {
	for i, w := range widths {
		next := total + w
		if len(keep) > 0 {
			next += gap
		}
		if next > room {
			continue
		}
		keep, total = append(keep, i), next
	}
	return keep, total
}
