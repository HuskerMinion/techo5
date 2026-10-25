//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"strings"

	"golang.org/x/image/font"
)

// An announcement arriving, and this device recording one.
//
// The voice is the announcement and the screen is the footnote: it says who it came from, because a
// voice from another room with no name on it is a voice you then go looking for. Where there are
// words as well — an automation sent them, or Home Assistant heard them — they are shown, since
// somebody who missed the first second can read the rest.
//
// It does not take the screen. The clock stays, the music stays, and this sits over the top for its
// forty-five seconds: an announcement is not a thing anybody has to answer.

const (
	// announceBar is how tall the strip is, and announceInset how far in from the edges it sits, in
	// Show 5 pixels (announceBox scales them).
	announceBar   = 132
	announceInset = 28

	// ellipsis marks a message that did not fit, so a sentence that stops reads as one that was cut
	// rather than one that ended there.
	ellipsis = "…"

	// dismissHint is the way out, said on the strip that owns it.
	dismissHint = "TAP TO DISMISS"
)

// announcementStrip is the arriving announcement, along the bottom where it covers least.
func (r *renderer) announcementStrip(s scene) {
	m := s.announcement
	who := m.From
	if who == "" {
		who = "Another room"
	}

	box := announceBox(r.w, r.h)
	in := stripPaint(r.w, r.h) // the strip's own lines (announceBox)
	r.roundShadow(box, r.cardRad(), r.sf(26), r.s(8), shadowAlpha()*1.2)
	r.roundFill(box, r.cardRad(), surface(4), surface(2))
	r.roundHighlight(box, r.cardRad())

	// The way out, on the right of the heading rather than on a line of its own: the strip is a
	// hundred and thirty pixels and the message has first claim on them.
	r.rightText(r.tiny, dismissHint, box.Max.X-r.rowIn(), box.Min.Y+in.s(40), dim)

	if m.Text == "" {
		// Nothing to read, so the room is the whole of it and is set large.
		r.text(r.tiny, "ANNOUNCEMENT", box.Min.X+r.rowIn(), box.Min.Y+in.s(40), amber)
		r.text(r.title, who, box.Min.X+r.rowIn(), box.Min.Y+in.s(92), cream)
		return
	}

	// With words, the room joins the heading and the message takes the line below.
	//
	// Three stacked sizes did not fit in the strip: the room set in the big face crowded the heading
	// above it and the message below, all in a hundred and thirty pixels. The room is context — it
	// says who, and whoever is reading already knows the rooms in their own house — while the message
	// is the part that has to be read, so the message keeps the large face and the width.
	head := "ANNOUNCEMENT  ·  " + strings.ToUpper(who)
	// The heading stops short of the hint on its right.
	room := box.Dx() - 2*r.rowIn() - r.width(r.tiny, dismissHint) - r.s(24)
	r.text(r.tiny, clipText(r, r.tiny, head, room), box.Min.X+r.rowIn(), box.Min.Y+in.s(44), amber)
	r.text(r.body, clipText(r, r.body, m.Text, box.Dx()-2*r.rowIn()), box.Min.X+r.rowIn(), box.Min.Y+in.s(100), cream)
}

// recordingStrip says this device's microphone is open and where what it hears is going. It is the
// same strip in the same place, so the two read as one thing happening in two directions.
func (r *renderer) recordingStrip(s scene) {
	box := announceBox(r.w, r.h)
	in := stripPaint(r.w, r.h)
	r.roundShadow(box, r.cardRad(), r.sf(26), r.s(8), shadowAlpha()*1.2)
	r.roundFill(box, r.cardRad(), shift(surface(4), 6), surface(2))
	r.roundStroke(box, r.cardRad(), r.sf(2), amber)
	r.roundHighlight(box, r.cardRad())

	r.text(r.tiny, "SPEAKING TO THE HOUSE", box.Min.X+r.rowIn(), box.Min.Y+in.s(40), amber)
	r.text(r.title, "Go ahead", box.Min.X+r.rowIn(), box.Min.Y+in.s(84), cream)

	to := "It sends when you stop talking"
	if s.announcePeers > 0 {
		to = "Goes to " + devicesText(s.announcePeers)
	}
	r.text(r.body, to, box.Min.X+r.rowIn()+r.s(300), box.Min.Y+in.s(84), dim)
	// The way out, said on the strip that owns it.
	r.text(r.tiny, "TAP TO SEND  ·  HOLD TO CANCEL", box.Min.X+r.rowIn(), box.Min.Y+in.s(112), dim)
}

// announceBox is where the strip is on a w by h panel, its fixed sizes scaled from the Show 5's
// (stripPaint).
func announceBox(w, h int) image.Rectangle {
	p := stripPaint(w, h)
	bar, in := p.s(announceBar), p.s(announceInset)
	return image.Rect(in, h-bar-in, w-in, h-in)
}

// stripPaint scales the strip's height, inset and lines: by the panel's width, as the renderer scales
// everything else, but a tenth less on a 16:10 panel (the Show 8's 1280 by 800). Its clock's date line
// sits lower on the screen than on the wider panels, and the strip at the full scale came within a few
// pixels of it. On a 16:9 panel (the Skylight's 1920 by 1080) it goes by the height, so it covers the
// date line whole, as on the Show 5, instead of cutting it in half. The text on it is the renderer's
// own, at the full scale.
func stripPaint(w, h int) *paint {
	if h*16 >= w*10 {
		return &paint{sNum: w * 9, sDen: drawnFor * 10}
	}
	if h*16 >= w*9 { // 16:9, the Skylight: by height, so it covers its lower date line whole
		return &paint{sNum: h, sDen: drawnFor / 2}
	}
	return &paint{sNum: w, sDen: drawnFor}
}

// onAnnounceStrip is whether a finger landed on the strip, which is the only part of the screen an
// announcement owns.
func onAnnounceStrip(x, y, w, h int) bool {
	b := announceBox(w, h)
	return x >= b.Min.X && x <= b.Max.X && y >= b.Min.Y && y <= b.Max.Y
}

// clipText is as much of s as fits in width, with an ellipsis when that is not all of it.
//
// A voice announcement carries its own message and the words are the footnote, so one line is the
// right amount of room. An automation sending a paragraph gets the start of it and a mark saying
// there was more, which beats both a wall of text and a sentence that simply stops.
func clipText(r *renderer, f font.Face, s string, width int) string {
	if r.width(f, s) <= width {
		return s
	}
	// The longest start of s that fits with the ellipsis, found by halves: a title of thousands of
	// letters is measured a dozen times rather than once for every letter taken off.
	run := []rune(s)
	lo, hi := 0, len(run)-1 // run[:lo] fits (or is empty); run[:hi+1] and longer do not, as s did not
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if r.width(f, string(run[:mid])+ellipsis) <= width {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo == 0 {
		return ellipsis
	}
	// Cut back to a space where there is one near the end, so the last word is whole.
	cut := string(run[:lo])
	if i := strings.LastIndex(strings.TrimRight(cut, " "), " "); i > len(cut)-12 && i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ") + ellipsis
}

// rightText draws s ending at x rather than starting there.
func (r *renderer) rightText(f font.Face, s string, x, y int, c color.Color) {
	r.text(f, s, x-r.width(f, s), y, c)
}
