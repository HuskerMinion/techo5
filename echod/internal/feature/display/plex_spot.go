//go:build spot

package display

import (
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// The Plex link on the Spot: the code across the middle of the face, where to type it under it, and
// Cancel at the top where a thumb reaches it.

func (r *roundRenderer) plexFace(l linkView) {
	r.centered(r.small, tr("cancel"), pinCancelY, colAccent)
	r.centered(r.small, "Plex", 112, colText)
	if l.code != "" {
		face := r.clock
		if r.width(face, l.code) > 260 {
			face = r.title
		}
		r.centered(face, l.code, 250, colText)
		r.centered(r.small, r.fit(r.small, tr("plex_go_to_link"), 320), 300, colDim)
	} else {
		r.centered(r.body, r.fit(r.body, tr("plex_waiting"), 320), 250, colDim)
	}
	if l.msg != "" {
		r.centered(r.small, r.fit(r.small, l.msg, 320), 340, colTimer)
	}
}

// plexGesture takes a tap while the card is up, and reports whether it did.
func (d *Display) plexGesture(g touch.Gesture) bool {
	if !plexLinkOpen() {
		return false
	}
	if g.Kind != touch.Tap {
		return true
	}
	if g.Y < pinCancelY+20 {
		closePlexLink(true)
	}
	d.wake()
	return true
}
