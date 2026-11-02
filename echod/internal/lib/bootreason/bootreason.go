// Package bootreason is why the device last started, as boot.sh worked it out at boot, and the mark a
// restart on purpose leaves for the next boot to find.
//
// The reasons are update, restart (asked for: the device's settings, a reboot over SSH),
// recovery (the device restarting itself to get out of a fault: no network, an update that did not
// settle), watchdog, panic, power and unknown, each only as far as the board can tell them apart.
package bootreason

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	Update   = "update"
	Restart  = "restart"
	Recovery = "recovery"
)

var (
	// markFile is read, and removed, by the next boot.sh. shutdown.sh writes restart there for a reboot
	// through init when nothing more exact is there.
	markFile = "/data/techo5-linux/restart-reason"

	// lastFile is what boot.sh found for this boot.
	lastFile = "/run/techo5/boot-reason"
)

// Mark says why the device is about to restart, for a restart that does not go through init (a reboot
// system call), which shutdown.sh would otherwise have marked. Where the folder is not there, an image
// without boot.sh, nothing is written.
func Mark(reason string) {
	if _, err := os.Stat(filepath.Dir(markFile)); err != nil {
		return
	}
	_ = os.WriteFile(markFile, []byte(reason+"\n"), 0o644)
}

// Unmark takes a mark back, for a restart that did not happen: left there, it would name the next boot,
// whatever caused that.
func Unmark() { _ = os.Remove(markFile) }

// Last is the reason boot.sh found for this boot, and false on an image whose boot does not work one out.
func Last() (string, bool) {
	b, err := os.ReadFile(lastFile)
	if err != nil {
		return "", false
	}
	r := strings.TrimSpace(string(b))
	return r, r != ""
}
