//go:build dot

package plexcast

// Here is whether this device is a Plex player: a Dot is not (here.go). Nothing of this package is
// built on that device -- no server, no socket, no registration -- and these stubs are only so that
// the settings screen and the setup page, which are built for every device, can ask what it is without
// knowing which device they are on.
const Here = false

func On() bool { return false }

func Current() Status { return Status{} }

func Listen(func()) {}

// StartPinLink on a Dot: there is no account to link and no screen to show a code on.
func StartPinLink() (string, error) { return "", ErrNotHere }

func CancelPinLink() {}

func UnlinkAccount() error { return ErrNotHere }
