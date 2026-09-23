//go:build spot

package display

// The face that answers a browser asking to be let into the setup page.
//
// The Spot has a volume dial and a mute button and no third one, so the press the setup page asks
// for had nowhere to happen: its face said "PRESS TO ALLOW SETUP" and there was nothing to press.
// Two buttons then, one above the other, since a round screen has its width in the middle and a row
// of two would put both answers where the glass curves away.

const (
	// askNoY and askYesY are the middles of the two answers. Refusal above, allow below: this face is
	// reached over rather than up at, and the answer that lets somebody in should not be the one a
	// hand lands on by falling.
	askNoY  = 270
	askYesY = 360

	// askReach is how far either side of a button's middle a tap still counts as that answer.
	askReach = 30
)

func (r *roundRenderer) setupAskFace(s roundScene) {
	r.centered(r.title, "Setup page", 130, colText)
	r.centered(r.small, "A browser is asking", 176, colDim)
	r.centered(r.small, "to be let in", 206, colDim)

	// pillButton is placed by its right edge; these are centered, so each is offset by half its own.
	fc := r.faces()
	right := func(label string) int { return center + (r.paint.width(fc.button, label)+44)/2 }
	r.pillButton(right("Not now"), askNoY, "Not now", btnSecondary)
	r.pillButton(right("Allow"), askYesY, "Allow", btnPrimary)
}

// askTapSpot is which answer a tap gave, and whether it answered at all: a tap on the words decides
// nothing, so reading the face cannot let anybody in.
func askTapSpot(y int) (allow, answered bool) {
	switch {
	case y >= askYesY-askReach && y <= askYesY+askReach:
		return true, true
	case y >= askNoY-askReach && y <= askNoY+askReach:
		return false, true
	}
	return false, false
}
