//go:build spot

package display

import (
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mdi"
)

// A notification from Home Assistant on the round face: the icon, the title, the words, and the way
// out. A circle has no corner to put a card in, so it takes the face, as an announcement does.

// notifyFaceLines is as many lines of the message as the face holds.
const notifyFaceLines = 3

func (r *roundRenderer) notifyFace(s roundScene) {
	n := s.notification
	y := 150
	if _, ok := mdi.Rune(n.Icon); ok {
		const size = 40
		r.mdiIcon(n.Icon, center-r.s(size)/2, y-r.s(size)-8, size, colAccent)
		y += 10
	}
	head := "NOTIFICATION"
	if n.Title != "" {
		head = strings.ToUpper(n.Title)
	}
	r.centered(r.label, clip(r.label, r, head, 340), y, colAccent)

	lines := r.wrap(r.title, n.Message, 340)
	if len(lines) > notifyFaceLines {
		lines = append(lines[:notifyFaceLines-1], clip(r.title, r, strings.Join(lines[notifyFaceLines-1:], " "), 340))
	}
	y += 60
	for _, line := range lines {
		r.centered(r.title, line, y, colText)
		y += 40
	}
	r.centered(r.small, "tap to dismiss", y+16, colDim)
}

// notifyLights puts the menu away for a notification of words and lights a dark face for either kind,
// once per notification and by day (noticeLights). At night it leaves the face as it is: the menu is
// put away by the touch that releases the notification (gesture), and a dark face stays dark until
// that touch lights it.
func (d *Display) notifyLights() {
	night := inNight(time.Now())
	d.mu.Lock()
	on := d.on
	if _, card := notify.Get().Card(); card && d.menuOpen && !night {
		d.closeMenu()
	}
	d.mu.Unlock()
	d.noticeLights(on, night)
	d.wake()
}
