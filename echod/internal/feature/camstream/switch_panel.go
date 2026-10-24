//go:build panel

package camstream

import (
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/lib/panelctl"
)

// The Panel app asks for camera permission only after someone enables its camera. Keep the setting
// across app restarts, and erase every captured image immediately when it is switched off.
func panelCameraSwitch(on bool) {
	if on {
		panelctl.Send("camera 1")
		return
	}
	panelctl.Send("camera 0")
	camera.Get().Disable()
}
