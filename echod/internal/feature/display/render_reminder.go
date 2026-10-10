//go:build !dot && !spot

package display

import (
	"image"
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
	heading := "REMINDER"
	if s.reminderFrom != "" {
		heading += "  ·  " + s.reminderFrom
	}
	r.noticeCard(r.reminderBox(), 56, 126, "", heading, s.reminder.Label, reminderLines)
}
