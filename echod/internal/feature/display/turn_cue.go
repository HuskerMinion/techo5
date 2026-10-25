//go:build !dot

package display

import (
	"image"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// The cue turn screens, Cue light and Cue and captions (turnStyles): the page on the screen stays as
// it was through a voice turn, and the turn is a light along the edge of the screen (the rim on the
// Spot) in the turn's colors. With captions, what was heard and the answer are laid over the foot of
// the page as well, cut short when they run long. For somebody who wants the dashboard, the music or
// the photos left alone while they talk, and for somebody who wants to see the answer as well as hear
// it.
//
// Only over a page that is there to be looked at: the clock, the photos, the dashboard, the deck, the
// music and the night clock. A page waiting on an answer from somebody (a call, a ring, the PIN, a
// browser or a video asking, the settings, a camera) takes its taps where it drew its buttons, so no
// captions are laid over it, as the other turn screens are not drawn over it either.
//
// The page keeps its taps through a cue: it is on the screen, and whoever touches it is using it. A
// tap on the captions puts them away, as a tap on an answer does; the turn itself is stopped by voice
// or the stop word.

// cueFrame is how often a lit cue is drawn again at the most: ten a second, as the wave is, for the
// light breathing and running. A page that moves faster keeps its own pace.
const cueFrame = 100 * time.Millisecond

// cueOn is whether turns are drawn as a cue over the page: Rainbow is one too, drawn its own way
// (render_rainbow.go) except at night, when it is Cue and captions. Where the device does not offer it,
// a saved Rainbow reads as Classic (turnStyleIndex).
func cueOn() bool {
	v := turnStyles[turnStyleIndex()].value
	return v == "cue" || v == "captions" || v == "rainbow"
}

// captionsOn is whether the cue carries the words.
func captionsOn() bool {
	v := turnStyles[turnStyleIndex()].value
	return v == "captions" || v == "rainbow"
}

// rainbowOn is whether turns are drawn as Rainbow.
func rainbowOn() bool { return turnStyles[turnStyleIndex()].value == "rainbow" }

// pageIdle is whether the page is up for a turn in the phase given: idle, or any phase under a cue,
// which leaves the page up. What the touch side asks before handing a finger to a page, so it is
// the same page the frame drew.
func pageIdle(phase string) bool { return phase == "idle" || cueOn() }

// cueTurn takes a turn off the page when turns are a cue: page is the phase the page is drawn for,
// idle, so whatever was up stays up; cue is the turn's own phase, for the light; and the words are
// kept only for captions. Outside a turn it changes nothing, and nor does a finished one with no
// captions: with the light off and no words, there is nothing of it left on the screen.
func cueTurn(phase, heard, reply string, captions bool) (cue, page, h, r string) {
	if phase == "idle" || phase == "" || (phase == "lingering" && !captions) {
		return "", "idle", "", ""
	}
	if !captions {
		heard, reply = "", ""
	}
	return phase, "idle", heard, reply
}

// cueLit is whether the light is on for a cue: while the device listens, thinks and answers, and not
// while the answer's words linger after it.
func cueLit(cue string) bool { return cue == "listening" || cue == "thinking" || cue == "replying" }

// cueWords is what the captions say for a cue: the answer once it is being given, and what was
// heard until then.
func cueWords(cue, heard, reply string) string {
	if reply != "" && (cue == "replying" || cue == "lingering") {
		return reply
	}
	return heard
}

// footControls says the page drew controls along its foot from y down: the captions go above them.
func (r *paint) footControls(y int) {
	if r.footAt == 0 || y < r.footAt {
		r.footAt = y
	}
}

// setCaptionsAt keeps where the captions were drawn, for a tap: empty for a frame with none.
func (r *paint) setCaptionsAt(b image.Rectangle) {
	r.zmu.Lock()
	r.captionsAt = b
	r.zmu.Unlock()
}

// captionsTapped is whether a tap at p landed on the captions as last drawn.
func (r *paint) captionsTapped(p image.Point) bool {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	return !r.captionsAt.Empty() && p.In(r.captionsAt)
}

// captionsTap is a tap on the captions: it puts the words away, as a tap on an answer does, and is not
// a tap on the page under them. Anywhere else the page has the tap.
func (d *Display) captionsTap(g touch.Gesture) bool {
	if g.Kind != touch.Tap || d.r == nil || !d.r.captionsTapped(image.Pt(g.X, g.Y)) {
		return false
	}
	d.clearAnswer()
	return true
}
