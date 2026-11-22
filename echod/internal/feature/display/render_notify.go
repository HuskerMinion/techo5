//go:build !dot && !spot

package display

import (
	"image"
	"image/color"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
	"github.com/HuskerMinion/techo5/echod/internal/feature/remind"
)

// A notification from Home Assistant: its words in a card in the middle of the screen, the reminder's
// card, since both are words somebody across the room is meant to read. Over the clock rather than
// instead of it, and until tapped or its time is up.

// notifyLines is as many lines as the card gives a message; a longer one ends in an ellipsis.
const notifyLines = 3

// A note of the device's own (notify.Note) says what to check, which takes more words than a
// notification's news: they are smaller, and there are more lines of them.
const (
	noteLines = 4
	noteLineH = 44
)

// notifyBox is where the card sits, for drawing it and for knowing a tap landed on it: the
// reminder's. The two share the box, so when both are up the reminder is drawn over the notification
// and its tap is taken first.
func (r *renderer) notifyBox() image.Rectangle { return r.reminderBox() }

// notifyBoxFor is where n's card is drawn, and so where a tap puts it away: a large or huge one takes
// most of the screen (sizedBox), and a tap anywhere on it is a tap on it.
func (r *renderer) notifyBoxFor(n notify.Notification) image.Rectangle {
	if !n.Own && n.Size != notify.SizeNormal {
		return r.sizedBox(n.Size)
	}
	box := r.notifyBox()
	if len(n.Buttons) > 0 {
		// The usual card grows by a row for its buttons, as much up as down.
		grow := r.notifyFoot() / 2
		box.Min.Y, box.Max.Y = box.Min.Y-grow, box.Max.Y+r.notifyFoot()-grow
	}
	return box
}

// notifyFoot is the room a card's row of buttons takes along its foot, with the space under it.
func (r *renderer) notifyFoot() int { return r.s(notifyButtonH + 24) }

// notifyButtons is where n's buttons are on its card: a row along the foot of notifyBoxFor, inside
// the card's margins. None for a card without them.
func (r *renderer) notifyButtons(n notify.Notification) []image.Rectangle {
	if len(n.Buttons) == 0 || n.Own {
		return nil
	}
	box, in := r.notifyBoxFor(n), r.rowIn()
	row := image.Rect(box.Min.X+in, box.Max.Y-r.s(24)-r.s(notifyButtonH), box.Max.X-in, box.Max.Y-r.s(24))
	return r.notifyButtonRow(row, len(n.Buttons))
}

// tapNotifyCard is a tap at p on the card of notification n, which p is on (notifyBoxFor).
func (r *renderer) tapNotifyCard(n notify.Notification, p image.Point) {
	tapNotifyButtons(n, r.notifyButtons(n), p)
}

// cardAt is whether p is on the card that is up: a reminder's, a notification's or an event's pop-up.
// The card is drawn over the announcement's strip where the two meet, so a finger there is the card's.
func (d *Display) cardAt(p image.Point) bool {
	if d.r == nil {
		return false
	}
	if _, up := remind.Get().Showing(); up {
		return p.In(d.r.reminderBox())
	}
	if n, up := notify.Get().Card(); up {
		return p.In(d.r.notifyBoxFor(n))
	}
	return d.r.popupTapped(p)
}

func (r *renderer) notifyCard(s scene) {
	n := s.notification
	if n.Own {
		r.noticeCardIn(r.notifyBox(), 56, 120, n.Icon, n.Title, n.Message, noteLines, r.small, noteLineH)
		return
	}
	// A critical card has no way out but its buttons, so it does not say a tap puts it away.
	r.hintOff = n.Critical
	defer func() { r.hintOff = false }()
	foot := 0
	if len(n.Buttons) > 0 {
		foot = r.notifyFoot()
	}
	box := r.notifyBoxFor(n)
	if s.notifyPulse > 0 {
		r.criticalScreen(s.notifyPulse)
	}
	if n.Size != notify.SizeNormal {
		r.sizedNoticeCardFoot(box, foot, n.Size, n.Icon, n.Title, n.Message)
	} else {
		r.noticeCard(box, 56, 126, n.Icon, n.Title, n.Message, notifyLines)
	}
	r.drawNotifyButtons(n, r.notifyButtons(n))
	if s.notifyPulse > 0 {
		r.criticalEdge(box)
	}
}

// The caption's own colors, which the camera page's top strip shares. The bar's shade is black
// whatever the theme, so its words cannot take the theme's text color, which is dark on Paper and
// Linen: they are a fixed light cream, the title a fixed amber.
var (
	captionText   = color.RGBA{0xf4, 0xee, 0xe2, 0xff}
	captionAccent = color.RGBA{0xf2, 0xb0, 0x4a, 0xff}
	captionDim    = color.RGBA{0xc4, 0xbc, 0xae, 0xff} // the clock beside the camera's name, on the same shade
)

// captionPad is the space above and below the words in the bar, in the Show 5's pixels.
const captionPad = 8

// cameraStrip is the height of the strip along the foot of the camera page, the hint's: at least the
// Show 5's 36, and tall enough to hold the Mute and Talk boxes, which are sized from the font and so
// grow on a larger panel.
func (r *renderer) cameraStrip() int {
	return max(r.s(36), r.h-r.cameraSoundBox("Unmute").Min.Y+r.s(4))
}

// captionRect is where the bar goes: along the foot of the page above the strip, as high as its words
// need in the fonts this panel draws them in (the message in r.small, the title above it in r.tiny).
func (r *renderer) captionRect(c home.Caption) image.Rectangle {
	if size := notify.ParseSize(c.Size); size != notify.SizeNormal && c.Message != "" {
		f := r.sizedCaption(c.Title, c.Message, size)
		h := 2*r.s(captionPad) + len(f.lines)*f.lineH
		if f.titleFace != nil {
			h += f.titleFace.Metrics().Height.Ceil()
		}
		strip := r.cameraStrip()
		return image.Rect(0, r.h-strip-h, r.w, r.h-strip)
	}
	h := 2*r.s(captionPad) + r.small.Metrics().Height.Ceil()
	if c.Title != "" {
		h += r.tiny.Metrics().Height.Ceil()
	}
	strip := r.cameraStrip()
	return image.Rect(0, r.h-strip-h, r.w, r.h-strip)
}

// captionBar is a picture notification's words along the foot of the camera page, above the strip with
// the hint: the title small and amber, the message on one line under it. The strip holds the Mute and
// Talk boxes, so the bar ends above them and the message cannot run under one.
func (r *renderer) captionBar(c home.Caption) {
	bar := r.captionRect(c)
	r.fillRect(bar, shade)
	width := r.w - 2*r.margin
	y := bar.Min.Y + r.s(captionPad)
	if size := notify.ParseSize(c.Size); size != notify.SizeNormal && c.Message != "" {
		f := r.sizedCaption(c.Title, c.Message, size)
		if f.titleFace != nil {
			m := f.titleFace.Metrics()
			r.text(f.titleFace, r.fit(f.titleFace, upper(c.Title), width), r.margin, y+m.Ascent.Ceil(), captionAccent)
			y += m.Height.Ceil()
		}
		for _, line := range f.lines {
			r.text(f.face, line, r.margin, y+f.face.Metrics().Ascent.Ceil(), captionText)
			y += f.lineH
		}
		return
	}
	if c.Title != "" {
		m := r.tiny.Metrics()
		r.text(r.tiny, clipText(r, r.tiny, upper(c.Title), width), r.margin, y+m.Ascent.Ceil(), captionAccent)
		y += m.Height.Ceil()
	}
	r.text(r.small, clipText(r, r.small, c.Message, width), r.margin, y+r.small.Metrics().Ascent.Ceil(), captionText)
}

// criticalScreen is the pulse behind a card, drawn before it: the whole screen around the card in red
// at strength a, so it is seen from across the room. One fill, cheap enough to redraw every pulseFrame.
func (r *paint) criticalScreen(a float64) { fillCanvas(r.dst, pulseColor(a)) }

// criticalEdge is the card's red edge while it pulses, drawn over the card.
func (r *paint) criticalEdge(box image.Rectangle) {
	r.roundStroke(box, r.cardRad(), float64(r.s(8)), danger)
}
