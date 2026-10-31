//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// The sunrise page: what the screen shows while the light before an alarm comes up.
//
// The panel is the only lamp in the room, so what is drawn matters more than the number the
// backlight is set to: a screen filled with warm color puts out far more light than a clock on a
// black background at the same brightness. The sky warms from the dark red of the first minute
// through orange to a pale gold, a sun climbs into it on the same curve as the light, and the time
// stays readable the whole way, because the first thing anybody does is squint at it.

// sunriseFace is whether the sun is drawn with a face on it, which is a matter of taste rather than
// of waking up. See config.Alarms.SunriseFace.

// sunrisePage draws the sky, the sun and the clock over it. p is 0 to 1, as sunriseProgress reports.
func (r *renderer) sunrisePage(s scene, p float64, face bool) {
	p = math.Min(math.Max(p, 0), 1)
	r.sunriseSky(p)
	r.sunriseSun(p, face)
	r.sunriseClock(s, p)
	if s.skip != nil && !skipOccluded(s) && !r.rainbowCovering(s) {
		// The volume bar along the foot is drawn over this page, and it covers the button: what cannot
		// be seen cannot be tapped, as the camera page's controls have it (render.go). The bar is up for
		// two seconds after the level last moved; the change wakes the frame loop, and the frame that
		// draws the bar is the one that drops the button and the place a tap was matched to with it.
		// While the bar is up the frames come every activeFrame rather than once a second, so the
		// button is back on the screen, and tappable, within a fifth of a second of the bar going.
		//
		// A card, a strip, the drawer or the update dialog over the page, and Rainbow's own drawing over
		// it while a turn is composed or fading, leave it out the same way (skipOccluded,
		// rainbowCovering): a button under them is one somebody never saw, and the fade had it covered.
		r.sunriseSkipButton(*s.skip, p)
	}
}

// sunriseSky fills the screen, dark red at the start and pale gold by the end, with the horizon
// lighter than the top so the light seems to come from below.
func (r *renderer) sunriseSky(p float64) {
	top := lerpRGB(color.RGBA{26, 8, 6, 255}, color.RGBA{232, 158, 78, 255}, p)
	bottom := lerpRGB(color.RGBA{74, 16, 8, 255}, color.RGBA{255, 214, 140, 255}, p)
	for y := 0; y < r.h; y++ {
		f := float64(y) / float64(r.h-1)
		row := lerpRGB(top, bottom, f)
		draw.Draw(r.dst, image.Rect(0, y, r.w, y+1), image.NewUniform(row), image.Point{}, draw.Src)
	}
}

// sunriseSun climbs from below the bottom edge toward the middle, growing and paling as it comes:
// deep red at the horizon, gold by the time it is up, as a real one does.
func (r *renderer) sunriseSun(p float64, face bool) {
	// It stops short of the clock: the time has to stay readable, which matters more than the sun
	// getting all the way up.
	rad := int(float64(r.h) * (0.15 + 0.07*p))
	cy := int(float64(r.h)*1.12 - float64(r.h)*0.40*p)
	cx := r.w / 2
	body := lerpRGB(color.RGBA{219, 74, 28, 255}, color.RGBA{255, 246, 205, 255}, p)

	// A soft edge: rings of the sun's color, each a little wider and fainter than the last, so it
	// sits in the sky rather than being cut out of it.
	for i := 6; i > 0; i-- {
		r.disc(cx, cy, rad+i*rad/8, faded(body, uint8(18+8*i)))
	}
	r.disc(cx, cy, rad, body)

	if !face {
		return
	}
	// The face, in the sun's own color darkened, so it is there without being a cartoon on a lamp.
	ink := color.RGBA{uint8(float64(body.R) * 0.35), uint8(float64(body.G) * 0.25), uint8(float64(body.B) * 0.2), 255}
	eye := max(rad/9, 3)
	r.disc(cx-rad/3, cy-rad/4, eye, ink)
	r.disc(cx+rad/3, cy-rad/4, eye, ink)
	// The smile: a row of discs along an arc, thicker in the middle.
	for i := -6; i <= 6; i++ {
		t := float64(i) / 6
		x := cx + int(t*float64(rad)*0.52)
		y := cy + int(float64(rad)*(0.18+0.30*(1-t*t)))
		r.disc(x, y, max(rad/14, 2), ink)
	}
}

// sunriseClock is the time over the sky: dark ink once the sky is light, pale while it is still dim,
// so it can be read at either end.
func (r *renderer) sunriseClock(s scene, p float64) {
	ink := lerpRGB(color.RGBA{255, 236, 214, 255}, color.RGBA{58, 22, 10, 255}, math.Min(p*1.6, 1))
	hour := clockHM(s.now)
	ampm := clockSuffix(s.now)
	hw := r.width(r.clock, hour)
	aw := r.width(r.ampm, ampm)
	gap := r.s(18)
	if ampm == "" {
		gap = 0
	}
	x := (r.w - hw - gap - aw) / 2
	base := r.h/2 - r.s(56)
	r.text(r.clock, hour, x, base, ink)
	r.text(r.ampm, ampm, x+hw+gap, base, ink)
	date := s.now.Format("Monday, January 2")
	r.text(r.small, date, (r.w-r.width(r.small, date))/2, base+r.s(64), ink)
}

// sunriseSkipButton draws the way to leave today's alarm out while the light comes up before it: what
// a press does, and the alarm and the moment it would leave out, so that the finger knows what it is
// about without a list to look at. It sits between the date and the foot, clear of the clock, and is
// drawn dark on a sky that goes from the dark red of the first minutes to a pale gold: the page is the
// only lamp in the room, and what is on it has to read at both ends of the light.
//
// Where it was drawn, and the occurrence it offered, are kept for the tap (setSkipDrawn): a finger
// acts on what it saw, not on what the alarms say by the time it lands.
func (r *renderer) sunriseSkipButton(offer skipOffer, p float64) {
	label := offer.label
	if label == "" {
		label = locale.Alarm(screenLang(), false)
	}
	action := skipAction()
	// A pill around both lines, measured from the faces in hand: a screen font of the owner's is not
	// the same height as Go's, and a line that overflows the button it is on reads as a mistake.
	padX, padY, gap := r.s(48), r.s(12), r.s(6)
	am, sm := r.title.Metrics(), r.small.Metrics()
	wide := r.w - 2*r.margin - 2*padX
	action = r.clipTo(r.title, action, wide)
	what := r.skipWhat(label, offer.token.At, wide)
	w := max(r.width(r.title, action), r.width(r.small, what)) + 2*padX
	tall := am.Ascent.Ceil() + am.Descent.Ceil() + gap + sm.Ascent.Ceil() + sm.Descent.Ceil() + 2*padY
	b := image.Rect((r.w-w)/2, r.h-r.s(64)-tall, (r.w+w)/2, r.h-r.s(64))
	rad := min(float64(r.s(30)), float64(b.Dy())/2)
	r.roundFill(b, rad, color.RGBA{0x2b, 0x13, 0x0b, 0xff}, color.RGBA{0x17, 0x09, 0x05, 0xff})
	// The edge is warm against the dark end of the sky and deepens as the sky pales, so the button
	// keeps its shape on the way up rather than glowing into the gold.
	r.roundStroke(b, rad, r.sf(2), lerpRGB(color.RGBA{0xf0, 0xa9, 0x3f, 0xff}, color.RGBA{0x7a, 0x33, 0x12, 0xff}, p))
	titleY := b.Min.Y + padY + am.Ascent.Ceil()
	whatY := titleY + am.Descent.Ceil() + gap + sm.Ascent.Ceil()
	r.text(r.title, action, b.Min.X+(b.Dx()-r.width(r.title, action))/2, titleY, color.RGBA{0xff, 0xf6, 0xe8, 0xff})
	r.text(r.small, what, b.Min.X+(b.Dx()-r.width(r.small, what))/2, whatY, color.RGBA{0xf3, 0xc3, 0x8a, 0xff})
	// The captions of a cue sit above the page's controls (footAt, turn_cue.go), and this is one: the
	// foot's room is taken from the top of the finger's area rather than the button's pixels, since a
	// tap just outside the button is still the button's (skipTapped) and a caption over that ring would
	// take the press.
	r.footControls(b.Min.Y - r.s(8))
	r.setSkipDrawn(b, offer)
}

// sunriseSkipNote draws the foot's line about the last press on the button above it: the same dark
// pill and the same pale ink. The foot's amber is read on the clock's ground, but not on the sky below
// the button, which goes from the dark red of the first minutes to a pale gold that swallows it — and
// this line is the only answer a press gets, so it is drawn dark on light the whole way up. room is
// what the foot has for it beside the word at the right end, padding included: the line is fitted to
// the pill at the face it is drawn in — the alarm's name shortened, the result and the hour kept — and
// the pill is measured from the line, so it stays on the page rather than running off it.
func (r *renderer) sunriseSkipNote(a skipAnswer, p float64, room int) {
	base := r.h - r.s(24)
	am := r.tiny.Metrics()
	padX, padY := r.s(14), r.s(7)
	line := r.skipLine(a, r.tiny, room-2*padX)
	b := image.Rect(r.margin, base-am.Ascent.Ceil()-padY, r.margin+r.width(r.tiny, line)+2*padX,
		base+am.Descent.Ceil()+padY)
	rad := float64(b.Dy()) / 2
	r.roundFill(b, rad, color.RGBA{0x2b, 0x13, 0x0b, 0xff}, color.RGBA{0x17, 0x09, 0x05, 0xff})
	r.roundStroke(b, rad, r.sf(2), lerpRGB(color.RGBA{0xf0, 0xa9, 0x3f, 0xff}, color.RGBA{0x7a, 0x33, 0x12, 0xff}, p))
	r.text(r.tiny, line, r.margin+padX, base, color.RGBA{0xff, 0xf6, 0xe8, 0xff})
}

// skipWhat is the button's second line: the alarm the button is about and the moment it would leave
// out. An alarm's name has no length limit, and a translated line is not always as short as the
// English one, so where the two do not fit together it is the name that is clipped, to the room left
// beside the moment. Clipping the whole line instead would cut the hour off first, and the hour is
// what says which occurrence a press is about.
func (r *renderer) skipWhat(label string, at time.Time, wide int) string {
	moment := " · " + clockText(at)
	room := wide - r.width(r.small, moment)
	if room <= r.width(r.small, "…") {
		// If even an elided name will not fit, the identifying time takes priority.
		return r.clipTo(r.small, clockText(at), wide)
	}
	return r.clipTo(r.small, label, room) + moment
}

// faded is a color at a given alpha, premultiplied — which is what Go's RGBA holds, so dimming the
// alpha alone leaves a color that is not the one that was asked for.
func faded(c color.RGBA, a uint8) color.RGBA {
	f := float64(a) / 255
	return color.RGBA{uint8(float64(c.R) * f), uint8(float64(c.G) * f), uint8(float64(c.B) * f), a}
}

// lerpRGB mixes two colors, t from 0 (a) to 1 (b).
func lerpRGB(a, b color.RGBA, t float64) color.RGBA {
	t = math.Min(math.Max(t, 0), 1)
	mix := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
	return color.RGBA{mix(a.R, b.R), mix(a.G, b.G), mix(a.B, b.B), 255}
}
