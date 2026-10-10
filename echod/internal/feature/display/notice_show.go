//go:build !dot && !spot

package display

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
)

// A notification from Home Assistant and the night. By day it lights a dark panel, once, and is drawn
// as soon as it is up. At night it leaves the screen as it is (dark, or the red night clock) and waits
// for the first touch, which is also what wakes the night screen; from then it is drawn as by day.
// What the touch released is kept here, in the Display's own state, so it holds from one frame to the
// next.

// noticeLights lights a dark panel for a notification by day, once per notification: a screen turned
// off while one is up (from Home Assistant, or by an empty room) stays off, as a video's does. At night
// it does not, and the notification is not counted as having lit it. It reports whether the panel is
// on. The frame loop's alone, like videoLit.
func (d *Display) noticeLights(on, night bool) bool {
	n, up := notify.Get().Showing()
	if !up || night || n.ID == d.notifyLit {
		return on
	}
	d.notifyLit = n.ID
	if !on {
		d.apply(true, d.ceilingOrDefault(), false)
	}
	return true
}

// noticeID is the id of the notification a touch may be waiting on: the card's, or the picture's caption.
func noticeID() string {
	id := ""
	if n, up := notify.Get().Showing(); up {
		id = n.ID
	}
	if v, up := home.Get().Camera(); up && v.Caption != nil && v.Caption.ID != "" {
		id = v.Caption.ID
	}
	return id
}

// gateNotice holds back a notification's card at night until a touch has released it. Released, it
// takes the night screen as an announcement or a reminder does: the red clock gives way to it.
func (d *Display) gateNotice(s *scene, night bool) {
	if !s.showNotification {
		return
	}
	if !d.noticeOut(s.notification.ID, night) {
		s.notification, s.showNotification = notify.Notification{}, false
		return
	}
	s.redClock = false
}

// noticeNight is whether it is night, for the notification's hold.
func noticeNight(now time.Time) bool { return nightNow(now) }
