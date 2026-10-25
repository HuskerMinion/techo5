//go:build panel

package setup

// sshHere and cameraWebHere are false on a TECHO5 Panel: an app cannot run an SSH server for the
// tablet, and the tablet's camera is Android's, so neither has a switch anywhere (security's
// sshAvailable and cameraPage). notHereWhy says so on the setup page.
const (
	sshHere       = false
	cameraWebHere = false

	notHereWhy = "SSH and Camera on the network are not part of TECHO5 Panel: Android runs the tablet and its camera."
)
