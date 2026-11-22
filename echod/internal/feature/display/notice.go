//go:build !dot

package display

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
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
	if id == "" || !noticeNight(time.Now()) || notify.Get().CriticalUp() {
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
// A camera view somebody asked for is not held, nor a notification's picture put over one (OverPlain):
// holding it back would take the camera from whoever was watching it.
func (d *Display) gateCamera(v home.CameraView, up, night bool) (home.CameraView, bool) {
	if up && v.Caption != nil && !v.OverPlain && !d.noticeOut(v.Caption.ID, night) {
		return home.CameraView{}, false
	}
	return v, up
}

// stillOnly is whether a camera view is a notification's still picture: nothing on it moves but its
// countdown, so it is redrawn as a static page is, once a second, rather than at a camera's pace. A
// still with no time stays up to twelve hours.
func stillOnly(v home.CameraView) bool { return v.Caption != nil && !home.PictureIsLive(v.Entity) }
