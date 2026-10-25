//go:build !panel

package setup

// sshHere and cameraWebHere: off a panel the setup page shows both switches as on or off, as it
// always has.
const (
	sshHere       = true
	cameraWebHere = true

	notHereWhy = ""
)
