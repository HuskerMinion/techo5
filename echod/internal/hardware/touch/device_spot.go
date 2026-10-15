//go:build spot

package touch

// The Echo Spot: a Goodix GT5668 reported by MediaTek's mtk-tpd driver, 0 to 480 on both axes, in
// the round panel's own frame.
const (
	deviceName = "mtk-tpd"

	// Width and Height are the frame coordinates are reported in.
	Width  = 480
	Height = 480

	rawFallbackW, rawFallbackH = 481, 481

	// holdGestures: the ring menu opens on a hold.
	holdGestures = true

	// tapMove is how far a finger may wander and still be a tap: a still finger on this controller
	// drifts over 20 pixels in the time of a tap (recorded 2026-09-16).
	tapMove = 40

	// notch is the vertical travel per volume step: on this panel 40 turned the volume from a tap's
	// drift.
	notch = 60
)

func toFrame(rawW, rawH, rx, ry int) (x, y int) {
	return rx * Width / max(rawW, 1), ry * Height / max(rawH, 1)
}
