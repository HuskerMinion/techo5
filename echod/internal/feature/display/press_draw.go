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

// buttonFacePressed is buttonFace for such a button: drawn pressed or not as on says, wherever the
// finger is.
func (r *paint) buttonFacePressed(b image.Rectangle, rad float64, style buttonStyle, on bool) color.RGBA {
	was := r.press
	r.press.on, r.press.at = on, b.Min
	fg := r.buttonFace(b, rad, style)
	r.press.on, r.press.at = was.on, was.at
	return fg
}
