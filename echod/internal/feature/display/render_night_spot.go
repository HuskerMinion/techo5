//go:build spot

package display

import (
	"image"
	"image/color"
	"math"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
)

// The night clock on the round face: the time alone on black, in the Show's three looks
// (render_night.go) laid out for the circle - the time in dim red, an LED clock's red segments, or a
// flip clock's charcoal cards. No rim, no date, no weather: the panel is at the night light's level, and
// nothing on it may light the room.

// nightClockFace is whether the face is the red night clock: the night's glow (redClock) with nothing
// that has to be seen over it. A ring stays under it, as on the Show, and a tap anywhere stops it; a
// call, a turn, the pages that ask for an answer, an announcement, a reminder, a notification a touch
// has released, a camera, and what a touch opened (the menu, the settings) are shown in its place at
// the same low light, and the clock comes back after them.
func (s roundScene) nightClockFace() bool {
	return s.redClock && s.phase == "idle" && !s.showVideo && s.call.Phase == phone.Idle && !s.pin.open &&
		!s.setupAsking && !s.showVideoAsk && !s.announceRecording && !s.showReminder && !s.showAnnouncement &&
		!(s.showNotification && !s.nightInfo) && !s.showAlert && !s.sheetOpen && !s.menuOpen && !s.showCamera && !s.btPairing
}

// nightFace draws the night clock in its style.
func (r *roundRenderer) nightFace(s roundScene) {
	fillCanvas(r.dst, color.Black)
	if s.nightInfo && s.showNotification {
		// Night clock shows Next up and notifications: the card takes the face, as by day, in the
		// night's tone (night_info.go), and the clock comes back once it is tapped away.
		layer := nightLayerFor(&r.nightBuf, r.dst.Rect)
		screen := r.dst
		r.dst = layer
		r.notifyFace(s)
		r.dst = screen
		nightToneOnto(screen, layer, layer.Rect)
		return
	}
	switch s.redStyle {
	case nightStyleLED:
		r.nightLED(s.now)
	case nightStyleFlip:
		r.nightFlip(s.now)
	default:
		r.nightPlain(s.now)
	}
	if s.muted {
		// The rim is the only place the mute shows on the Spot (mutedRim), so it is said here too: thin, as
		// the subtle ring is, and in the night's red, which lights nothing.
		r.arc(rimOut-mutedSoftWidth, rimOut, 0, 2*math.Pi, nightRed)
	}
}

// nightAMPM is the AM or PM under the night clock, centered at baseline, in c.
func (r *roundRenderer) nightAMPM(now time.Time, baseline int, c color.RGBA) {
	if ampm := clockSuffix(now); ampm != "" {
		r.centered(r.styleFace(true, 34), ampm, baseline, c)
	}
}

// nightPlain is the time in dim red, as large as the circle holds it, the AM or PM under it.
func (r *roundRenderer) nightPlain(now time.Time) {
	hm := clockHM(now)
	f := r.fitted(true, 160, hm, 400)
	h := capHeightOf(f)
	base := center + h/2
	if clockSuffix(now) != "" {
		base -= 24 // room under it for the AM or PM
	}
	r.centered(f, hm, base, nightRed)
	r.nightAMPM(now, base+64, nightRed)
}

// nightLED is the time as an LED clock's seven-segment digits, the unlit segments faintly behind.
func (r *roundRenderer) nightLED(now time.Time) {
	h, m := hourMinute(now)
	digits := []int{-1, -1, -1, -1}
	if len(h) == 2 {
		digits[0] = int(h[0] - '0')
	}
	digits[1] = int(h[len(h)-1] - '0')
	if len(m) == 2 {
		digits[2], digits[3] = int(m[0]-'0'), int(m[1]-'0')
	}
	const dw, dh, t = 66.0, 140.0, 14.0
	gap, colon := dw*0.42, t*2
	total := 4*dw + 3*gap + colon
	x, top := float64(center)-total/2, float64(center)-dh/2
	if clockSuffix(now) != "" {
		top -= 22
	}
	for i, d := range digits {
		r.segmentDigitAt(x, top, dw, dh, t, d, nightRed, nightGhost)
		x += dw + gap
		if i == 1 {
			cx := x - gap/2 + colon/2
			r.discAt(cx, top+dh*0.3, t*0.6, nightRed)
			r.discAt(cx, top+dh*0.7, t*0.6, nightRed)
			x += colon
		}
	}
	r.nightAMPM(now, int(top+dh)+60, nightRed)
}

// nightFlip is the time on a flip clock's charcoal cards, the figures a dim off-white, the AM or PM
// under them. A 12-hour clock before ten has three cards, as the Flip clock style does.
func (r *roundRenderer) nightFlip(now time.Time) {
	h, m := hourMinute(now)
	cards := []string{}
	if len(h) == 2 {
		cards = append(cards, h[:1])
	}
	cards = append(cards, h[len(h)-1:], m[:1], m[1:])
	const cw, ch, gap, apart = 84, 168, 12, 38
	total := len(cards)*cw + (len(cards)-2)*gap + apart
	x, y := center-total/2, center-ch/2
	if clockSuffix(now) != "" {
		y -= 20
	}
	f := r.styleFace(true, 132)
	for i, c := range cards {
		b := image.Rect(x, y, x+cw, y+ch)
		mid := y + ch/2
		r.roundFill(image.Rect(x, y, x+cw, mid), 10, flipTop, flipTop)
		r.roundFill(image.Rect(x, mid, x+cw, y+ch), 10, flipLower, flipLower)
		r.cardFigure(f, c, image.Rect(x, y+ch*12/100, x+cw, y+ch-ch*12/100), flipInk)
		r.line(float64(x), float64(mid), float64(x+cw), float64(mid), 3, flipSplit)
		r.roundFill(image.Rect(b.Min.X-4, mid-ch/18, b.Min.X+2, mid+ch/18), 2, flipHinge, flipHinge)
		r.roundFill(image.Rect(b.Max.X-2, mid-ch/18, b.Max.X+4, mid+ch/18), 2, flipHinge, flipHinge)
		x += cw + gap
		if i == len(cards)-3 {
			// The colon between the hours and the minutes, in the figures' ink.
			cx := float64(x - gap + apart/2)
			r.discAt(cx, float64(y+ch*36/100), 4, flipInk)
			r.discAt(cx, float64(y+ch*64/100), 4, flipInk)
			x += apart - gap
		}
	}
	r.nightAMPM(now, y+ch+56, flipInk)
}
