//go:build !dot && !panel

package camera

// Captures. A board whose sensor a kernel driver of its own drives (not one this package programs)
// registers a capture from a file of its own (registerCapture, in an init): while its probe finds the
// driver, the camera is there (Available) and opened through it (open), and the device's chain is
// the capture's. Nothing here knows any board: with none registered, the camera is driven as ever.

// chain is the sensor driven through a kernel's chain: the athena kernel's V4L2 (camera_v4l2.go), or
// a capture's. device.v holds it, and the register paths are left alone.
type chain interface {
	exposure(shutter, gain int)
	stream(stop chan struct{}, frame func(bayer []byte))
	close()
}

// capture is a way to open the board's sensor through a driver of its own.
type capture struct {
	name  string
	probe func() bool             // the driver's device is there
	open  func() (*device, error) // the device, with v the capture's chain
}

var captures []capture

// registerCapture adds a board's capture, from an init in the board's own file. No board in this tree
// registers one, so the camera is driven as ever.
//
//lint:ignore U1000 the extension point boards register their captures through; unused in this tree by design.
func registerCapture(c capture) { captures = append(captures, c) }

// activeCapture is the first capture whose driver is there, nil for this package's own paths.
func activeCapture() *capture {
	for i := range captures {
		if captures[i].probe() {
			return &captures[i]
		}
	}
	return nil
}

// SensorGain is the sensor's gain the frame was asked for with, in 1/64 (64: 1x; 0: not known). The
// exposure lengthens the frame before it adds the last of its gain (autoExpose), so a high gain is a
// dim scene and a noisy picture.
func (f *Frame) SensorGain() int { return f.gain }
