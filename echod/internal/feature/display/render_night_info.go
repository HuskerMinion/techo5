//go:build !dot && !spot

package display

import (
	"image"
	"time"
)

// Night clock shows Next up and notifications (night_info.go) on the Show: the Next up card or the glance
// chips along the foot of the night clock, and a notification's card over it. Each is drawn as by day
// into a black layer and then taken into the night's tone (nightTone), so the colors of a calendar, a
// chip's icon or a theme never light the room: nothing is brighter than the night clock's own red.

// nightInfoGap is the clear space, before scaling, between the clock and what is along its foot.
const nightInfoGap = 14

// nightInfoScene is s as the night's strip is laid out: without the Call button the night clock hides, and
// as the night light.
func nightInfoScene(s scene) scene {
	s.callButton, s.strip, s.nightDim = false, false, true
	return s
}

// nightInfoChips is whether the chips are what the night clock carries at its foot; with none, the
// Next up card is (as by day, the card is only where the chips would have nothing to show).
func nightInfoChips(s scene) bool { return s.nightInfo && len(s.glance) > 0 }

// nightInfoCard is whether the Next up card is what the night clock carries at its foot.
func nightInfoCard(s scene) bool { return s.nightInfo && len(s.glance) == 0 && s.nextUp != nil }

// nightInfoReserve is how much of the screen's height the foot takes: what the clock keeps clear of.
func (r *renderer) nightInfoReserve(s scene) int {
	switch {
	case s.showNotification:
		return 0 // the card is the one thing drawn; the foot is empty under it
	case nightInfoChips(s):
		return r.s(96 + nightInfoGap)
	case nightInfoCard(s):
		box, _ := r.nextUpRect(nightInfoScene(s))
		return r.h - box.Min.Y + r.s(nightInfoGap)
	}
	return 0
}

// nightInfoBox is a box w wide and h tall, centered in area.
func nightInfoBox(area image.Rectangle, w, h int) image.Rectangle {
	cx, cy := (area.Min.X+area.Max.X)/2, (area.Min.Y+area.Max.Y)/2
	return image.Rect(cx-w/2, cy-h/2, cx-w/2+w, cy-h/2+h)
}

// nightLayer paints with paint onto a black layer over b and takes what it drew into the night's tone
// onto the screen. The layer is black, so a pixel nothing touched is left as the clock drew it.
func (r *renderer) nightLayer(b image.Rectangle, paint func()) {
	b = b.Intersect(r.dst.Rect)
	if b.Empty() {
		return
	}
	layer := nightLayerFor(&r.nightBuf, b)
	screen := r.dst
	r.dst = layer
	paint()
	r.dst = screen
	nightToneOnto(screen, layer, b)
}

// nightInfoOver draws what the night clock carries over and below the time: the chips or the Next up
// card along the foot, and a notification's card.
func (r *renderer) nightInfoOver(s scene) {
	if !s.nightInfo {
		return
	}
	foot := image.Rect(0, r.h-r.nightInfoReserve(s)+r.s(nightInfoGap), r.w, r.h)
	switch {
	case s.showNotification:
		// A notification is the one thing on the night clock: the strip and chips wait under it, so the
		// card never cuts across them.
	case nightInfoChips(s):
		r.nightLayer(foot, func() { r.glanceStrip(s.glance, false, false) })
	case nightInfoCard(s):
		ns := nightInfoScene(s)
		r.nightLayer(foot, func() { r.drawNextUp(ns) })
		// Nobody can aim at a card on a night light: a tap on it is the night light's, not the calendar's.
		r.setNextCard(image.Rectangle{}, time.Time{})
	}
	if s.showNotification {
		box := r.notifyBox()
		r.nightLayer(box.Inset(-r.s(24)), func() { r.notifyCard(s) })
	}
}
