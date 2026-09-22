//go:build !dot && !spot

package touch

import "testing"

// The readings are a real finger on a real Echo Show 8, 2026-09-22: the panel reports 0 to 800
// across and 0 to 1280 down, and the four corners of the landscape screen were touched in turn.
// A quarter turn the wrong way does not break the screen, it mirrors it, which is the sort of thing
// nobody notices until the volume slider runs backwards — so the corners are written down here.
func TestTheShow8CornersLandWhereTheyWereTouched(t *testing.T) {
	defer func(w, h int) { Width, Height = w, h }(Width, Height)
	Width, Height = 1280, 800

	const rawW, rawH = 800, 1280
	for _, c := range []struct {
		corner       string
		rx, ry       int
		wantX, wantY int
	}{
		{"top left", 740, 87, 87, 59},
		{"top right", 746, 1225, 1225, 53},
		{"bottom right", 37, 1240, 1240, 762},
		{"bottom left", 39, 9, 9, 760},
	} {
		x, y := toFrame(rawW, rawH, c.rx, c.ry)
		if x != c.wantX || y != c.wantY {
			t.Errorf("%s: panel (%d,%d) became (%d,%d), want (%d,%d)", c.corner, c.rx, c.ry, x, y, c.wantX, c.wantY)
		}
		if wantLeft := c.corner == "top left" || c.corner == "bottom left"; wantLeft != (x < Width/2) {
			t.Errorf("%s ended up on the wrong side: x=%d of %d", c.corner, x, Width)
		}
		if wantTop := c.corner == "top left" || c.corner == "top right"; wantTop != (y < Height/2) {
			t.Errorf("%s ended up in the wrong half: y=%d of %d", c.corner, y, Height)
		}
	}
}

// The Show 5 keeps the mapping it had. Its panel is 480 by 960 behind a 960 by 480 screen, and the
// same corners have to come out in the same places.
func TestTheShow5KeepsItsCorners(t *testing.T) {
	defer func(w, h int) { Width, Height = w, h }(Width, Height)
	Width, Height = 960, 480

	const rawW, rawH = 480, 960
	for _, c := range []struct {
		corner       string
		rx, ry       int
		wantX, wantY int
	}{
		{"top left", 479, 0, 0, 0},
		{"top right", 479, 959, 959, 0},
		{"bottom right", 0, 959, 959, 479},
		{"bottom left", 0, 0, 0, 479},
	} {
		if x, y := toFrame(rawW, rawH, c.rx, c.ry); x != c.wantX || y != c.wantY {
			t.Errorf("%s: panel (%d,%d) became (%d,%d), want (%d,%d)", c.corner, c.rx, c.ry, x, y, c.wantX, c.wantY)
		}
	}
}

// onCrown has to hand back the Show 5's value on a Show 5, which is what a workstation build looks
// like: layout.Crown() is false when there is no crown panel in the kernel command line.
func TestOnCrownDefaultsToTheShow5(t *testing.T) {
	if got := onCrown("goodix-ts", "fts_ts"); got != "goodix-ts" {
		t.Errorf("device name %q, want goodix-ts", got)
	}
	if got := onCrown(960, 1280); got != 960 {
		t.Errorf("width %d, want 960", got)
	}
}
