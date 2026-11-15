//go:build spot

package display

import (
	"image"
	"image/color"
	"testing"
	"time"
)

// The Spot's face wanders a pixel a minute at most, across and up only, within its reach, the same
// for the same time of day; with the Call button up it keeps to across (#560).
func TestSpotClockDrift(t *testing.T) {
	day := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	prev := spotClockDrift(day, false)
	if prev != (image.Point{}) {
		t.Errorf("midnight: %v, not its place", prev)
	}
	seen := map[image.Point]bool{}
	for m := 1; m <= 24*60; m++ {
		d := spotClockDrift(day.Add(time.Duration(m%(24*60))*time.Minute), false)
		if dx, dy := d.X-prev.X, d.Y-prev.Y; abs(dx) > 1 || abs(dy) > 1 {
			t.Fatalf("minute %d: jumps from %v to %v", m, prev, d)
		}
		if abs(d.X) > spotDriftAcross || d.Y > 0 || d.Y < -spotDriftUp {
			t.Fatalf("minute %d: %v", m, d)
		}
		if c := spotClockDrift(day.Add(time.Duration(m)*time.Minute), true); c.Y != 0 || c.X != d.X {
			t.Fatalf("minute %d with the Call button: %v", m, c)
		}
		seen[d] = true
		prev = d
	}
	if len(seen) < 2*spotDriftAcross+1 {
		t.Errorf("visits %d places", len(seen))
	}
	a := spotClockDrift(time.Date(2026, 3, 1, 10, 43, 0, 0, time.UTC), false)
	b := spotClockDrift(time.Date(2026, 11, 9, 10, 43, 59, 0, time.UTC), false)
	if a != b {
		t.Errorf("10:43 on two days: %v and %v", a, b)
	}
}

// What the page draws moves by the drift; what was under it stays exactly where it was.
func TestSpotDrifted(t *testing.T) {
	r := newRoundRenderer(image.NewRGBA(image.Rect(0, 0, side, side)))
	fillCanvas(r.dst, colBackground)
	under := color.RGBA{200, 10, 10, 255}
	r.dst.SetRGBA(10, 10, under)
	r.dst.SetRGBA(side-1, side-1, under)
	ink := color.RGBA{10, 200, 10, 255}
	d := image.Pt(-4, -3)
	r.drifted(d, func() { r.dst.SetRGBA(center, center, ink) })
	if got := r.dst.RGBAAt(center-4, center-3); got != ink {
		t.Errorf("the page's pixel at its new place: %v", got)
	}
	if got := r.dst.RGBAAt(center, center); got != colBackground {
		t.Errorf("the page's pixel's old place: %v", got)
	}
	if r.dst.RGBAAt(10, 10) != under || r.dst.RGBAAt(side-1, side-1) != under {
		t.Error("what was under the page moved")
	}
	if r.driftCanvas == r.dst {
		t.Error("the canvas was left as the spare")
	}
}
