//go:build !dot

package display

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// What the Show and the Spot share of holding a notification through the night (notice_show.go and
// notice_spot.go have the rest). noticeNight is each screen's own idea of night.

// noticeOut is whether the screen may show the notification with this id: by day always, and it is
// remembered as seen, so that a night that begins with it up does not take it away. At night only one
// a touch has released.
func (d *Display) noticeOut(id string, night bool) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !night {
		d.notifySeen = id
		return true
	}
	return id != "" && d.notifySeen == id
}

// noticeHeld is whether a notification is waiting for a touch: one that came up at night and has not
// been released.
func (d *Display) noticeHeld() bool {
	id := noticeID()
	if id == "" || !noticeNight(time.Now()) {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.notifySeen != id
}

// releaseNotice lets the screen show the notification that has been waiting for a touch.
func (d *Display) releaseNotice() {
	if id := noticeID(); id != "" {
		d.mu.Lock()
		d.notifySeen = id
		d.mu.Unlock()
	}
}

// gateCamera holds back a picture notification's camera page at night until a touch has released it.
// A camera view somebody asked for is not held.
func (d *Display) gateCamera(v home.CameraView, up, night bool) (home.CameraView, bool) {
	if up && v.Caption != nil && !d.noticeOut(v.Caption.ID, night) {
		return home.CameraView{}, false
	}
	return v, up
}
