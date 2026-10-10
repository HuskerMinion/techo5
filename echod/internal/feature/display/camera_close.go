//go:build !dot

package display

import (
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
)

// closeCamera takes the camera page down for a tap. A notification's picture is dismissed rather than
// just closed, so Home Assistant hears that somebody saw it; Dismiss takes the view down itself.
func closeCamera(v home.CameraView) {
	if v.Caption != nil && notify.Get().Dismiss() {
		return
	}
	home.Get().HideCamera()
}
