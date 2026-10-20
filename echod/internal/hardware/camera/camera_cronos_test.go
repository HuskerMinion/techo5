//go:build !dot && !spot

package camera

import "testing"

// Each generation's sensor: the 2nd gen's OV02B10, the 1st gen's OV9734, whose cells start with
// blue rather than red and whose frames are smaller and faster, and the Show 8 2nd gen's OV13855.
func TestPickSensor(t *testing.T) {
	second := pickSensor(sensorOV02B10)
	if second.w != 1600 || second.h != 1200 || second.outW != 800 || second.outH != 600 {
		t.Errorf("2nd gen: %+v", second)
	}
	if second.blueFirst {
		t.Error("2nd gen: its cells start with red")
	}

	first := pickSensor(sensorOV9734)
	if first.w != 1280 || first.h != 720 {
		t.Errorf("1st gen: %dx%d, want 1280x720", first.w, first.h)
	}
	if !first.blueFirst {
		t.Error("1st gen: its cells start with blue, so red and blue swap")
	}
	if first.minShut < 1 || first.frameLines >= 802 {
		t.Errorf("1st gen exposure limits %+v: the driver allows 1 line and its frame is 802 lines less a margin", first)
	}
	show8 := pickSensor(sensorOV13855)
	if show8.w != 2112 || show8.h != 1568 || !show8.blueFirst {
		t.Errorf("Show 8 2nd gen: %+v, want 2112x1568 starting with blue", show8)
	}
	if show8.maxShut > 52000-16 || show8.frameLines > show8.maxShut {
		t.Errorf("Show 8 2nd gen exposure limits %+v: the mainline driver's longest frame is 52000 lines less a margin of 16", show8)
	}
	for _, s := range []sensorSpec{second, first, show8} {
		if s.outW*2 != s.w || s.outH*2 != s.h {
			t.Errorf("%+v: the live view is one pixel per Bayer cell, so half of each dimension", s)
		}
	}
}
