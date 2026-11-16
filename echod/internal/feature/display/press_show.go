//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
)

// The press on the Show's pages beyond its buttons and zones (press_draw.go): the clock's places to tap,
// the calendar's days and events, and the parts of the sheets over the dashboard. Each is found as the
// tap finds it, with the hit test the tap uses, and shown only where that tap does something.

// buttonFacePressed is buttonFace for such a button: drawn pressed or not as on says, wherever the
// finger is.
func (r *paint) buttonFacePressed(b image.Rectangle, rad float64, style buttonStyle, on bool) color.RGBA {
	was := r.press
	r.press.on, r.press.at = on, b.Min
	fg := r.buttonFace(b, rad, style)
	r.press.on, r.press.at = was.on, was.at
	return fg
}

// pressClock shows the press on the clock's place to tap under the finger: the alert badge, the weather,
// the date and the next events, which open what they show, and the footer's word for the music put
// away. Each is looked for as Display.gesture looks for it, in its order, with the rectangle its tap is
// matched against, and only where that tap opens something: the date opens nothing on a device that
// shows no calendar. The Call button and the strip are drawn over the clock and show their own.
func (r *renderer) pressClock(s scene) {
	if !r.press.on || r.press.shown {
		return
	}
	at := r.press.at
	if s.strip && at.In(r.stripRect()) || s.callButton && at.In(r.callButtonRect().Inset(-r.s(12))) {
		return
	}
	var b image.Rectangle
	switch {
	case r.badgeTapped(at):
		b = r.alertBadgeAt().Inset(-r.s(10))
	case r.weatherTapped(at):
		r.weatherMu.Lock()
		b = r.tempAt
		if !r.weatherAt.Empty() && at.In(r.weatherAt) {
			b = r.weatherAt
		}
		r.weatherMu.Unlock()
	case !calendarsShown():
	case r.dateTapped(at):
		r.weatherMu.Lock()
		b = r.dateAt
		r.weatherMu.Unlock()
	case r.nextCardAtPoint(at):
		r.weatherMu.Lock()
		b = r.nextCardAt
		r.weatherMu.Unlock()
	case r.nextTapped(at):
		r.weatherMu.Lock()
		b = r.nextAt
		r.weatherMu.Unlock()
	}
	if b.Empty() && s.musicAway && playingWord(s) != "" && at.In(r.playingButton()) && at.Y >= topEdge {
		b = r.playingButton()
	}
	if !b.Empty() {
		r.pressOver(b, r.sf(14), cream)
	}
}

// nextCardAtPoint is nextCardTapped without the day.
func (r *renderer) nextCardAtPoint(p image.Point) bool {
	_, ok := r.nextCardTapped(p)
	return ok
}

// pressCalendar shows the press on what of the calendar a tap at the finger lands on (calendarHit): a
// day, an event, a chip, a view of the switch. Its buttons show their own (buttonFace). Not the page
// round an open window, a tap on which only closes it, nor the window's own ground.
func (r *renderer) pressCalendar() {
	if !r.press.on || r.press.shown {
		return
	}
	h, ok := r.calendarHit(r.press.at)
	if !ok {
		return
	}
	switch h.kind {
	case calNone, calOutside, calInside:
		return
	case calChipsDone:
		if h.r == r.dst.Rect {
			return // a tap beside the list of calendars, which only puts it away
		}
	case calViewDay, calViewWeek, calMonthView:
		// A view of the switch, drawn inside its hit (calSegmented).
		in := h.r.Inset(r.s(6))
		r.pressOver(in, float64(in.Dy())/2, cream)
		return
	}
	r.pressOver(h.r, r.sf(10), cream)
}

// pressPart is the part of a sheet drawn as zones over card that the finger is on, found as the
// sheet's tap finds it (mediaHit), and whether there is one.
func (r *paint) pressPart(zones []mediaZone, card image.Rectangle) (mediaZone, bool) {
	if !r.press.on {
		return mediaZone{}, false
	}
	z, _, in := mediaHit(zones, card, r.press.at.X, r.press.at.Y, r.s(sheetSlop))
	return z, in && z.kind != mediaPartNone
}

// pressOnPart shows the press on a sheet's part in b, in the sheet's own colors: a pill or a chip as
// round as it is, a larger part (a cover) with the sheet's corners.
func (r *paint) pressOnPart(b image.Rectangle, pal dashPal) {
	rad := pal.rad
	if b.Dy() <= r.s(64) {
		rad = float64(b.Dy()) / 2
	}
	r.pressOver(b, rad, pal.text)
}
