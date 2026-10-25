//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
)

// The Show's cue (turn_cue.go): a band of the accent color along the top of the screen, with a
// microphone hanging from it while the device listens, and the captions in a shaded strip at the
// foot of the page.

// cueBand is the light's height, in the Show 5's pixels; cueGlow how far its soft edge reaches below
// it; captionLines the most lines the captions take.
const (
	cueBand      = 12
	cueGlow      = 10
	captionLines = 4
)

// cueOver is what of a cue goes over the page the frame drew. The light goes over a page that is there
// to be looked at, a card or a strip on it included, since those are in the middle and along the foot;
// the captions only where no card or strip of words is up. Over the controls along a page's foot - the
// music strip, the Now Playing transport, the Tessera page's dots - the captions sit above them
// (footAt), so those still take their taps. A page of its own with buttons on it, and the pages a tap
// opened, get neither: they say what they are doing themselves, as they do under the other turn
// screens, and the captions would be over what they take taps on.
func cueOver(s scene) (light, words bool) {
	if s.cue == "" {
		return false, false
	}
	if s.redClockFace() {
		// A ring under the night clock is stopped by a tap anywhere; nothing goes over that.
		return !s.ring.any(), !s.ring.any()
	}
	if s.call.Phase != phone.Idle || s.ring.any() || s.pin.open || s.setupAsking || s.showVideoAsk || s.bt.Pairing ||
		s.showWifi || s.showSheet || s.showCamera || s.showDrawer || s.showAlert || s.showCalendar || s.showRadar ||
		s.showWeather || s.announceRecording {
		return false, false
	}
	words = !s.showAnnouncement && !s.showReminder && !s.showNotification && s.popup == nil && !s.showVolume
	return true, words
}

// turnCue draws a cue turn over whatever the frame turned out to be.
func (r *renderer) turnCue(s scene) {
	if rainbowHere(s) {
		r.rainbowTurn(s)
		return
	}
	light, words := cueOver(s)
	if light && cueLit(s.cue) {
		r.cueLight(s)
	}
	if text := cueWords(s.cue, s.heard, s.reply); words && text != "" {
		r.captions(s, text)
	}
}

// cueColor is the light's and the captions' color: the accent, and the night's red over the night
// clock, as the clock is.
func cueColor(s scene, day color.RGBA) color.RGBA {
	if s.redClockFace() {
		return nightRed
	}
	return day
}

// cueTop is where the band starts: the top edge, or under the console's top bar where LCARS drew it,
// around the home clock, where a band of the accent along the edge would be one more of its segments.
// Over the music, a dashboard or the photos there is no bar, and the band is along the edge.
func (r *renderer) cueTop() int {
	if r.lcarsDrawn {
		return r.s(lcarsEdge + lcarsBarH + 4)
	}
	return 0
}

// cueLight is the band: breathing while the device listens, with a microphone hanging from its
// middle so it cannot be taken for anything else; a soft stretch running along it while it thinks; and
// steady while it answers. A soft shadow under it keeps it apart from a bright photo, and the
// "microphone off" pill, when it is up, keeps its place in the middle.
func (r *renderer) cueLight(s scene) {
	c := cueColor(s, amber)
	top, h := r.cueTop(), r.s(cueBand)
	band := image.Rect(0, top, r.w, top+h)
	for i, n := 0, r.s(cueGlow); i < n; i++ {
		a := uint8(0x70 * (n - i) / n)
		draw.Draw(r.dst, image.Rect(0, band.Max.Y+i, r.w, band.Max.Y+i+1), image.NewUniform(color.RGBA{0, 0, 0, a}), image.Point{}, draw.Over)
	}
	since := float64(s.now.Sub(s.since).Milliseconds())
	switch s.cue {
	case "listening":
		// With the period of the listening title's bar (status): 1.6 s. The tab first, so the band
		// covers its top.
		r.cueMic(c, band)
		k := 0.6 + 0.4*math.Sin(2*math.Pi*since/1600)
		draw.Draw(r.dst, band, image.NewUniform(color.Black), image.Point{}, draw.Src)
		draw.Draw(r.dst, band, image.NewUniform(faded(c, uint8(255*k))), image.Point{}, draw.Over)
	case "thinking":
		// A third of the width crossing the screen every two seconds, over the band at a quarter, its
		// ends soft so that a frame's step along reads as movement rather than a jump.
		draw.Draw(r.dst, band, image.NewUniform(color.Black), image.Point{}, draw.Src)
		draw.Draw(r.dst, band, image.NewUniform(faded(c, 0x50)), image.Point{}, draw.Over)
		seg := r.w / 3
		x0 := int(float64(r.w+seg)*math.Mod(since/2000, 1)) - seg
		for x := max(x0, 0); x < min(x0+seg, r.w); x++ {
			a := math.Sin(math.Pi * float64(x-x0) / float64(seg))
			draw.Draw(r.dst, image.Rect(x, band.Min.Y, x+1, band.Max.Y), image.NewUniform(faded(c, uint8(255*a))), image.Point{}, draw.Over)
		}
	default:
		draw.Draw(r.dst, band, image.NewUniform(c), image.Point{}, draw.Src)
	}
	// The header's "microphone off" pill goes back over the band: it says more than the band does.
	if s.muted && !s.redClockFace() {
		r.header(s)
	}
}

// cueMic is the tab hanging from the middle of the band while the device listens, with a microphone on
// it: the band alone could be anything lit along the top of a page. Drawn before the band, which
// covers its rounded top.
func (r *renderer) cueMic(c color.RGBA, band image.Rectangle) {
	w, rad := r.s(44), r.s(10)
	// In the middle, clear of the weather and the badges in the corners. The "microphone off" pill is
	// there too, but only while muted, when the device does not listen.
	x := (r.w - w) / 2
	tab := image.Rect(x, band.Min.Y, x+w, band.Max.Y+r.s(36))
	r.roundShadow(tab, float64(rad), r.sf(8), r.s(3), 0.5)
	r.roundFill(tab, float64(rad), c, c)
	r.mdiIconIn("microphone", image.Rect(tab.Min.X, band.Max.Y, tab.Max.X, tab.Max.Y), 28, color.RGBA{0x14, 0x0e, 0x0a, 0xff})
}

// captions lays text over the foot of the page on a shaded strip, light on dark whatever the theme,
// as words over a photo are; red over the night clock. Above the controls along the foot when the page
// has them (footAt). A longer answer is cut after captionLines lines,
// the last ending in an ellipsis, and a word too long for a line is cut the same way.
func (r *renderer) captions(s scene, text string) {
	width := r.w - 2*r.margin
	lines := r.wrap(r.small, text, width)
	if len(lines) > captionLines {
		lines = lines[:captionLines]
		lines[captionLines-1] = r.clipTo(r.small, lines[captionLines-1]+"…", width)
	}
	for i, l := range lines {
		lines[i] = r.clipTo(r.small, l, width)
	}
	lineH := r.s(42)
	bottom := r.h
	if r.footAt > 0 {
		bottom = r.footAt
	}
	top := bottom - len(lines)*lineH - r.s(16)
	strip := image.Rect(0, top, r.w, bottom)
	under := shade
	if s.redClockFace() {
		// Darker at night: red words over the red clock's digits are hard to tell apart.
		under = color.RGBA{0, 0, 0, 0xe0}
	}
	draw.Draw(r.dst, strip, image.NewUniform(under), image.Point{}, draw.Over)
	r.setCaptionsAt(strip)
	onPhoto(func() {
		c := cueColor(s, cream)
		y := top + r.s(36)
		for _, l := range lines {
			r.text(r.small, l, r.margin, y, c)
			y += lineH
		}
	})
}
