//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"log/slog"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// Weather alerts on the Show (home/alerts.go fetches them): a badge on the clock while any is in force
// at home, pills over the rain map, and a page with an alert's full text, opened from either. The
// page is swiped sideways from one alert to the next and up or down through a long one, and put away
// with Close or by leaving it a minute. Anything else opened (the forecast, the calendar, "go home")
// puts it away too.

// alertShow is how long the alert page stays without a touch.
const alertShow = time.Minute

// OpenAlert puts alert i of those at home up; false, and nothing, when there is no such alert.
func (d *Display) OpenAlert(i int) bool {
	here := home.Get().Alerts().Here
	if i < 0 || i >= len(here) {
		return false
	}
	d.mu.Lock()
	d.alertUntil, d.alertIdx, d.alertScroll = time.Now().Add(alertShow), i, 0
	d.weatherUntil, d.sheet, d.calUntil = time.Time{}, false, time.Time{}
	d.mu.Unlock()
	slog.Info("screen: alert opened", "event", here[i].Event)
	d.wake()
	return true
}

// closeAlert puts the alert page away. Called with d.mu held, by whatever opens another page.
func (d *Display) closeAlert() { d.alertUntil = time.Time{} }

// alertUp is whether the alert page is on the screen: the same test alertScene draws it by.
func (d *Display) alertUp() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return time.Now().Before(d.alertUntil) && d.view.Phase == "idle"
}

// alertScene fills in the alerts every page may show (the badge, the pills), and the page when up.
// Only while idle: a turn, and its answer, draw over it.
func (d *Display) alertScene(s *scene, now time.Time) {
	s.alerts = home.Get().Alerts()
	d.mu.Lock()
	up := now.Before(d.alertUntil) && s.phase == "idle"
	i, scroll := d.alertIdx, d.alertScroll
	if now.Before(d.alertUntil) && len(s.alerts.Here) == 0 {
		d.alertUntil, up = time.Time{}, false // it ended while it was open
	}
	d.mu.Unlock()
	if up {
		s.showAlert, s.alertIdx, s.alertScroll = true, min(i, len(s.alerts.Here)-1), scroll
	}
}

// alertGesture is a touch on the alert page. Every touch keeps it up a while longer.
func (d *Display) alertGesture(g touch.Gesture) {
	n := len(home.Get().Alerts().Here)
	limit := 0
	if d.r != nil {
		limit = d.r.alertScrollLimit()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.alertUntil = time.Now().Add(alertShow)
	if n == 0 {
		d.alertUntil = time.Time{}
		return
	}
	switch g.Kind {
	case touch.SwipeLeft:
		d.alertIdx, d.alertScroll = (d.alertIdx+1)%n, 0
	case touch.SwipeRight:
		d.alertIdx, d.alertScroll = (d.alertIdx+n-1)%n, 0
	case touch.SwipeUp:
		d.alertScroll = min(d.alertScroll+3, limit)
	case touch.SwipeDown:
		d.alertScroll = max(min(d.alertScroll, limit)-3, 0)
	case touch.Tap:
		if d.r != nil && image.Pt(g.X, g.Y).In(d.r.alertCloseButton()) {
			d.alertUntil = time.Time{}
			slog.Info("screen: alert put away")
		}
	}
}

// ---- drawing

// clearAlertTaps forgets where the badge and the pills were: a frame that does not draw them must not
// leave them tappable. Called at the start of every frame.
func (r *renderer) clearAlertTaps() {
	r.alertMu.Lock()
	r.badgeAt, r.pillsAt, r.pillsIdx = image.Rectangle{}, nil, nil
	r.alertMu.Unlock()
}

// alertCloseButton is the page's Close, where the weather page keeps its toggle.
func (r *renderer) alertCloseButton() image.Rectangle { return r.weatherButton() }

// alertScrollLimit is how far the alert on the page could scroll in the frame last drawn.
func (r *renderer) alertScrollLimit() int {
	r.alertMu.Lock()
	defer r.alertMu.Unlock()
	return r.alertMax
}

// alertPage is one alert's full text: what it is and when, who issued it and where, then what the NWS
// says, scrolled by s.alertScroll lines, with what to do in the accent color.
func (r *renderer) alertPage(s scene) {
	a := s.alerts.Here[s.alertIdx]
	r.fillRect(r.dst.Rect, walnut)
	r.fillRect(image.Rect(0, 0, r.s(12), r.h), a.Color)
	x := r.s(40)
	r.text(r.title, a.Event, x, r.s(66), cream)
	sub := alertWhen(a, s.now)
	if a.Sender != "" {
		sub += "  ·  " + a.Sender
	}
	r.text(r.small, r.clipTo(r.small, sub, r.w-x-r.margin), x, r.s(112), amber)
	r.text(r.tiny, r.clipTo(r.tiny, a.Area, r.w-x-r.margin), x, r.s(148), dim)

	type line struct {
		s string
		c color.RGBA
	}
	var lines []line
	width := r.w - x - r.margin
	for _, p := range strings.Split(a.Description, "\n") {
		for _, l := range r.wrap(r.tiny, p, width) {
			lines = append(lines, line{l, cream})
		}
	}
	for _, p := range strings.Split(a.Instruction, "\n") {
		for _, l := range r.wrap(r.tiny, p, width) {
			lines = append(lines, line{l, amber})
		}
	}
	top, step := r.s(196), r.s(34)
	rows := max((r.h-r.s(112)-top)/step+1, 1)
	most := max(len(lines)-rows, 0)
	first := min(s.alertScroll, most)
	r.alertMu.Lock()
	r.alertMax = most
	r.alertMu.Unlock()
	for i := 0; i < rows && first+i < len(lines); i++ {
		l := lines[first+i]
		r.text(r.tiny, l.s, x, top+i*step, l.c)
	}

	foot := ""
	if n := len(s.alerts.Here); n > 1 {
		foot = itoa(s.alertIdx+1) + " of " + itoa(n) + "  ·  swipe for the next"
	}
	if first < most {
		if foot != "" {
			foot += "  ·  "
		}
		foot += "swipe up for more"
	}
	r.text(r.micro, foot, x, r.h-r.s(62), dim)
	b := r.alertCloseButton()
	r.bevel(b, shift(ember, 16), true)
	r.text(r.small, "Close", b.Min.X+(b.Dx()-r.width(r.small, "Close"))/2, b.Max.Y-r.s(15), cream)
}

// alertPills lays out the pills for the kinds of alert at home in a row from x, y, as many as fit and
// then one saying how many more there are. Each is the alert it opens (its kind's first; the "+N"
// pill opens the first it stands for) with where it is.
func (r *renderer) alertPills(here []home.Alert, x, y int) (rects []image.Rectangle, idx []int, labels []string) {
	kinds := alertKinds(here)
	h, pad, gap := r.s(40), r.s(16), r.s(10)
	pillW := func(label string) int { return r.width(r.tiny, label) + 2*pad }
	for i, k := range kinds {
		w := pillW(k.event)
		rest := len(kinds) - i - 1
		// Room is kept for a "+N" after this one while any are left.
		need := w
		if rest > 0 {
			need += gap + pillW("+"+itoa(rest))
		}
		if i >= 3 || x+need > r.w-r.margin {
			more := "+" + itoa(len(kinds)-i)
			rects = append(rects, image.Rect(x, y, x+pillW(more), y+h))
			idx, labels = append(idx, k.idx), append(labels, more)
			break
		}
		rects, idx, labels = append(rects, image.Rect(x, y, x+w, y+h)), append(idx, k.idx), append(labels, k.event)
		x += w + gap
	}
	return rects, idx, labels
}

// drawAlertPills draws the row, and keeps where it is for taps.
func (r *renderer) drawAlertPills(here []home.Alert, x, y int) {
	rects, idx, labels := r.alertPills(here, x, y)
	for k, b := range rects {
		c := here[idx[k]].Color
		if strings.HasPrefix(labels[k], "+") {
			c = color.RGBA{60, 56, 52, 255}
		}
		r.roundButton(b, float64(b.Dy())/2, c)
		r.text(r.tiny, labels[k], b.Min.X+r.s(16), b.Max.Y-r.s(12), inkOn(c))
	}
	r.alertMu.Lock()
	r.pillsAt, r.pillsIdx = rects, idx
	r.alertMu.Unlock()
}

// pillTapped is the alert a tap on the rain map's pills opens, or -1.
func (r *renderer) pillTapped(p image.Point) int {
	r.alertMu.Lock()
	defer r.alertMu.Unlock()
	for k, b := range r.pillsAt {
		if p.In(b.Inset(-r.s(6))) {
			return r.pillsIdx[k]
		}
	}
	return -1
}

// alertBadge is the clock's word that something is in force: the most severe alert's name, and how
// many more, in its color at the top right, with a warning mark. It keeps clear of the header's
// "microphone off" pill in the middle.
func (r *renderer) alertBadge(s scene) {
	here := s.alerts.Here
	if len(here) == 0 {
		return
	}
	a := here[0]
	more := ""
	if len(here) > 1 {
		more = "  +" + itoa(len(here)-1)
	}
	h, mark := r.s(40), r.s(30)
	right := r.w - r.margin
	left := r.w / 2 // never past the middle
	if s.muted {
		left = (r.w+r.width(r.tiny, "microphone off"))/2 + r.s(14) + r.s(12)
	}
	room := right - left - mark - r.s(28)
	label := r.clipTo(r.tiny, a.Event, room-r.width(r.tiny, more)) + more
	w := r.width(r.tiny, label) + mark + r.s(28)
	b := image.Rect(right-w, r.s(18), right, r.s(18)+h)
	r.roundButton(b, float64(h)/2, a.Color)
	ink := inkOn(a.Color)
	// The mark: a triangle with an exclamation point.
	cx, top, bot := b.Min.X+r.s(14)+mark/2, b.Min.Y+r.s(8), b.Max.Y-r.s(8)
	tri := []image.Point{{cx, top}, {cx + mark/2, bot}, {cx - mark/2, bot}}
	strokeRing(r.dst, tri, ink, float64(r.s(3)))
	strokeRing(r.dst, []image.Point{{cx, top + r.s(8)}, {cx, bot - r.s(9)}}, ink, float64(r.s(3)))
	strokeRing(r.dst, []image.Point{{cx, bot - r.s(4)}, {cx, bot - r.s(4)}}, ink, float64(r.s(3)))
	r.text(r.tiny, label, b.Min.X+mark+r.s(22), b.Max.Y-r.s(12), ink)
	r.alertMu.Lock()
	r.badgeAt = b
	r.alertMu.Unlock()
}

// badgeTapped is whether p is on the clock's alert badge.
func (r *renderer) badgeTapped(p image.Point) bool {
	r.alertMu.Lock()
	defer r.alertMu.Unlock()
	return !r.badgeAt.Empty() && p.In(r.badgeAt.Inset(-r.s(10)))
}

// alertKind is one pill: a kind of event, the first alert of that kind at home (the one a tap opens),
// and how many alerts of it there are.
type alertKind struct {
	idx   int
	event string
	color color.RGBA
	count int
}

// alertKinds are the alerts at home as pills, one per kind of event, most severe first.
func alertKinds(here []home.Alert) []alertKind {
	var out []alertKind
	at := map[string]int{}
	for i, a := range here {
		if k, ok := at[a.Event]; ok {
			out[k].count++
			continue
		}
		at[a.Event] = len(out)
		out = append(out, alertKind{i, a.Event, a.Color, 1})
	}
	return out
}
