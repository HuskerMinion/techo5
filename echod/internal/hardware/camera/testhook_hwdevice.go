//go:build hwdevice && !dot && !spot

package camera

// CapExposureForTest holds the exposure to lines at most, for a device test that wants the sensor's
// shortest frame in any light; the returned func puts it back. Only in hwdevice test builds.
func CapExposureForTest(lines int) (restore func()) {
	f, m := aeFrame, aeMaxShut
	aeFrame, aeMaxShut = min(aeFrame, lines), lines
	return func() { aeFrame, aeMaxShut = f, m }
}
