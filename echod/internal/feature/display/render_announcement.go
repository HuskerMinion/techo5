//go:build !dot && !spot

package display

import "image"

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

	// wordsGap is the space between the name of the room an announcement came from and the words it
	// carried, when it carried any.
	wordsGap = 28
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

	r.text(r.tiny, "ANNOUNCEMENT", box.Min.X+rowIn, box.Min.Y+40, amber)
	r.text(r.title, who, box.Min.X+rowIn, box.Min.Y+84, cream)

	if m.Text != "" {
		// After the name, not at a fixed offset from the edge. The offset was 300 and "Laundry Room"
		// is wider than that, so the words were drawn straight through the name of the room they came
		// from — exactly the two things this strip exists to say.
		x := box.Min.X + rowIn + r.width(r.title, who) + wordsGap
		// One line: the voice carries the rest, and a wall of text on a strip nobody asked for is
		// worse than a sentence that stops.
		words := m.Text
		for len(words) > 0 && x+r.width(r.body, words) > box.Max.X-rowIn {
			words = words[:len(words)-1]
		}
		r.text(r.body, words, x, box.Min.Y+84, dim)
	}
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
