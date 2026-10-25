//go:build panel

package camera

import "testing"

// A panel never drives the Echo camera. A tablet on the Echo Show 5's MediaTek chip (the 8th gen Fire
// HD 8) has /dev/camera-isp and /dev/kd_camera_hw like a Show, and a panel that looked for them took
// the tablet for a Show and failed to open the camera ("open camera-isp: permission denied"). The
// Echo camera is not built into a panel, so nothing here may answer as if a camera were there.
func TestAPanelHasNoCamera(t *testing.T) {
	if Available() {
		t.Fatal("a panel reports a camera of the daemon's own")
	}
	if _, err := Get().Acquire(); err == nil {
		t.Fatal("a panel acquired a camera")
	}
	if _, err := Get().AcquireSlow(); err == nil {
		t.Fatal("a panel acquired a camera for watching the room")
	}
	if f, err := Get().Snapshot(t.Context()); err == nil || f != nil {
		t.Fatal("a panel took a snapshot")
	}
}
