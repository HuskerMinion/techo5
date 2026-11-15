//go:build spot

package display

import (
	"image"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
)

// The press on the round panel (press.go): the faces the Spot draws, and which of them a press made on
// one is not shown on.

// pageID is which face a frame draws, as far as a press cares: a press made on one face is not shown
// on another, where something else is under the same point.
type pageID struct {
	phase                                              string
	call, ring, pin, ask, videoAsk, video, firstRun    bool
	recording, reminder, announcement, notice, alert   bool
	sheet, sheetGrid, menu, camera, volume, dash, play bool
	saver, night                                       bool
	cat                                                category
	picker                                             string
	menuMode                                           menuMode
	menuSel                                            int
}

// pageOf is the face s draws.
func pageOf(s *roundScene) pageID {
	return pageID{
		phase: s.phase, call: s.call.Phase != phone.Idle, ring: s.ringing.any(), pin: s.pin.open, ask: s.setupAsking,
		videoAsk: s.showVideoAsk, video: s.showVideo, firstRun: s.firstRunUp, recording: s.announceRecording,
		reminder: s.showReminder, announcement: s.showAnnouncement, notice: s.showNotification, alert: s.showAlert,
		sheet: s.sheetOpen, sheetGrid: s.sheetOpen && s.sheetGrid, menu: s.menuOpen, camera: s.showCamera,
		volume: s.showVolume, dash: s.showDash, play: s.nowPlaying, saver: s.slideshowScreensaver != nil,
		night: s.nightClockFace(), cat: s.sheet.st.cat, picker: s.sheet.st.picker, menuMode: s.menuMode, menuSel: s.menuSel,
	}
}

// pressScene puts the press on the frame, while there is one to show and the frame is still the face it
// was made on, and notes the face for the next finger to land on.
func (d *Display) pressScene(s *roundScene, now time.Time) {
	s.press = d.pressNow(pageOf(s), now)
}

// pressUnshown lets go of the press a frame was drawn with when nothing in it took the press: a finger
// on the clock's face or on a card's words. It would show on nothing in the frames after either (the
// face is the same face), and letting it go here spares the two frames its lift and its linger would
// otherwise ask for, for a look that does not change. Only that press: one landed since is kept.
func (d *Display) pressUnshown(shown pressView, took bool) {
	if !shown.on || took {
		return
	}
	d.mu.Lock()
	if d.press.set && d.press.seq == shown.seq {
		d.press = pressState{}
	}
	d.mu.Unlock()
}

// pressRefused is whether a finger landing at a point is on no control to show it on. The faces that
// take the whole panel (a card, a reminder) draw no press of their own, so none is turned away here.
func (d *Display) pressRefused(image.Point, pageID) bool { return false }

// pressHeld is a finger the round panel says is held (Hold). One that moved is dragging; one held still
// is no tap any more, but on the dashboard, where its lift soon after is still the tile's tap
// (dashGestureSpot). Either way the press is over, unless it is the dashboard's still finger.
func (d *Display) pressHeld(moved bool) {
	d.mu.Lock()
	dash := d.dashShowing && !d.menuOpen && !d.sheetOpen
	d.mu.Unlock()
	if (moved || !dash) && d.pressClear() {
		d.wake()
	}
}
