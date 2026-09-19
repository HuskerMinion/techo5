//go:build !dot && !spot

package camera

import "testing"

// Each generation's sensor: the 2nd gen's OV02B10 and the 1st gen's OV9734, whose cells start with
// blue rather than red and whose frames are smaller and faster.
func TestPickSensor(t *testing.T) {
	second := pickSensor(false)
	if second.w != 1600 || second.h != 1200 || second.outW != 800 || second.outH != 600 {
		t.Errorf("2nd gen: %+v", second)
	}
	if second.blueFirst {
		t.Error("2nd gen: its cells start with red")
	}

	first := pickSensor(true)
	if first.w != 1280 || first.h != 720 {
		t.Errorf("1st gen: %dx%d, want 1280x720", first.w, first.h)
	}
	if !first.blueFirst {
		t.Error("1st gen: its cells start with blue, so red and blue swap")
	}
	if first.minShut < 1 || first.frameLines >= 802 {
		t.Errorf("1st gen exposure limits %+v: the driver allows 1 line and its frame is 802 lines less a margin", first)
	}
	for _, s := range []sensorSpec{second, first} {
		if s.outW*2 != s.w || s.outH*2 != s.h {
			t.Errorf("%+v: the live view is one pixel per Bayer cell, so half of each dimension", s)
		}
	}
}
