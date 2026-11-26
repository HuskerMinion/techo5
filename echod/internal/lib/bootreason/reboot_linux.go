package bootreason

import "syscall"

// reboot is the system call; a variable, for tests.
var reboot = func() error { return syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART) }

// Reboot restarts the device now, for why: what has to outlive the process runs first (BeforeReboot),
// the reason is left for the next boot (Mark), and the filesystems are synced. A reboot that fails takes
// its mark back and says why; the process goes on.
func Reboot(why string) error {
	runHooks()
	Mark(why)
	syscall.Sync()
	if err := reboot(); err != nil {
		Unmark()
		return err
	}
	return nil
}
