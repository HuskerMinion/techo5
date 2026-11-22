//go:build !dot

package display

import (
	"image"
	"image/color"
	"log/slog"
	"math"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
)

// A notification's buttons (notify/buttons.go), as the Show's card and the Spot's face both draw them: a
// row of pill buttons along the foot, the first one highlighted on a critical notification. A tap on a
// button answers the notification; a tap anywhere else on it is a tap on the card (notify.Tap), which
// puts an ordinary one away and quiets a critical one.

// A critical notification pulses red while it rings: a device with no light ring (a Show) has nothing
// else to show it, and one that has one shows it on the screen too. Quieted, its card is still.
const (
	// pulseEvery is one breath of the pulse, and pulseFrame how often the screen is drawn while it runs.
	pulseEvery = 1200 * time.Millisecond
	pulseFrame = 150 * time.Millisecond
)

// notifyPulse is how strongly notification n's red pulse shows at now, from 0.15 to 1, or 0 when it
// does not pulse: it is not a critical one, or its bell has been quieted.
func notifyPulse(n notify.Notification, now time.Time) float64 {
	if !n.Critical || !notify.Get().Ringing(n.ID) {
		return 0
	}
	phase := float64(now.UnixMilli()%pulseEvery.Milliseconds()) / float64(pulseEvery.Milliseconds())
	return 0.15 + 0.85*(0.5-0.5*math.Cos(2*math.Pi*phase))
}

// pulseColor is the pulse's red at strength a, faded toward black for the weaker part of a breath.
func pulseColor(a float64) color.RGBA {
	return color.RGBA{uint8(float64(danger.R) * a), uint8(float64(danger.G) * a), uint8(float64(danger.B) * a), 0xff}
}

const (
	// notifyButtonH is a button's height, and notifyButtonGap the room between two, in the Show 5's
	// pixels (paint.s).
	notifyButtonH   = 64
	notifyButtonGap = 16
)

// A critical notification takes the screen's light over while it rings (notify.CriticalLight): from
// the backlight it found to its own brightness over its brightness_rise, and back to what the screen
// would otherwise have over the same time (critBackMin at least) once it is quieted or answered. A
// screen it had to turn on goes off again once it is gone.

// critBackMin is the least time the way back takes, so a light that came up at once still fades down.
const critBackMin = 2 * time.Second

// critLit is where the screen's light is in a critical notification's, read and written by relight
// under the Display's mu.
type critLit struct {
	since    time.Time     // when the ring it follows started; zero when none rings
	from     float64       // the backlight it climbs from
	rise     time.Duration // how long the climb, and the way back, take
	pct      int           // the brightness it climbs to, in percent
	up       bool          // the climb has been lit to its end
	backAt   time.Time     // when the way back started; zero when not on it
	backFrom float64       // the backlight the way back starts from
}

// lightRamp is from on its way to to, evenly over d, after elapsed.
func lightRamp(from, to float64, elapsed, d time.Duration) float64 {
	if d <= 0 || elapsed >= d {
		return to
	}
	return from + (to-from)*max(float64(elapsed), 0)/float64(d)
}

// target is relight's target with the critical notification's light over it. level is the backlight
// now, target what the screen would otherwise have, and full a brightness in percent as a backlight.
func (c *critLit) target(l notify.CriticalLight, ringing bool, level, target float64, now time.Time, full func(int) float64) float64 {
	if ringing {
		if !l.Since.Equal(c.since) {
			*c = critLit{since: l.Since, from: level, rise: l.Rise, pct: l.Percent}
		}
		c.up = now.Sub(c.since) >= c.rise
		return lightRamp(c.from, full(c.pct), now.Sub(c.since), c.rise)
	}
	if !c.since.IsZero() {
		c.since, c.backAt, c.backFrom = time.Time{}, now, level
	}
	if c.backAt.IsZero() {
		return target
	}
	back := max(c.rise, critBackMin)
	if now.Sub(c.backAt) >= back {
		c.backAt = time.Time{}
		return target
	}
	return lightRamp(c.backFrom, target, now.Sub(c.backAt), back)
}

// moving is whether the light is on its way up or back, which settle steps along until the end of
// either has been lit.
func (c *critLit) moving() bool {
	return (!c.since.IsZero() && !c.up) || !c.backAt.IsZero()
}

// followCriticalLight relights the screen when a critical notification starts or stops ringing, so the
// backlight follows the bell rather than the next settle, and turns the screen off again once a critical
// notification that turned it on is gone. The frame loop's alone.
func (d *Display) followCriticalLight() {
	l, _ := notify.Get().CriticalLight()
	if !l.Since.Equal(d.critSeen) {
		d.critSeen = l.Since
		go d.relight(true)
	}
	d.mu.Lock()
	woke := d.critWoke
	d.mu.Unlock()
	if woke == "" {
		return
	}
	if n, up := notify.Get().Showing(); up && n.ID == woke {
		return
	}
	d.mu.Lock()
	if d.critWoke != woke {
		// Another critical notification lit the face in between, and keeps it lit.
		d.mu.Unlock()
		return
	}
	d.critWoke = ""
	on := d.on
	d.mu.Unlock()
	if on {
		slog.Info("screen: off again after the critical notification that turned it on")
		go d.apply(false, d.ceilingOrDefault(), false)
	}
}

// notifyButtonText is what a button says: its label, or "OK" in the screen's language for the one a
// critical notification is given when sent none.
func notifyButtonText(b notify.Button) string {
	if b.Label == "" {
		return tr("ok")
	}
	return b.Label
}

// notifyButtonRow lays count buttons out side by side, of equal width, across row.
func (r *paint) notifyButtonRow(row image.Rectangle, count int) []image.Rectangle {
	if count <= 0 {
		return nil
	}
	gap := r.s(notifyButtonGap)
	w := (row.Dx() - gap*(count-1)) / count
	out := make([]image.Rectangle, count)
	for i := range out {
		x := row.Min.X + i*(w+gap)
		out[i] = image.Rect(x, row.Min.Y, x+w, row.Max.Y)
	}
	return out
}

// drawNotifyButtons draws n's buttons in rects, one each, their labels cut short to fit.
func (r *paint) drawNotifyButtons(n notify.Notification, rects []image.Rectangle) {
	face := r.faces().button
	for i, b := range rects {
		if i >= len(n.Buttons) {
			return
		}
		style := btnSecondary
		if n.Critical && i == 0 {
			style = btnPrimary
		}
		r.labelButton(b, r.fit(face, notifyButtonText(n.Buttons[i]), b.Dx()-r.s(24)), style)
	}
}

// tapNotifyButtons is a tap at p on notification n, whose buttons are at rects: a button is pressed, and
// anywhere else is a tap on the card. Off the screen's goroutine, as a dismissal always was: both tell
// Home Assistant.
func tapNotifyButtons(n notify.Notification, rects []image.Rectangle, p image.Point) {
	for i, b := range rects {
		if p.In(b) {
			go notify.Get().Press(n.ID, i)
			return
		}
	}
	go notify.Get().Tap(n.ID)
}
