//go:build spot

package display

import (
	"fmt"
	"math"
	"strings"
)

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
		who = tr("drawer_another_room")
	}

	r.centered(r.label, tr("drawer_announcement"), 150, colAccent)
	r.centered(r.title, clip(r.title, r, who, 360), 210, colText)

	if m.Text == "" {
		r.centered(r.small, tr("drawer_playing"), 258, colDim)
		r.centered(r.small, tr("drawer_dismiss"), 300, colDim)
		return
	}
	// Two lines at most: the voice carries it, and a circle has no room for a paragraph.
	y := 258
	for i, line := range r.wrap(r.body, m.Text, 330) {
		if i == 2 {
			break
		}
		r.centered(r.body, line, y, colDim)
		y += 34
	}
	// The way out, said on the face. Without it this face owns the screen until it times out, and
	// there is nothing on it to suggest otherwise.
	r.centered(r.small, tr("drawer_dismiss"), y+16, colDim)
}

// recordingFace is this device's microphone open for one.
func (r *roundRenderer) recordingFace(s roundScene) {
	r.centered(r.label, tr("drawer_speaking_house"), 150, colAccent)
	r.centered(r.title, tr("drawer_go_ahead"), 212, colText)

	to := tr("drawer_auto_send")
	if s.announcePeers > 0 {
		to = fmt.Sprintf(tr("drawer_heard_on"), devicesText(s.announcePeers))
	}
	r.centered(r.small, to, 254, colDim)
	// The way out, said on the face: without it the only way off this screen was to wait.
	r.centered(r.small, strings.Join(strings.Fields(tr("drawer_send_hint")), " "), 288, colDim)

	// A ring just inside the bezel while it listens, so the state reads from across the room rather
	// than only up close.
	r.ringAt(center, center, 224, 229, 0, 2*math.Pi, colAccent)
}
