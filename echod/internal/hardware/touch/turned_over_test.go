//go:build !dot && !spot && !panel

package touch

import "testing"

// A screen turned over reads each corner in the opposite one: a Show 8's panel (800 by 1280,
// reported portrait) shown half a turn round. The readings are the Show 8's own corners
// (device_cronos_test.go).
func TestAScreenTurnedOverTurnsItsCornersOver(t *testing.T) {
	defer func(w, h int, over bool) { Width, Height, turnedOver = w, h, over }(Width, Height, turnedOver)
	Width, Height, turnedOver = 1280, 800, true

	const rawW, rawH = 800, 1280
	for _, c := range []struct {
		corner       string
		rx, ry       int
		wantX, wantY int
	}{
		{"bottom right", 740, 87, 1192, 740},
		{"bottom left", 746, 1225, 54, 746},
		{"top left", 37, 1240, 39, 37},
		{"top right", 39, 9, 1270, 39},
	} {
		x, y := toFrame(rawW, rawH, c.rx, c.ry)
		if x != c.wantX || y != c.wantY {
			t.Errorf("%s: panel (%d,%d) became (%d,%d), want (%d,%d)", c.corner, c.rx, c.ry, x, y, c.wantX, c.wantY)
		}
	}

	// A landscape controller is turned over the same way.
	if x, y := toFrame(1280, 800, 0, 0); x != 1279 || y != 799 {
		t.Errorf("landscape, turned over: (0,0) became (%d,%d), want (1279,799)", x, y)
	}

	// And with it off, as on every Show, nothing is turned.
	turnedOver = false
	if x, y := toFrame(rawW, rawH, 740, 87); x != 87 || y != 59 {
		t.Errorf("not turned over: (%d,%d), want (87,59)", x, y)
	}
}
