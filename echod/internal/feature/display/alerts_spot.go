//go:build spot

package display

import (
	"image"
	"image/color"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// Weather alerts on the Spot's round face (home/alerts.go fetches them): a pill near the top of the
// clock while any is in force at home, the shapes and a pill on the rain map, and a face with the
// alert's whole text, opened from either pill. On that face a tap puts it away, a sideways swipe goes
// to the next alert and an up or down one scrolls the words.

// alertShowSpot is how long the alert face stays without a touch.
const alertShowSpot = time.Minute

// alertLineH is one line of an alert's words on the face.
const alertLineH = 30

// Where the clock's alert pill and the rain map's go: a band near the top of the circle.
var (
	clockPillY = 96
	radarPillY = 88
)

// closeAlert takes the alert face down: what somebody asked for since comes up in its place. Called
// with d.mu held.
func (d *Display) closeAlert() { d.alertUntil = time.Time{} }

// openAlertSpot puts alert i of those at home on the face. Called with d.mu held.
func (d *Display) openAlertSpot(i int) {
	d.alertUntil, d.alertIdx, d.alertScroll = time.Now().Add(alertShowSpot), i, 0
	if d.menuOpen {
		d.closeMenu()
	}
}

// alertUpSpot is whether the alert face is showing: the same test alertSceneSpot draws it by, and not
// under a camera, which is drawn over it and takes the taps.
func (d *Display) alertUpSpot() bool {
	here := len(home.Get().Alerts().Here)
	if _, camera := home.Get().Camera(); camera {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return time.Now().Before(d.alertUntil) && here > 0 && d.view.Phase == "idle"
}

// alertSceneSpot fills in the alerts the faces may show, and the alert face when it is up. Only while
// idle: a turn's listening rim and its answer draw over it.
func (d *Display) alertSceneSpot(s *roundScene, now time.Time) {
	s.alerts = home.Get().Alerts()
	d.mu.Lock()
	up := now.Before(d.alertUntil) && len(s.alerts.Here) > 0 && s.phase == "idle"
	i, scroll := d.alertIdx, d.alertScroll
	d.mu.Unlock()
	if up {
		s.showAlert, s.alertIdx, s.alertScroll = true, min(i, len(s.alerts.Here)-1), scroll
	}
}

// alertGestureSpot is a finger on the alert face; every gesture is its.
func (d *Display) alertGestureSpot(g touch.Gesture) {
	n := len(home.Get().Alerts().Here)
	limit := 0
	if d.r != nil {
		limit = d.r.alertScrollLimit()
	}
	d.mu.Lock()
	d.alertUntil = time.Now().Add(alertShowSpot)
	switch {
	case n == 0 || g.Kind == touch.Tap:
		d.alertUntil = time.Time{}
		slog.Info("screen: alert put away")
	case g.Kind == touch.SwipeLeft:
		d.alertIdx, d.alertScroll = (d.alertIdx+1)%n, 0
	case g.Kind == touch.SwipeRight:
		d.alertIdx, d.alertScroll = (d.alertIdx+n-1)%n, 0
	case g.Kind == touch.SwipeUp:
		d.alertScroll = min(d.alertScroll+2, limit)
	case g.Kind == touch.SwipeDown:
		d.alertScroll = max(min(d.alertScroll, limit)-2, 0)
	}
	d.mu.Unlock()
	d.wake()
}

// clearAlertTaps forgets where the pill was: a face that does not draw it must not leave it
// tappable. Called at the start of every frame.
func (r *roundRenderer) clearAlertTaps() {
	r.zmu.Lock()
	r.alertPillAt = image.Rectangle{}
	r.zmu.Unlock()
}

// alertScrollLimit is how far the alert on the face could scroll in the frame last drawn.
func (r *roundRenderer) alertScrollLimit() int {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	return r.alertMax
}

// alertPillTapped reports whether a tap at x, y is on an alert pill drawn in the frame last drawn.
func (r *roundRenderer) alertPillTapped(x, y int) bool {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	return !r.alertPillAt.Empty() && image.Pt(x, y).In(r.alertPillAt.Inset(-10))
}

// alertPill is the most severe alert at home, and how many more, as a pill centered at y.
func (r *roundRenderer) alertPill(here []home.Alert, y int) {
	r.zmu.Lock()
	r.alertPillAt = image.Rectangle{}
	r.zmu.Unlock()
	if len(here) == 0 {
		return
	}
	a := here[0]
	// The pill is narrow on a round face: "T-Storm" is how the NWS itself shortens it on its maps.
	label := strings.Replace(a.Event, "Thunderstorm", "T-Storm", 1)
	if n := otherKinds(here); n > 0 {
		label += " +" + itoa(n)
	}
	mark := 22
	label = clip(r.label, r, label, 330-mark)
	w := r.width(r.label, label) + mark + 36
	b := image.Rect(center-w/2, y, center+w/2, y+32)
	r.line(float64(b.Min.X+16), float64(y+16), float64(b.Max.X-16), float64(y+16), 32, a.Color)
	ink := inkOn(a.Color)
	// The mark: a triangle with an exclamation point, drawn (the face's font has no warning sign).
	cx, top, bot := b.Min.X+14+mark/2, y+6, y+26
	strokeRing(r.dst, []image.Point{{cx, top}, {cx + mark/2, bot}, {cx - mark/2, bot}}, ink, 2.5)
	strokeRing(r.dst, []image.Point{{cx, top + 7}, {cx, bot - 7}}, ink, 2.5)
	strokeRing(r.dst, []image.Point{{cx, bot - 3}, {cx, bot - 3}}, ink, 2.5)
	r.text(r.label, label, b.Min.X+mark+24, y+23, ink)
	r.zmu.Lock()
	r.alertPillAt = b
	r.zmu.Unlock()
}

// alertFace is one alert's whole text on the round face, in a ring of its color.
func (r *roundRenderer) alertFace(s roundScene) {
	a := s.alerts.Here[s.alertIdx]
	r.ringAt(center, center, rimIn-8, rimIn, 0, 2*math.Pi, a.Color)
	r.centered(r.label, "WEATHER ALERT", 70, a.Color)
	r.paragraph(r.title, a.Event, 112, colText, 2)
	sub := clip(r.small, r, alertWhen(a, s.now), 360)
	top := 176
	if r.width(r.title, a.Event) > 360 {
		top += 40 // the name took two lines
	}
	r.centered(r.small, sub, top, colAccent)

	type line struct {
		s string
		c color.RGBA
	}
	var lines []line
	for _, p := range strings.Split(a.Description, "\n") {
		for _, l := range r.wrap(r.small, p, 340) {
			lines = append(lines, line{l, colText})
		}
	}
	for _, p := range strings.Split(a.Instruction, "\n") {
		for _, l := range r.wrap(r.small, p, 340) {
			lines = append(lines, line{l, colAccent})
		}
	}
	first, rows := top+34, max((392-(top+34))/alertLineH, 1)
	most := max(len(lines)-rows, 0)
	scroll := min(s.alertScroll, most)
	r.zmu.Lock()
	r.alertMax = most
	r.zmu.Unlock()
	for i := 0; i < rows && scroll+i < len(lines); i++ {
		l := lines[scroll+i]
		r.centered(r.small, l.s, first+i*alertLineH+20, l.c)
	}
	hint := "tap to close"
	if len(s.alerts.Here) > 1 {
		hint = itoa(s.alertIdx+1) + " of " + itoa(len(s.alerts.Here)) + " · swipe for next"
	}
	if scroll+rows < len(lines) {
		hint += " · swipe up"
	}
	r.centered(r.tiny, hint, 420, colDim)
}
