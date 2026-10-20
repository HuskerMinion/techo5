//go:build hwdevice && !dot && !spot

package camera

// CapExposureForTest holds the exposure to lines at most, for a device test that wants the sensor's
// shortest frame in any light; the returned func puts it back. Only in hwdevice test builds.
func CapExposureForTest(lines int) (restore func()) {
	f, m := aeFrame, aeMaxShut
	aeFrame, aeMaxShut = min(aeFrame, lines), lines
	return func() { aeFrame, aeMaxShut = f, m }
}

// PinGainForTest holds the gain to gain (in 1/64) at a 30-a-second frame, whatever the light, for a
// device test that wants the stream at a given gain; the returned func lets the exposure loop have it
// again. Only in hwdevice test builds.
func PinGainForTest(gain int) (restore func()) {
	g := aePinGain
	aePinGain = gain
	return func() { aePinGain = g }
}
