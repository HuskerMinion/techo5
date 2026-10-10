//go:build !dot

package display

import (
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
)

// closeCamera takes the camera page down for a tap. A notification's picture is dismissed rather than
// just closed, so Home Assistant hears that somebody saw it; DismissID takes the view down itself, and
// only if the notification up is the one this view belongs to. A view that is not a notification's, or
// whose notification has gone, is just hidden, if it is still the view that was tapped. It runs on its
// own goroutine (the dismissal ends in a network write), so a tap that follows is not held up behind
// Home Assistant.
func closeCamera(v home.CameraView) {
	if v.Caption != nil && notify.Get().DismissID(v.Caption.ID) {
		return
	}
	// A view of a notification that has gone is hidden by its id: this runs on its own goroutine, and a
	// newer picture may have replaced it by now, which HideCamera would take down.
	if v.Caption != nil {
		home.Get().HidePicture(v.Caption.ID)
		return
	}
	// A plain view is hidden by its number, for the same reason: HideCamera would take down whatever
	// is up when this runs, a picture notification that landed first included.
	home.Get().HideView(v.View)
}
