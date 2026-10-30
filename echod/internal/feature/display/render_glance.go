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
// While the music strip is up (Chips while music plays: Above the strip) the row sits over it instead,
// smaller and always in the tiny face, so the clock has to move up only a little for it.
func (r *renderer) glanceStrip(chips []home.Chip, callButton, overStrip bool) {
	icon := 24 // mdiIcon scales it
	h := r.s(46)
	bottom := r.h - r.s(50)
	gap, pad, iconGap := r.s(12), r.s(16), r.s(10)
	if overStrip {
		icon, h, bottom = 18, r.s(overStripChipHigh), r.stripRect().Min.Y-r.s(overStripChipGap)
		gap, pad, iconGap = r.s(10), r.s(12), r.s(6)
	}
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
	// The small face when every chip fits in it; otherwise the tiny one, when that fits more. Glance chip
	// size keeps to one of them: the chips that do not fit in it drop off.
	face, lift := r.small, r.s(10)
	size := glanceSize()
	if overStrip {
		size = "tiny"
	}
	if size == "tiny" {
		face, lift = r.tiny, r.s(8)
	}
	widths := widthsIn(face)
	keep, total := fitChips(widths, gap, room)
	if size == "" && len(keep) < len(chips) {
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

// overStripChipHigh is how tall the chips are over the music strip, and overStripChipGap the room
// between them and the strip, before scaling: the clock moves up by about the two together.
const overStripChipHigh, overStripChipGap = 32, 6

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

// glanceShows is whether the clock page has the glance chips: there is news, no running timer has the
// foot, and the music strip is down or Chips while music plays puts them over it.
func glanceShows(s scene, timers bool) bool {
	return len(s.glance) > 0 && !timers && (!s.strip || glanceOverStrip())
}
