//go:build !dot && !spot

package display

import "image"

// The page that answers a browser asking to be let into the setup page.
//
// A press on the device is what proves somebody is standing at it, and the Dot has a button for
// that. The Show has volume and mute and nothing else — so the screen said "press the action
// button", which is a button it does not have, and a browser could ask for ever with nothing on the
// device able to say yes. This is that press: over whatever was on the screen, with the refusal
// where the eye lands first and the two answers far enough apart that neither is given by accident.

// askButtonsTop is where the answers begin, and the line above which a tap decides nothing — the
// same arrangement the ringing page uses.
const askButtonsTop = 330

func (r *renderer) setupAskPage(s scene) {
	r.text(r.title, "Setup page", r.margin, 96, amber)
	r.text(r.body, "A browser is asking to be let in", r.margin, 176, cream)
	r.text(r.small, "Allow it only if that browser is yours.", r.margin, 224, dim)

	y0, y1 := askButtonsTop, r.h-30
	no := image.Rect(r.margin, y0, r.w/2-12, y1)
	yes := image.Rect(r.w/2+12, y0, r.w-r.margin, y1)

	r.bevel(no, shift(ember, 16), true)
	r.text(r.body, "Not now", no.Min.X+(no.Dx()-r.width(r.body, "Not now"))/2, y0+72, cream)

	r.bevel(yes, amber, true)
	r.text(r.title, "Allow", yes.Min.X+(yes.Dx()-r.width(r.title, "Allow"))/2, y0+76, walnut)
}

// askTap is which answer a tap gave: allow, and whether it answered at all. A tap on the words above
// the buttons answers nothing, so reading the page cannot let somebody in.
func askTap(x, y, width int) (allow, answered bool) {
	if y < askButtonsTop-20 {
		return false, false
	}
	return x >= width/2, true
}
