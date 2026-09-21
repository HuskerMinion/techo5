//go:build spot

package display

import "math"

// An announcement on the round face: one arriving, and this device taking one.
//
// The Show puts a strip along the bottom, where a rectangle has room to spare. A circle has none, so
// this takes the face: the voice is the announcement and the face says whose it is, for its
// forty-five seconds, over the clock.

// announceFace is an announcement that arrived.
func (r *roundRenderer) announceFace(s roundScene) {
	m := s.announcement
	who := m.From
	if who == "" {
		who = "Another room"
	}

	r.centred(r.label, "ANNOUNCEMENT", 150, colAccent)
	r.centred(r.title, clip(r.title, r, who, 360), 210, colText)

	if m.Text == "" {
		r.centred(r.small, "playing", 258, colDim)
		r.centred(r.small, "tap to dismiss", 300, colDim)
		return
	}
	// Two lines at most: the voice carries it, and a circle has no room for a paragraph.
	y := 258
	for i, line := range r.wrap(r.body, m.Text, 330) {
		if i == 2 {
			break
		}
		r.centred(r.body, line, y, colDim)
		y += 34
	}
	// The way out, said on the face. Without it this face owns the screen until it times out, and
	// there is nothing on it to suggest otherwise.
	r.centred(r.small, "tap to dismiss", y+16, colDim)
}

// recordingFace is this device's microphone open for one.
func (r *roundRenderer) recordingFace(s roundScene) {
	r.centred(r.label, "SPEAKING TO THE HOUSE", 150, colAccent)
	r.centred(r.title, "Go ahead", 212, colText)

	to := "it sends when you stop"
	if s.announcePeers > 0 {
		to = "heard on " + devicesText(s.announcePeers)
	}
	r.centred(r.small, to, 254, colDim)
	// The way out, said on the face: without it the only way off this screen was to wait.
	r.centred(r.small, "tap to send · hold to cancel", 288, colDim)

	// A ring just inside the bezel while it listens, so the state reads from across the room rather
	// than only up close.
	r.ringAt(centre, centre, 224, 229, 0, 2*math.Pi, colAccent)
}
