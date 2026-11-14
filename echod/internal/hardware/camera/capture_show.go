//go:build !dot && !spot && !panel

package camera

// setSensor makes s the board's sensor, and everything the package works out from it at its start
// (camera.go's and exposure.go's variables, as camera_athena_test.go's useSensor sets them): for a
// capture (capture.go) that drives a sensor of its own, from its init, before anything opens the
// camera.
//
//lint:ignore U1000 for a capture's board file; unused in this tree by design.
func setSensor(s sensorSpec) {
	sensor = s
	Width, Height = s.outW, s.outH
	sensorW, sensorH = s.w, s.h
	bytesPerLine = sensorW * 10 / 8
	frameBytes = bytesPerLine * sensorH
	aeMinShut, aeFrame, aeMaxShut = s.minShut, s.frameLines, s.maxShut
	blackCap = s.blackCap
}

// stillGeometry is the size of a still taken in a mode bigger than the stream's, where a capture has
// one (ok); a frame of that many bytes develops at that size (geometry).
var stillGeometry = func() (g geometry, ok bool) { return }

// geometry is the frame's size: the stream's, or a still's from a bigger mode.
func (f *Frame) geometry() geometry {
	if g, ok := stillGeometry(); ok && len(f.raw) == g.bpl*g.h {
		return g
	}
	return streamed()
}

// aePinGain holds the gain there at a 30-a-second frame, whatever the light (0: off). Only device
// tests set it (PinGainForTest).
var aePinGain = 0
