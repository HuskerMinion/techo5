//go:build !dot && !spot

package display

import (
	"image"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mdi"
)

// A notification from Home Assistant: its words in a card in the middle of the screen, the reminder's
// card, since both are words somebody across the room is meant to read. Over the clock rather than
// instead of it, and until tapped or its time is up.

// notifyLines is as many lines as the card gives a message; a longer one ends in an ellipsis.
const notifyLines = 3

// notifyIcon is the heading's icon size, in the Show 5's pixels.
const notifyIcon = 26

// notifyBox is where the card sits, for drawing it and for knowing a tap landed on it: the
// reminder's. The two share the box, so when both are up the reminder is drawn over the notification
// and its tap is taken first.
func (r *renderer) notifyBox() image.Rectangle { return r.reminderBox() }

func (r *renderer) notifyCard(s scene) {
	n := s.notification
	box := r.notifyBox()
	r.roundShadow(box, r.cardRad(), float64(r.s(34)), r.s(12), shadowAlpha()*1.3)
	r.roundFill(box, r.cardRad(), surface(4), surface(2))
	r.roundHighlight(box, r.cardRad())

	in := r.rowIn()
	width := box.Dx() - 2*in
	base := box.Min.Y + r.s(56)
	hintW := r.width(r.tiny, dismissHint)
	r.rightText(r.tiny, dismissHint, box.Max.X-in, base, dim)

	// The heading: the icon, then the title. Either may be missing; with neither, the hint alone.
	x := box.Min.X + in
	if _, ok := mdi.Rune(n.Icon); ok {
		r.mdiIcon(n.Icon, x, base-r.s(notifyIcon)+r.s(4), notifyIcon, amber)
		x += r.s(notifyIcon + 12)
	}
	if n.Title != "" {
		r.text(r.tiny, clipText(r, r.tiny, strings.ToUpper(n.Title), box.Max.X-in-hintW-r.s(24)-x), x, base, amber)
	}

	lines := r.wrap(r.title, n.Message, width)
	if len(lines) > notifyLines {
		lines = append(lines[:notifyLines-1], clipText(r, r.title, strings.Join(lines[notifyLines-1:], " "), width))
	}
	y := box.Min.Y + r.s(126)
	for _, line := range lines {
		r.text(r.title, line, box.Min.X+in, y, cream)
		y += r.s(58)
	}
}

// captionBar is a picture notification's words along the foot of the camera page, above the strip with
// the hint: the title small and amber, the message on one line under it.
func (r *renderer) captionBar(c home.Caption) {
	const strip = 36 // the hint's strip, which cameraView draws below this
	h := 44
	if c.Title != "" {
		h = 70
	}
	bar := image.Rect(0, r.h-strip-h, r.w, r.h-strip)
	r.fillRect(bar, shade)
	width := r.w - 2*r.margin
	if c.Title != "" {
		r.text(r.tiny, clipText(r, r.tiny, strings.ToUpper(c.Title), width), r.margin, bar.Min.Y+24, amber)
	}
	r.text(r.small, clipText(r, r.small, c.Message, width), r.margin, bar.Max.Y-12, cream)
}
