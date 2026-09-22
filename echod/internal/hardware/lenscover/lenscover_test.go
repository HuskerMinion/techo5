//go:build !dot

package lenscover

import "testing"

// The kernel writes an input device's switch capabilities as hex words, most significant first, so
// the bit for a given code is counted from the end rather than the start. Getting that backwards
// finds the shutter on the wrong device, or on none, and the failure is silent: the camera simply
// never knows it is covered.
func TestTheLensCoverBitIsFoundInTheCapabilityBitmap(t *testing.T) {
	for _, c := range []struct {
		name string
		caps string
		want bool
	}{
		{"a Show 8's gpio-keys, lens cover only", "200", true},
		{"a Show 5's gpio-keys, which is the same bitmap", "200", true},
		{"the same with the volume keys' bits too", "201", true},
		{"a device with no switches at all", "0", false},
		{"a headphone jack but no lens cover", "4", false},
		{"more words than one, the cover in the lowest", "1 0 200", true},
		{"more words than one, nothing in the lowest", "200 0 4", false},
		{"trailing newline and spaces, as sysfs writes it", " 200 \n", true},
		{"empty", "", false},
		{"not a number", "zzz", false},
	} {
		if got := capsHaveLensCover(c.caps); got != c.want {
			t.Errorf("%s: %q gave %v, want %v", c.name, c.caps, got, c.want)
		}
	}
}

// A device with no shutter must not report itself as uncovered: "not covered" and "there is nothing
// covering this" are different answers, and only one of them is an assurance. Covered() says false
// either way, so anything gating on it has to ask Present() too.
func TestADeviceWithNoShutterIsNotReportedAsUncovered(t *testing.T) {
	var c Cover
	if c.Present() {
		t.Error("a cover that was never found reports itself present")
	}
	if c.Covered() {
		t.Error("a cover that was never found reports itself covering the lens")
	}
}

// And one that has a shutter reports what the shutter is doing.
func TestAShutterReportsItsPosition(t *testing.T) {
	c := Cover{present: true, covered: true}
	if !c.Present() || !c.Covered() {
		t.Error("a closed shutter did not report itself")
	}
	c.covered = false
	if !c.Present() || c.Covered() {
		t.Error("an open shutter did not report itself")
	}
}

// The capability bitmap cannot tell a Show 8 from a Show 5, which is the whole reason the shutter is
// decided by the board instead.
//
// Measured on units 2026-09-22: both report caps.sw=200 on event6 gpio-keys. The Show 5 has no
// shutter behind it and its switch read false, so nothing was blocked, but a device reading the
// other way would have refused the camera with nothing in the log but "the lens cover is closed".
// If this test ever fails because the two bitmaps differ, the board gate could be reconsidered — but
// not before measuring both generations again.
func TestTheBitmapCannotTellTheBoardsApart(t *testing.T) {
	const show8, show5 = "200", "200"
	if !capsHaveLensCover(show8) || !capsHaveLensCover(show5) {
		t.Fatal("the measured bitmaps stopped matching the lens cover bit")
	}
	if show8 != show5 {
		t.Error("the two boards no longer advertise the same switches; re-measure before trusting the bitmap")
	}
}
