//go:build !dot && !spot

package display

import (
	"bytes"
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
)

// An alarm ringing over the night clock has the day's Stop and Snooze buttons (#646). It used to keep
// the red clock up with no buttons at all, and a tap anywhere stopped it, so there was no way to
// snooze from the screen. The buttons are drawn where a tap on them does what they say (ringTap), and
// the page stays in the night's red: nothing on it is brighter than the night clock.
func TestAnAlarmRingingOverTheNightClockHasItsButtons(t *testing.T) {
	now := time.Date(2026, 11, 19, 6, 30, 0, 0, time.Local)
	ringing := ringState{alarm: &alarm.Ring{Label: "Wake up", At: now}, snoozable: true}
	for _, panel := range showPanels()[:2] {
		for _, style := range []string{nightStylePlain, nightStyleLED, nightStyleFlip} {
			draw := func(ring ringState) (*image.RGBA, *renderer) {
				img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
				r := newRenderer(img)
				r.draw(scene{now: now, phase: "idle", redClock: true, redStyle: style, ring: ring})
				return img, r
			}
			quiet, _ := draw(ringState{})
			img, r := draw(ringing)
			if bytes.Equal(quiet.Pix, img.Pix) {
				t.Errorf("%s %s: the alarm rang over the night clock with no Stop or Snooze on the screen", panel.name, style)
				continue
			}

			stop, snooze := r.actionHalves()
			for name, button := range map[string]image.Rectangle{"Stop": stop, "Snooze": snooze} {
				if !drawnIn(img, button) {
					t.Errorf("%s %s: no %s button where a tap on it would be one", panel.name, style, name)
				}
			}
			// The Snooze button is the one a tap on it snoozes.
			mid := image.Pt((snooze.Min.X+snooze.Max.X)/2, (snooze.Min.Y+snooze.Max.Y)/2)
			if !ringSnoozeAt(mid.X, r.w, true, r.actionDecided(mid.Y)) {
				t.Errorf("%s %s: a tap on the Snooze button drawn at night does not snooze", panel.name, style)
			}

			for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
				for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
					if c := img.RGBAAt(x, y); c.R > nightRed.R || c.G > nightRed.G || c.B != 0 {
						t.Fatalf("%s %s: %v at (%d,%d) is brighter than the night red", panel.name, style, c, x, y)
					}
				}
			}
		}
	}
}

// drawnIn is whether anything but black is drawn inside b.
func drawnIn(img *image.RGBA, b image.Rectangle) bool {
	b = b.Intersect(img.Rect)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if c := img.RGBAAt(x, y); c.R|c.G|c.B != 0 {
				return true
			}
		}
	}
	return false
}

// The next alarm under the night clock is half as large again as the date's words, and bold, so it is
// read from the pillow (#646).
func TestTheNightClocksNextAlarmIsLargeAndBold(t *testing.T) {
	for _, panel := range showPanels()[:2] {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
		small, line := r.small.Metrics().Height.Ceil(), r.nightAlarmFace.Metrics().Height.Ceil()
		if line*100 < small*145 {
			t.Errorf("%s: the next alarm is %d pixels tall, under half as large again as the date's %d", panel.name, line, small)
		}
		words := "Alarm 7:30 AM"
		if r.width(r.nightAlarmFace, words)*100 < r.width(r.small, words)*150 {
			t.Errorf("%s: the next alarm is no wider than the regular face at that size; it should be bold", panel.name)
		}
	}
}
