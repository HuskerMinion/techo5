//go:build spot

package display

import (
	"image"
	"math"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mdi"
)

// A notification from Home Assistant on the round face: the icon, the title, the words, and the way
// out. A circle has no corner to put a card in, so it takes the face, as an announcement does.

// notifyFaceLines is as many lines of the message as the face holds, and notifyFaceLinesButtons as many
// with a row of buttons under them.
const (
	notifyFaceLines        = 3
	notifyFaceLinesButtons = 2
)

// The row of a notification's buttons on the round face: two at most, where the circle is still wide
// enough for both.
const (
	notifyFaceButtons    = 2
	notifyFaceButtonTop  = 330
	notifyFaceButtonHalf = 150 // half the row's width
)

// notifyButtons is where n's buttons are on the face, the first two of them; none without buttons.
func (r *roundRenderer) notifyButtons(n notify.Notification) []image.Rectangle {
	if len(n.Buttons) == 0 || n.Own {
		return nil
	}
	row := image.Rect(center-notifyFaceButtonHalf, notifyFaceButtonTop, center+notifyFaceButtonHalf, notifyFaceButtonTop+notifyButtonH)
	return r.notifyButtonRow(row, min(len(n.Buttons), notifyFaceButtons))
}

// tapNotifyFace is a tap at p on the face while notification n takes it.
func (r *roundRenderer) tapNotifyFace(n notify.Notification, p image.Point) {
	tapNotifyButtons(n, r.notifyButtons(n), p)
}

// A note of the device's own (notify.Note) says what to check, which takes more words than a
// notification's news: they are smaller, and there are more lines of them.
const (
	noteFaceLines = 5
	noteFaceStep  = 30
)

// A large or huge notification (notify's size) keeps the face's shape and gives the message more of it:
// a wider, taller band for the words, set as large as the band takes them, and no icon on a huge one.
func (r *roundRenderer) sizedNotifyFace(n notify.Notification) {
	huge := n.Size == notify.SizeHuge
	head := tr("card_notification")
	if n.Title != "" {
		head = upper(n.Title)
	}
	hf := r.textFace(true, 22)
	r.centered(hf, r.fit(hf, head, 340), 118, colAccent)
	top, width, high := 140, 380, 190
	if huge {
		top, width, high = 134, 400, 226
	}
	sizes := stepsDown(64, 28, 4)
	if huge {
		sizes = stepsDown(150, 26, 6)
	}
	lines, face, lineH := r.fitWords(true, n.Message, width, high, sizes)
	if face != nil {
		y := top + (high-len(lines)*lineH)/2 + face.Metrics().Ascent.Ceil()
		for _, line := range lines {
			r.centered(face, line, y, colText)
			y += lineH
		}
	}
	r.centered(r.small, tr("drawer_dismiss"), top+high+34, colDim)
}

func (r *roundRenderer) notifyFace(s roundScene) {
	n := s.notification
	if len(n.Buttons) > 0 && !n.Own {
		r.buttonsNotifyFace(n, s.notifyPulse)
		return
	}
	if n.Size != notify.SizeNormal && !n.Own {
		r.sizedNotifyFace(n)
		return
	}
	y := 150
	if _, ok := mdi.Rune(n.Icon); ok {
		const size = 40
		r.mdiIcon(n.Icon, center-r.s(size)/2, y-r.s(size)-8, size, colAccent)
		y += 10
	}
	head := tr("card_notification")
	if n.Title != "" {
		head = upper(n.Title)
	}
	r.centered(r.label, clip(r.label, r, head, 340), y, colAccent)

	face, most, step := r.title, notifyFaceLines, 40
	if n.Own {
		face, most, step = r.small, noteFaceLines, noteFaceStep
	}
	lines := r.wrap(face, n.Message, 340)
	if len(lines) > most {
		lines = append(lines[:most-1], clip(face, r, strings.Join(lines[most-1:], " "), 340))
	}
	y += 60
	for _, line := range lines {
		r.centered(face, line, y, colText)
		y += step
	}
	r.centered(r.small, tr("drawer_dismiss"), y+16, colDim)
}

// notifyLights puts the menu away for a notification of words and lights a dark face for either kind,
// once per notification and by day (noticeLights). At night it leaves the face as it is: the menu is
// put away by the touch that releases the notification (gesture), and a dark face stays dark until
// that touch lights it.
func (d *Display) notifyLights() {
	night := nightNow(time.Now())
	d.mu.Lock()
	on := d.on
	if _, card := notify.Get().Card(); card && d.menuOpen && !night {
		d.closeMenu()
	}
	d.mu.Unlock()
	d.noticeLights(on, night)
	d.wake()
}

// buttonsNotifyFace is a notification with buttons on the face: the icon and the title, two lines of
// the message whatever its size, and the buttons under them. A critical one says nothing of a tap
// putting it away, since only a button does.
func (r *roundRenderer) buttonsNotifyFace(n notify.Notification, pulse float64) {
	if pulse > 0 {
		// The round face is the card, so there is nothing around it to light: a wide band along its rim
		// pulses instead, under the words and the buttons.
		r.aaRing(center, center, rimIn-16, 32, 0, 2*math.Pi, pulseColor(pulse))
	}
	y := 130
	if _, ok := mdi.Rune(n.Icon); ok {
		const size = 40
		r.mdiIcon(n.Icon, center-r.s(size)/2, y-r.s(size)-8, size, colAccent)
		y += 10
	}
	head := tr("card_notification")
	if n.Title != "" {
		head = upper(n.Title)
	}
	r.centered(r.label, clip(r.label, r, head, 340), y, colAccent)
	lines := r.wrap(r.title, n.Message, 360)
	if len(lines) > notifyFaceLinesButtons {
		lines = append(lines[:notifyFaceLinesButtons-1], clip(r.title, r, strings.Join(lines[notifyFaceLinesButtons-1:], " "), 360))
	}
	y += 60
	for _, line := range lines {
		r.centered(r.title, line, y, colText)
		y += 40
	}
	r.drawNotifyButtons(n, r.notifyButtons(n))
	if !n.Critical {
		r.centered(r.small, tr("drawer_dismiss"), notifyFaceButtonTop+notifyButtonH+34, colDim)
	}
}
