//go:build !dot && !spot

package display

import "testing"

// The strip is the only part of the screen an announcement owns, so it is the only part where a tap
// ends one. A tap on the clock above it is a tap on the clock.
func TestWhereATapEndsAnAnnouncement(t *testing.T) {
	// The Show 5's panel, where the strip's sizes are drawn 1:1.
	const w, h = showWide, showHigh

	cases := []struct {
		what string
		x, y int
		want bool
	}{
		{"the middle of the strip", w / 2, h - announceInset - announceBar/2, true},
		{"just inside its top edge", w / 2, h - announceInset - announceBar + 2, true},
		{"just above it", w / 2, h - announceInset - announceBar - 4, false},
		{"the clock, well above", w / 2, h / 3, false},
		{"below it, in the margin", w / 2, h - 4, false},
		{"left of it, in the margin", 4, h - announceInset - announceBar/2, false},
	}
	for _, c := range cases {
		if got := onAnnounceStrip(c.x, c.y, w, h); got != c.want {
			t.Errorf("%s: got %v, want %v", c.what, got, c.want)
		}
	}
	// A larger panel draws the strip larger: the Skylight's 1920 by 1080 by its height, 2.25 times
	// over, so it covers the date line whole; a tap follows it there.
	const sw, sh = 1920, 1080
	top := sh - (announceInset+announceBar)*9/4
	if !onAnnounceStrip(sw/2, top+2, sw, sh) || onAnnounceStrip(sw/2, top-4, sw, sh) {
		t.Errorf("on a 1920 by 1080 panel the strip's top should be at %d", top)
	}
	// The Show 8's 16:10 panel draws it a tenth smaller than its width alone would, clear of the date
	// line above it: 158 high, 34 in, so its top is at 608.
	if b := announceBox(show8Wide, show8High); b.Min.Y != 608 || b.Max.Y != show8High-34 {
		t.Errorf("on the Show 8 the strip is %v", b)
	}
}
