//go:build !panel

package layout

import "os"

// OnAndroid reports whether an Android userspace is running around the daemon. Off Android — the
// Linux image, or a workstation — there are no properties, no init services and no vendor firewall,
// and everything that would speak to them stands down.
func OnAndroid() bool {
	_, err := os.Stat(setprop)
	return err == nil
}
