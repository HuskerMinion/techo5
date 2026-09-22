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
//
// The two answers are drawn by the same code as every other button on these devices, buttonFace in
// sheet_widgets.go, so they are rounded and lit the way the Spot's are. They used to be square
// bevelled boxes and looked like they had come from a different program.

// Where the answers sit, in the Show 5's pixels: the space between them, the margin under them, how
// tall they are, and how far above them a tap still decides nothing.
const (
	askGap    = 24
	askBottom = 30
	askHeight = 120
	askReach  = 20
)

// askBand is the top and bottom of the answers. Anchored to the foot of the screen rather than
// measured from the top, so a taller panel sets them under the words instead of stretching them
// down the page.
func (r *renderer) askBand() (y0, y1 int) {
	y1 = r.h - r.s(askBottom)
	return y1 - r.s(askHeight), y1
}

func (r *renderer) setupAskPage(s scene) {
	r.text(r.title, "Setup page", r.margin, r.s(96), amber)
	r.text(r.body, "A browser is asking to be let in", r.margin, r.s(176), cream)
	r.text(r.small, "Allow it only if that browser is yours.", r.margin, r.s(224), dim)

	y0, y1 := r.askBand()
	half := r.s(askGap) / 2
	no := image.Rect(r.margin, y0, r.w/2-half, y1)
	yes := image.Rect(r.w/2+half, y0, r.w-r.margin, y1)

	// Rounded enough to read as a button at this size without becoming a lozenge.
	rad := float64(r.s(28))
	mid := (y0 + y1) / 2

	fg := r.buttonFace(no, rad, btnSecondary)
	r.text(r.body, "Not now", no.Min.X+(no.Dx()-r.width(r.body, "Not now"))/2, mid+r.s(14), fg)

	fg = r.buttonFace(yes, rad, btnPrimary)
	r.text(r.title, "Allow", yes.Min.X+(yes.Dx()-r.width(r.title, "Allow"))/2, mid+r.s(16), fg)
}

// askTap is which answer a tap gave: allow, and whether it answered at all. A tap on the words above
// the buttons answers nothing, so reading the page cannot let somebody in.
func (r *renderer) askTap(x, y int) (allow, answered bool) {
	y0, _ := r.askBand()
	if y < y0-r.s(askReach) {
		return false, false
	}
	return x >= r.w/2, true
}
