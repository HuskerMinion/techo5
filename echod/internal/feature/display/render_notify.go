//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// A notification from Home Assistant: its words in a card in the middle of the screen, the reminder's
// card, since both are words somebody across the room is meant to read. Over the clock rather than
// instead of it, and until tapped or its time is up.

// notifyLines is as many lines as the card gives a message; a longer one ends in an ellipsis.
const notifyLines = 3

// notifyBox is where the card sits, for drawing it and for knowing a tap landed on it: the
// reminder's. The two share the box, so when both are up the reminder is drawn over the notification
// and its tap is taken first.
func (r *renderer) notifyBox() image.Rectangle { return r.reminderBox() }

func (r *renderer) notifyCard(s scene) {
	n := s.notification
	r.noticeCard(r.notifyBox(), 56, 126, n.Icon, n.Title, n.Message, notifyLines)
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
	return max(36, r.h-r.cameraSoundBox("Unmute").Min.Y+r.s(4))
}

// captionRect is where the bar goes: along the foot of the page above the strip, as high as its words
// need in the fonts this panel draws them in (the message in r.small, the title above it in r.tiny).
func (r *renderer) captionRect(c home.Caption) image.Rectangle {
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
	if c.Title != "" {
		m := r.tiny.Metrics()
		r.text(r.tiny, clipText(r, r.tiny, strings.ToUpper(c.Title), width), r.margin, y+m.Ascent.Ceil(), captionAccent)
		y += m.Height.Ceil()
	}
	r.text(r.small, clipText(r, r.small, c.Message, width), r.margin, y+r.small.Metrics().Ascent.Ceil(), captionText)
}
