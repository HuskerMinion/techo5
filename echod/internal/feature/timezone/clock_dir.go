//go:build !panel

package timezone

import (
	"os"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
)

// ntpSet marks a clock NTP has set since boot: boot writes it when a time server answers (t5_ntp; on
// the Dot, its boot.sh and ntp-event.sh). The kernel's own flag cannot say it: busybox's ntpd leaves
// it set.
var ntpSet = "/run/techo5/ntp-set"

// ntpSynced is whether NTP has set the clock since boot.
func ntpSynced() bool {
	_, err := os.Stat(ntpSet)
	return err == nil
}

// setSystemClock sets the system clock, and the RTC from it, as boot does after NTP: in UTC, so the
// next boot starts closer.
func setSystemClock(t time.Time) error {
	tv := unix.NsecToTimeval(t.UnixNano())
	if err := unix.Settimeofday(&tv); err != nil {
		return err
	}
	_ = exec.Command("hwclock", "-w", "-u").Run()
	return nil
}
