//go:build !dot && !spot

package display

import (
	"image"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// The Plex link on the Show: what it is, the code as large as the width takes it, where to type it, and
// Cancel under the words.

func (r *renderer) plexCancel() image.Rectangle {
	w, h := r.s(150), r.s(56)
	y := r.h - r.s(40) - h
	return image.Rect(r.margin, y, r.margin+w, y+h)
}

func (r *renderer) plexPage(l linkView) {
	room := r.w - 2*r.margin
	r.text(r.title, r.fit(r.title, "Plex", room), r.margin, r.s(120), cream)
	if l.code != "" {
		// The four characters as big as they go: somebody is reading them off the screen and typing
		// them somewhere else, often from across the room.
		face := r.big
		code := l.code
		for face == r.big && r.width(face, code) > room/2 {
			face = r.title
		}
		r.text(face, code, r.margin, r.s(230), cream)
		r.text(r.body, r.fit(r.body, tr("plex_go_to_link"), room), r.margin, r.s(300), dim)
	} else {
		r.text(r.body, tr("plex_waiting"), r.margin, r.s(230), dim)
	}
	if l.msg != "" {
		r.text(r.body, r.fit(r.body, l.msg, room), r.margin, r.s(355), amber)
	}
	r.labelButton(r.plexCancel(), tr("cancel"), btnSecondary)
}

// plexGesture takes a tap while the card is up, and reports whether it did.
func (d *Display) plexGesture(g touch.Gesture) bool {
	if !plexLinkOpen() {
		return false
	}
	if g.Kind != touch.Tap || d.r == nil {
		return true // the card takes everything while it is up; only taps do anything
	}
	if image.Pt(g.X, g.Y).In(d.r.plexCancel()) {
		closePlexLink(true)
	}
	d.wake()
	return true
}
