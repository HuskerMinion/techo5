//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"testing"
	"time"
)

// Each red clock style draws something red on black and nothing brighter than the night red: a
// clock for a dark room must not light it.
func TestRedClockStaysDim(t *testing.T) {
	now := time.Date(2026, 9, 26, 3, 47, 0, 0, time.Local)
	for _, style := range []string{nightStylePlain, nightStyleLED, nightStyleFlip} {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, 960, 480)))
		r.draw(scene{now: now, phase: "idle", redClock: true, redStyle: style})
		lit := 0
		b := r.dst.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				c := r.dst.RGBAAt(x, y)
				if luma(c.R, c.G, c.B) > luma(nightRed.R, nightRed.G, nightRed.B) {
					t.Fatalf("%q: a pixel brighter than the night red at (%d, %d): %v", style, x, y, c)
				}
				if c.R == nightRed.R {
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
