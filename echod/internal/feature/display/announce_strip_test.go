//go:build !dot && !spot

package display

import "testing"

// The strip is the only part of the screen an announcement owns, so it is the only part where a tap
// ends one. A tap on the clock above it is a tap on the clock.
func TestWhereATapEndsAnAnnouncement(t *testing.T) {
	const w, h = 1024, 600

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
}
