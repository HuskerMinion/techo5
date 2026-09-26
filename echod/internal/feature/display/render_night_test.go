//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"testing"
	"time"
)

// Each night clock style draws the time on black and nothing brighter than its own ink - the night red,
// or the flip clock's dim off-white: a clock for a dark room must not light it.
func TestRedClockStaysDim(t *testing.T) {
	now := time.Date(2026, 9, 26, 3, 47, 0, 0, time.Local)
	for _, style := range []string{nightStylePlain, nightStyleLED, nightStyleFlip} {
		ink := nightRed
		if style == nightStyleFlip {
			ink = flipInk
		}
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, 960, 480)))
		r.draw(scene{now: now, phase: "idle", redClock: true, redStyle: style})
		lit := 0
		b := r.dst.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				c := r.dst.RGBAAt(x, y)
				if luma(c.R, c.G, c.B) > luma(ink.R, ink.G, ink.B)+1 {
					t.Fatalf("%q: a pixel brighter than its ink at (%d, %d): %v", style, x, y, c)
				}
				if c == ink {
					lit++
				}
			}
		}
		if lit < 2000 {
			t.Errorf("%q: only %d red pixels; is the time there?", style, lit)
		}
		if got := r.dst.RGBAAt(2, 2); got != (color.RGBA{0, 0, 0, 255}) {
			t.Errorf("%q: the corner is %v, not black", style, got)
		}
	}
}

// The seven-segment table lights the right segments, and a 12-hour clock leaves the first place empty
// before ten.
func TestClockDigits(t *testing.T) {
	clock24.Store(false)
	if got := clockDigits(time.Date(2026, 1, 1, 3, 47, 0, 0, time.Local)); got != [4]int{-1, 3, 4, 7} {
		t.Errorf("3:47 AM: %v", got)
	}
	if got := clockDigits(time.Date(2026, 1, 1, 22, 5, 0, 0, time.Local)); got != [4]int{1, 0, 0, 5} {
		t.Errorf("10:05 PM: %v", got)
	}
	clock24.Store(true)
	defer clock24.Store(false)
	if got := clockDigits(time.Date(2026, 1, 1, 3, 47, 0, 0, time.Local)); got != [4]int{0, 3, 4, 7} {
		t.Errorf("03:47: %v", got)
	}
	if len(segments[8]) != 7 || segments[1] != "bc" {
		t.Error("segment table")
	}
}

// A card that changes flips for a moment and then settles; coming back to the flip clock after a while
// away does not flip every card at once.
func TestFlipCardsFlipOnce(t *testing.T) {
	var f flipState
	t0 := time.Date(2026, 9, 26, 12, 59, 59, 0, time.Local)
	a := [5]string{"1", "2", "5", "9", "PM"}
	b := [5]string{"", "1", "0", "0", "PM"}
	f.update(a, t0)
	if f.busy(t0) {
		t.Fatal("the first look flipped")
	}
	t1 := t0.Add(time.Second)
	f.update(b, t1)
	if !f.busy(t1.Add(100*time.Millisecond)) || f.from != a || f.shown != b {
		t.Fatalf("no flip on a change: %+v", f)
	}
	if f.busy(t1.Add(flipFor)) {
		t.Error("still flipping after flipFor")
	}
	// Away for a minute, then back to different cards: no flip.
	t2 := t1.Add(time.Minute)
	f.update(a, t2)
	if f.busy(t2) {
		t.Error("coming back flipped every card")
	}
}

// The night clock gives way to a reminder: its page is drawn, not the clock alone on black.
func TestNightClockGivesWayToAReminder(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, 960, 480)))
	now := time.Date(2026, 9, 26, 3, 0, 0, 0, time.Local)
	r.draw(scene{now: now, phase: "idle", redClock: true, showReminder: true})
	if got := r.dst.RGBAAt(2, 240); got == (color.RGBA{0, 0, 0, 255}) {
		t.Error("the night clock was drawn over a reminder")
	}
}
