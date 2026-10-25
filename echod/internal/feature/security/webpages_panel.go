//go:build panel

package security

// webPages: a panel serves the screen page, but has no camera of its own for the camera page (the
// tablet's is Android's), so only the screen's switch is offered.
const (
	webPages   = true
	cameraPage = false
)
