//go:build !dot

package display

import (
	"image"
	"image/color"
)

// A press shown under the finger. Nearly everything on these screens acts when the finger lifts, and
// until then nothing said the screen had felt it: a slow press on Stop or on a tile looked like a
// screen that was not listening. The control under a finger is drawn a little toward the color of its
// own words while the finger is down and for a moment after, and plain again after that. It is only a
// look: what a tap lands on and what it does are decided where they always were.

// pressTint is how far a pressed control's fill goes toward the color of its words: enough to see on
// every theme, the light ones included, and too little to read as the control being on.
const pressTint = 0.16

// pressReach is how far outside a button a finger still shows on it, in the Show 5's pixels: less than
// half the least gap between two buttons, so a finger never shows on two.
const pressReach = 5

// pressView is the press a frame shows: where the finger came down, while there is one to show.
type pressView struct {
	on bool
	at image.Point
	// shown is a button of the frame in hand having taken the press, so the zone around it does not
	// show it a second time (pressZone).
	shown bool
	// seq is which press this is (pressState.seq), for letting go of it after the frame.
	seq uint64
}

// pressed is whether the finger of the frame in hand is on the button drawn in b.
func (r *paint) pressed(b image.Rectangle) bool {
	return r.press.on && r.press.at.In(b.Inset(-r.s(pressReach)))
}

// pressFill is fill for a button in b whose words are drawn in ink: fill itself, or with the finger on
// the button, fill gone a little toward ink.
func (r *paint) pressFill(b image.Rectangle, fill, ink color.RGBA) color.RGBA {
	return r.pressTinted(r.pressed(b), fill, ink)
}

// pressTinted is pressFill for a button whose page says for itself whether the finger is its: one that
// answers a tap made off it, as a ringing alarm's Stop answers one anywhere on its page.
func (r *paint) pressTinted(on bool, fill, ink color.RGBA) color.RGBA {
	if !on {
		return fill
	}
	r.press.shown = true
	return lerp(fill, ink, pressTint)
}

// The press on what is not one of the program's buttons: a row or a tab of the settings and the drawer,
// a dashboard's tile. Those are filled in more ways than a button is, so the press is laid over them
// once they are drawn.

// pressOver shows the press on something already drawn in b, whose words are in ink: for a tile or a
// row, which is filled in more ways than a button is.
func (r *paint) pressOver(b image.Rectangle, rad float64, ink color.RGBA) {
	r.press.shown = true
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
