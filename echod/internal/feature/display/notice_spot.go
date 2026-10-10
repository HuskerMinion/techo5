//go:build spot

package display

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
)

// A notification from Home Assistant and the night, as on the Show (notice_show.go). By day it lights a
// dark face, once, and is drawn as soon as it is up. At night it leaves the face as it is (dark, or
// dimmed to the night level) and waits for the first touch; from then it is drawn as by day. What the
// touch released is kept in the Display's own state, so it holds from one frame to the next.

// noticeLights lights a dark face for a notification by day, once per notification: a screen turned off
// while one is up (from Home Assistant, or by an empty room) stays off, as a video's does. At night it
// does not, and the notification is not counted as having lit it. It reports whether the face is on.
func (d *Display) noticeLights(on, night bool) bool {
	n, up := notify.Get().Showing()
	if !up || night {
		return on
	}
	d.mu.Lock()
	lit := n.ID == d.notifyLit
	d.notifyLit = n.ID
	d.mu.Unlock()
	if lit {
		return on
	}
	if !on {
		d.apply(true, d.ceilingOrDefault(), false)
	}
	return true
}

// noticeID is the id of the notification a touch may be waiting on: the picture's caption when its
// view is up, otherwise the card's. A camera view somebody asked for is on top of a card and owns the
// touches, so with one up there is nothing a touch could release.
func noticeID() string {
	if v, up := home.Get().Camera(); up {
		if v.Caption != nil {
			return v.Caption.ID
		}
		return ""
	}
	if n, up := notify.Get().Showing(); up {
		return n.ID
	}
	return ""
}

// gateNotice holds back a notification's card at night until a touch has released it.
func (d *Display) gateNotice(s *roundScene, night bool) {
	if s.showNotification && !d.noticeOut(s.notification.ID, night) {
		s.notification, s.showNotification = notify.Notification{}, false
	}
}

// noticeNight is whether it is night, for the notification's hold.
func noticeNight(now time.Time) bool { return inNight(now) }

// releaseHeld is the touch that releases a held notification: it lets the face show it, and puts away a
// menu left open, since the card is about to be drawn over it (notifyLights left it alone for the night).
func (d *Display) releaseHeld() {
	d.releaseNotice()
	d.mu.Lock()
	if _, card := notify.Get().Card(); card && d.menuOpen {
		d.closeMenu()
	}
	d.mu.Unlock()
}
