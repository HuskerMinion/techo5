//go:build !dot && !spot

package display

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
// beveled boxes and looked like they had come from a different program.

func (r *renderer) setupAskPage(s scene) {
	r.text(r.title, "Setup page", r.margin, r.s(96), amber)
	r.text(r.body, "A browser is asking to be let in", r.margin, r.s(176), cream)
	r.text(r.small, "Allow it only if that browser is yours.", r.margin, r.s(224), dim)

	no, yes := r.actionHalves()
	rad := float64(r.s(actionRadius))
	mid := (no.Min.Y + no.Max.Y) / 2

	fg := r.buttonFace(no, rad, btnSecondary)
	r.text(r.body, "Not now", no.Min.X+(no.Dx()-r.width(r.body, "Not now"))/2, mid+r.s(14), fg)

	fg = r.buttonFace(yes, rad, btnPrimary)
	r.text(r.title, "Allow", yes.Min.X+(yes.Dx()-r.width(r.title, "Allow"))/2, mid+r.s(16), fg)
}

// askTap is which answer a tap gave: allow, and whether it answered at all. A tap on the words above
// the buttons answers nothing, so reading the page cannot let somebody in.
func (r *renderer) askTap(x, y int) (allow, answered bool) {
	if !r.actionDecided(y) {
		return false, false
	}
	return x >= r.w/2, true
}
