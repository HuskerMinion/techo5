//go:build spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
)

// The Spot's cue (turn_cue.go): the rim says the turn as it always has (rim), over the face that was
// up rather than the conversation's, and the captions sit in a shaded band across the lower face.
// Over the night clock, which has no rim, a thin red one.

// captionTop is the captions' first baseline, low on the face where the circle is still wide;
// spotCaptionLines the most lines they take.
const (
	captionTop       = 330
	spotCaptionLines = 3
)

// captionShade is the band under the captions, as the Show's shade is under words over a picture.
var captionShade = color.RGBA{0x00, 0x00, 0x00, 0x90}

// cueFace is whether a cue goes over the face the frame drew: a face to be looked at (the clock, the
// photos, the light before an alarm, the music, the dashboard, the night clock), and not one that is
// waiting on an answer or has taken the face for something else, which says what it is doing itself.
func cueFace(s roundScene) bool {
	if s.cue == "" {
		return false
	}
	if s.nightClockFace() {
		return !s.ringing.any()
	}
	return s.call.Phase == phone.Idle && !s.ringing.any() && !s.pin.open && !s.setupAsking && !s.showVideoAsk &&
		!s.announceRecording && !s.showReminder && !s.showAnnouncement && !(s.showNotification && !s.showCamera) &&
		!(s.showAlert && !s.showCamera) && !s.sheetOpen && !s.showCamera && !s.showVolume && !s.menuOpen && !s.btPairing
}

// turnCue draws what a cue turn puts over the face. The rim is drawn again here, last, because a
// photo, the sunrise and the like paint the whole face, the rim drawn before them included.
func (r *roundRenderer) turnCue(s roundScene) {
	if !cueFace(s) {
		return
	}
	switch {
	case !cueLit(s.cue):
	case s.nightClockFace():
		if !s.muted {
			pulse := 0.55 + 0.45*math.Sin(float64(s.now.UnixMilli())/180)
			r.arc(rimOut-mutedSoftWidth, rimOut, 0, 2*math.Pi, fade(nightRed, pulse))
		}
	default:
		if s.slideshowScreensaver != nil || s.slideshow != nil || s.sunrise > 0 {
			// Over a picture a dark ring goes under the rim, a little wider than it, so the listening
			// rim's pale pulse and the others read on a light photo as on the dark face.
			r.arc(rimIn-5, side/2, 0, 2*math.Pi, color.RGBA{0, 0, 0, 0xb0})
		}
		r.rim(s)
	}
	text := cueWords(s.cue, s.heard, s.reply)
	if text == "" {
		return
	}
	c := colText
	if s.nightClockFace() {
		c = nightRed
	}
	strip := image.Rect(0, captionTop-30, side, captionTop+76)
	draw.Draw(r.dst, strip, image.NewUniform(captionShade), image.Point{}, draw.Over)
	r.setCaptionsAt(strip)
	r.paragraph(r.small, text, captionTop, c, spotCaptionLines)
}
