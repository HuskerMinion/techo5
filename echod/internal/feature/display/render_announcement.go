//go:build !dot && !spot

package display

import (
	"image"
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
	// announceBar is how tall the strip is, and announceInset how far in from the edges it sits.
	announceBar   = 132
	announceInset = 28

	// ellipsis marks a message that did not fit, so a sentence that stops reads as one that was cut
	// rather than one that ended there.
	ellipsis = "…"
)

// announcementStrip is the arriving announcement, along the bottom where it covers least.
func (r *renderer) announcementStrip(s scene) {
	m := s.announcement
	who := m.From
	if who == "" {
		who = "Another room"
	}

	top := r.h - announceBar - announceInset
	box := image.Rect(announceInset, top, r.w-announceInset, r.h-announceInset)
	r.roundShadow(box, cardRad, 26, 8, shadowAlpha()*1.2)
	r.roundFill(box, cardRad, surface(4), surface(2))
	r.roundHighlight(box, cardRad)

	r.text(r.tiny, "ANNOUNCEMENT", box.Min.X+rowIn, box.Min.Y+34, amber)

	if m.Text == "" {
		// Nothing to read: the name has the strip to itself, set where it sits when it is the only
		// thing here rather than pinned to where it goes when it shares.
		r.text(r.title, who, box.Min.X+rowIn, box.Min.Y+88, cream)
		return
	}

	// The room on its line and the words on theirs. Beside each other they were fighting over the
	// same inches — a name as ordinary as "Laundry Room" left almost nothing for the message — and
	// the message is the half somebody has to read rather than recognise.
	r.text(r.title, who, box.Min.X+rowIn, box.Min.Y+72, cream)
	r.text(r.body, clipText(r, r.body, m.Text, box.Dx()-2*rowIn), box.Min.X+rowIn, box.Min.Y+112, dim)
}

// recordingStrip says this device's microphone is open and where what it hears is going. It is the
// same strip in the same place, so the two read as one thing happening in two directions.
func (r *renderer) recordingStrip(s scene) {
	top := r.h - announceBar - announceInset
	box := image.Rect(announceInset, top, r.w-announceInset, r.h-announceInset)
	r.roundShadow(box, cardRad, 26, 8, shadowAlpha()*1.2)
	r.roundFill(box, cardRad, shift(surface(4), 6), surface(2))
	r.roundStroke(box, cardRad, 2, amber)
	r.roundHighlight(box, cardRad)

	r.text(r.tiny, "SPEAKING TO THE HOUSE", box.Min.X+rowIn, box.Min.Y+40, amber)
	r.text(r.title, "Go ahead", box.Min.X+rowIn, box.Min.Y+84, cream)

	to := "It sends when you stop talking"
	if s.announcePeers > 0 {
		to = "Goes to " + devicesText(s.announcePeers)
	}
	r.text(r.body, to, box.Min.X+rowIn+300, box.Min.Y+84, dim)
	// The way out, said on the strip that owns it.
	r.text(r.tiny, "TAP TO SEND  ·  HOLD TO CANCEL", box.Min.X+rowIn, box.Min.Y+112, dim)
}

// onAnnounceStrip is whether a finger landed on the strip, which is the only part of the screen an
// announcement owns.
func onAnnounceStrip(x, y, w, h int) bool {
	top := h - announceBar - announceInset
	return x >= announceInset && x <= w-announceInset && y >= top && y <= h-announceInset
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
	run := []rune(s)
	for len(run) > 0 {
		run = run[:len(run)-1]
		// Cut back to a space where there is one near the end, so the last word is whole.
		cut := string(run)
		if r.width(f, cut+ellipsis) <= width {
			if i := strings.LastIndex(strings.TrimRight(cut, " "), " "); i > len(cut)-12 && i > 0 {
				cut = cut[:i]
			}
			return strings.TrimRight(cut, " ") + ellipsis
		}
	}
	return ellipsis
}
