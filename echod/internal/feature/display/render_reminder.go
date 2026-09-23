//go:build !dot && !spot

package display

import (
	"image"
	"strings"
)

// A reminder going off: its words in a card in the middle of the screen, over the clock rather than
// instead of it. It is bigger than an announcement's strip because the words are the whole point of
// it and may run to a sentence, and it stays until somebody taps it, since a reminder nobody saw has
// not reminded anybody.

// reminderLines is as many lines as the card gives a reminder; a longer one ends in an ellipsis.
const reminderLines = 3

// reminderBox is where the card sits, for drawing it and for knowing a tap landed on it.
func (r *renderer) reminderBox() image.Rectangle {
	w, h := r.s(700), r.s(300)
	return image.Rect((r.w-w)/2, (r.h-h)/2, (r.w-w)/2+w, (r.h-h)/2+h)
}

func (r *renderer) reminderCard(s scene) {
	box := r.reminderBox()
	r.roundShadow(box, r.cardRad(), float64(r.s(34)), r.s(12), shadowAlpha()*1.3)
	r.roundFill(box, r.cardRad(), surface(4), surface(2))
	r.roundHighlight(box, r.cardRad())

	heading := "REMINDER"
	if s.reminderFrom != "" {
		heading += "  ·  " + strings.ToUpper(s.reminderFrom)
	}
	hintW := r.width(r.tiny, dismissHint)
	r.rightText(r.tiny, dismissHint, box.Max.X-r.rowIn(), box.Min.Y+r.s(56), dim)
	r.text(r.tiny, clipText(r, r.tiny, heading, box.Dx()-2*r.rowIn()-hintW-r.s(24)),
		box.Min.X+r.rowIn(), box.Min.Y+r.s(56), amber)

	width := box.Dx() - 2*r.rowIn()
	lines := r.wrap(r.title, s.reminder.Label, width)
	if len(lines) > reminderLines {
		lines = append(lines[:reminderLines-1], clipText(r, r.title, strings.Join(lines[reminderLines-1:], " "), width))
	}
	y := box.Min.Y + r.s(126)
	for _, line := range lines {
		r.text(r.title, line, box.Min.X+r.rowIn(), y, cream)
		y += r.s(58)
	}
}
