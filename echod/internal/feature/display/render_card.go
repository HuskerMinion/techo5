//go:build !dot && !spot

package display

import (
	"image"
	"strconv"
	"strings"

	"golang.org/x/image/font"

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

// cardsUnder is how many cards wait under the one a frame shows. One card is up at a time, a reminder
// over a notification over an event's pop-up, and putting one away uncovers the next: the card says
// how many, so that is not a surprise. There is one reminder and one notification at most (a newer
// one takes the older one's place), and as many pop-ups as there are events due.
func cardsUnder(s scene) int {
	popups := s.popupWaiting
	if s.popup != nil {
		popups++
	}
	switch {
	case s.showReminder && s.showNotification:
		return 1 + popups
	case s.showReminder, s.showNotification:
		return popups
	}
	return s.popupWaiting
}

// cardHint draws the way out ending at right on the heading's baseline, and before it "+2" in a
// small pill when two more cards wait under this one (r.cardsUnder). It returns the width the two
// take, which the heading stops short of.
func (r *renderer) cardHint(right, base int) int {
	hint := dismissHint()
	w := r.width(r.tiny, hint)
	r.rightText(r.tiny, hint, right, base, dim)
	if r.cardsUnder <= 0 {
		return w
	}
	more := "+" + strconv.Itoa(r.cardsUnder)
	m := r.tiny.Metrics()
	pad := r.s(10)
	pill := image.Rect(0, base-m.Ascent.Ceil()-r.s(2), r.width(r.tiny, more)+2*pad, base+m.Descent.Ceil()+r.s(2))
	pill = pill.Add(image.Pt(right-w-r.s(12)-pill.Dx(), 0))
	r.roundFill(pill, float64(pill.Dy())/2, surface(10), surface(10))
	r.text(r.tiny, more, pill.Min.X+pad, base, cream)
	return right - pill.Min.X
}

// noticeCard draws the card's box, its heading and up to n lines of text wrapped inside it. base is the
// heading's baseline and top the first line's, both in the Show 5's pixels down from the box's top. icon
// is an mdi name that comes before the heading, or "" (or a name there is no glyph for) for none. With no
// heading the hint stands alone.
func (r *renderer) noticeCard(box image.Rectangle, base, top int, icon, heading, text string, n int) {
	r.noticeCardIn(box, base, top, icon, heading, text, n, r.title, cardLineH)
}

// noticeCardIn is noticeCard with the words in face, lineH apart (in the Show 5's pixels): a note of the
// device's own has more to say than a notification, and is read from closer.
func (r *renderer) noticeCardIn(box image.Rectangle, base, top int, icon, heading, text string, n int, face font.Face, lineH int) {
	r.roundShadow(box, r.cardRad(), float64(r.s(34)), r.s(12), shadowAlpha()*1.3)
	r.roundFill(box, r.cardRad(), surface(4), surface(2))
	r.roundHighlight(box, r.cardRad())

	in := r.rowIn()
	width := box.Dx() - 2*in
	baseY := box.Min.Y + r.s(base)
	hintW := r.cardHint(box.Max.X-in, baseY)

	// The heading: the icon, then the words, clipped short of the hint.
	x := box.Min.X + in
	if _, ok := mdi.Rune(icon); ok {
		r.mdiIcon(icon, x, baseY-r.s(cardIcon)+r.s(4), cardIcon, amber)
		x += r.s(cardIcon + 12)
	}
	if heading != "" {
		r.text(r.tiny, clipText(r, r.tiny, upper(heading), box.Max.X-in-hintW-r.s(24)-x), x, baseY, amber)
	}

	lines := r.wrap(face, text, width)
	if len(lines) > n {
		lines = append(lines[:n-1], strings.Join(lines[n-1:], " "))
	}
	y := box.Min.Y + r.s(top)
	for _, line := range lines {
		r.text(face, clipText(r, face, line, width), box.Min.X+in, y, cream)
		y += r.s(lineH)
	}
}
