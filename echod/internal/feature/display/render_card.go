//go:build !dot && !spot

package display

import (
	"image"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/lib/mdi"
)

// The notice card: a rounded box in the middle of the screen, over the clock rather than instead of
// it, with a small amber heading and the "tap to dismiss" hint along its top and the words below. A
// reminder, an event's pop-up and a notification from Home Assistant are all this card, so they look
// alike and a change to how it looks is made once.

// cardIcon is the heading's icon size, in the Show 5's pixels.
const cardIcon = 26

// cardLineH is the distance between the baselines of a card's lines of words, in the Show 5's pixels.
const cardLineH = 58

// noticeCard draws the card's box, its heading and up to n lines of text wrapped inside it. base is the
// heading's baseline and top the first line's, both in the Show 5's pixels down from the box's top. icon
// is an mdi name that comes before the heading, or "" (or a name there is no glyph for) for none. With no
// heading the hint stands alone.
func (r *renderer) noticeCard(box image.Rectangle, base, top int, icon, heading, text string, n int) {
	r.roundShadow(box, r.cardRad(), float64(r.s(34)), r.s(12), shadowAlpha()*1.3)
	r.roundFill(box, r.cardRad(), surface(4), surface(2))
	r.roundHighlight(box, r.cardRad())

	in := r.rowIn()
	width := box.Dx() - 2*in
	baseY := box.Min.Y + r.s(base)
	hintW := r.width(r.tiny, dismissHint)
	r.rightText(r.tiny, dismissHint, box.Max.X-in, baseY, dim)

	// The heading: the icon, then the words, clipped short of the hint.
	x := box.Min.X + in
	if _, ok := mdi.Rune(icon); ok {
		r.mdiIcon(icon, x, baseY-r.s(cardIcon)+r.s(4), cardIcon, amber)
		x += r.s(cardIcon + 12)
	}
	if heading != "" {
		r.text(r.tiny, clipText(r, r.tiny, strings.ToUpper(heading), box.Max.X-in-hintW-r.s(24)-x), x, baseY, amber)
	}

	lines := r.wrap(r.title, text, width)
	if len(lines) > n {
		lines = append(lines[:n-1], strings.Join(lines[n-1:], " "))
	}
	y := box.Min.Y + r.s(top)
	for _, line := range lines {
		r.text(r.title, clipText(r, r.title, line, width), box.Min.X+in, y, cream)
		y += r.s(cardLineH)
	}
}
