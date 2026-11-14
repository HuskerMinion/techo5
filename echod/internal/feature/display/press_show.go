//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
)

// The press on what is not one of the program's buttons: a row or a tab of the settings and the drawer,
// a dashboard's tile. Those are filled in more ways than a button is, so the press is laid over them
// once they are drawn (press_draw.go has the buttons').

// pressOver shows the press on something already drawn in b, whose words are in ink: for a tile or a
// row, which is filled in more ways than a button is.
func (r *paint) pressOver(b image.Rectangle, rad float64, ink color.RGBA) {
	rad = min(rad, float64(min(b.Dx(), b.Dy()))/2)
	r.glassFill(b, rad, ink, pressTint, pressTint)
}

// pressZone shows the press on the zone under the finger, of those the frame in hand has drawn so far:
// the settings' and the drawer's rows, tabs and buttons. The zone is found as a tap finds it (zoneAt),
// so what shows pressed is what the tap would land on. Not one that is most of the screen: the dimmed
// clock beside the drawer and the ground under an open list are places to tap, not things to press.
func (r *paint) pressZone() {
	if !r.press.on || r.press.shown {
		return
	}
	z, ok := zoneIn(r.pending, r.press.at, r.s(zoneSlop))
	if !ok || z.kind == zoneDismiss || z.r.Dy() > r.h/2 {
		return
	}
	r.pressOver(z.r, r.sf(10), cream)
}

// pressTile shows the press on the dashboard tile under the finger, of those the page just drew, in the
// page's own colors. The first tile holding the point, as a tap finds it (tileAt). Not a way to another
// page (a dot, the way back), which is no tile.
func (r *paint) pressTile(pal dashPal) {
	if !r.press.on {
		return
	}
	tiles, _ := r.dash()
	for _, t := range tiles {
		if !r.press.at.In(t.r) {
			continue
		}
		if t.page == 0 {
			r.pressOver(t.r, pal.rad, pal.text)
		}
		return
	}
}
