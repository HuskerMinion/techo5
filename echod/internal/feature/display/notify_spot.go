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

// notifyLights puts the menu away for a notification of words, at any hour, as a reminder does
// (reminder_spot.go), and lights a dark face for either kind by day. At night it waits, dark, until the
// face is woken.
func (d *Display) notifyLights() {
	d.mu.Lock()
	on := d.on
	if _, card := notify.Get().Card(); card && d.menuOpen {
		d.closeMenu()
	}
	d.mu.Unlock()
	if _, up := notify.Get().Showing(); up && !on && !inNight(time.Now()) {
		d.apply(true, d.ceilingOrDefault(), false)
	}
	d.wake()
}
