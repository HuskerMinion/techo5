//go:build !dot && !spot && !panel

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

// The 2nd gen Show 8's driver reports 0 to 1280 across and 0 to 800 down, the landscape frame
// itself, so a reading is only scaled. These corners are what that layout implies, not readings
// from a finger on the unit (not yet taken): they hold the arithmetic, not the orientation.
func TestALandscapeControllerIsScaledNotTurned(t *testing.T) {
	defer func(w, h int) { Width, Height = w, h }(Width, Height)
	Width, Height = 1280, 800

	const rawW, rawH = 1281, 801 // the driver's 0..1280 and 0..800, inclusive
	for _, c := range []struct {
		corner       string
		rx, ry       int
		wantX, wantY int
	}{
		{"top left", 0, 0, 0, 0},
		{"top right", 1280, 0, 1279, 0},
		{"bottom right", 1280, 800, 1279, 799},
		{"bottom left", 0, 800, 0, 799},
		{"middle", 640, 400, 639, 399},
	} {
		if x, y := toFrame(rawW, rawH, c.rx, c.ry); x != c.wantX || y != c.wantY {
			t.Errorf("%s: raw (%d,%d) became (%d,%d), want (%d,%d)", c.corner, c.rx, c.ry, x, y, c.wantX, c.wantY)
		}
	}
}

// onBoard has to hand back the Show 5's value on a Show 5, which is what a workstation build looks
// like: no crown panel in the kernel command line, no athena or Skylight device tree.
func TestOnBoardDefaultsToTheShow5(t *testing.T) {
	if got := onBoard("goodix-ts", "fts_ts", "Goodix Capacitive TouchScreen"); got != "goodix-ts" {
		t.Errorf("device name %q, want goodix-ts", got)
	}
	if got := onBoard(960, 1280, 1920); got != 960 {
		t.Errorf("width %d, want 960", got)
	}
}

// The Skylight's GT911 reports 1920 by 1200 over a 1920 by 1080 screen. The corners the user tapped on
// the unit on 2026-10-17 have to land in the matching corners of the frame, the y axis squeezed.
func TestTheSkylightCornersLandInTheFrameCorners(t *testing.T) {
	w, h := Width, Height
	Width, Height = 1920, 1080
	defer func() { Width, Height = w, h }()
	const rawW, rawH = 1920, 1200
	for _, c := range []struct {
		corner       string
		rx, ry       int
		wantX, wantY int
	}{
		{"top left", 18, 55, 18, 49},
		{"top right", 1887, 38, 1887, 34},
		{"bottom left", 34, 1159, 34, 1043},
		{"bottom right", 1886, 1179, 1886, 1061},
	} {
		if x, y := toFrame(rawW, rawH, c.rx, c.ry); x != c.wantX || y != c.wantY {
			t.Errorf("%s: raw (%d,%d) became (%d,%d), want (%d,%d)", c.corner, c.rx, c.ry, x, y, c.wantX, c.wantY)
		}
	}
}

// rawFrame is a portrait panel exactly the landscape screen turned, so frame and raw pixels match.
func rawFrame() (rawW, rawH int, toRaw func(x, y int) (int, int)) {
	return Height, Width, func(x, y int) (int, int) { return Height - 1 - y, x }
}

// Issue #113 on both Shows, each at its own size and notch.
func TestShowSwipeDirections(t *testing.T) {
	defer func(w, h, n, m int) { Width, Height, notch, tapMove = w, h, n, m }(Width, Height, notch, tapMove)
	for _, c := range []struct {
		name                 string
		w, h, notch, tapMove int
	}{
		{"Show 5", 960, 480, 40, 24},
		{"Show 8", 1280, 800, 56, 32},
	} {
		t.Run(c.name, func(t *testing.T) {
			Width, Height, notch, tapMove = c.w, c.h, c.notch, c.tapMove
			checkSwipeDirections(t)
		})
	}
}
