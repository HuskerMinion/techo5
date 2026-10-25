//go:build linux

package update

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/HuskerMinion/techo5/echod/internal/update/kernel"
)

// On the Linux image the daemon is not a file to swap but part of a root filesystem in one of
// two slots. An update there is a rootfs tarball handed to slotctl, which unpacks it into the
// other slot and marks it "trial"; the boot scripts commit it once the daemon has been up for a
// while, or fall back after three failed boots. So the daemon's own trial machinery is not
// involved: install, reboot, and the slots take it from there.

const (
	// slotFile is left by the initramfs and names the booted slot; its presence is what makes this a
	// slot system.
	slotFile = "/run/techo5/slot"

	// slotctl is the slot tool on the rootfs.
	slotctl = "/usr/local/sbin/slotctl"

	// rootfsDir is where tarballs land: the data partition survives slot changes.
	rootfsDir = "/data/techo5-linux"
)

// slotSystem reports whether this daemon runs from a rootfs slot.
func slotSystem() bool {
	_, err := os.Stat(slotFile)
	return err == nil
}

// installRootfs fetches the rootfs the manifest offers for this device (rootfsArch) and hands it to
// slotctl, which unpacks it into the other slot and marks it "trial". It does not reboot: the caller
// runs the countdown and then asks for the reboot (rebootIntoSlot), so the countdown is actually
// seen. The install lock stays held across both, since they are one install. On the 2nd gen Show 8
// slotctl checks the rootfs is athena's too (T5_BOARD in its device.conf against the store's board)
// before it empties the other slot.
func installRootfs(ctx context.Context, m Manifest, progress func(float32), phase func(string)) error {
	key := rootfsArch()
	b, ok := m.Rootfs[key]
	if !ok {
		return fmt.Errorf("update: %s carries no rootfs for %s; this device updates by slot", m.Version, key)
	}
	if err := b.valid(m.Version, key); err != nil {
		return err
	}
	// A kernel on trial (athena, update/kernel) has its tries to use first: a new slot on trial with it
	// would share its boots, and a bad kernel would take the slot down with it.
	if kernel.OnTrial(kernel.Default) {
		return fmt.Errorf("update: a new kernel is on trial; %s waits until it is over", m.Version)
	}
	if err := os.MkdirAll(rootfsDir, 0o755); err != nil {
		return err
	}
	to := filepath.Join(rootfsDir, "techo5-rootfs-"+m.Version+".tar.gz")
	sweepRootfs(to, b)
	// Before the fetch, not after it: a tarball this size arriving on a full data partition is how the
	// device ends up with no room for its own state either.
	if err := room(rootfsDir, b.Size-partial(to, b)); err != nil {
		return err
	}
	if err := download(ctx, b, to, progress); err != nil {
		return err
	}
	slog.Info("update: installing the rootfs into the other slot", "version", m.Version, "file", to)
	// A bar stuck at the end of the download is not what is happening now, and the unpack is the step
	// the device must not lose power in: the display is told so, and told again while it runs, since
	// slotctl says nothing at all until it returns.
	report(phase, installStep)
	stop := stillInstalling(phase)
	cmd := exec.CommandContext(ctx, slotctl, "install", to)
	out, err := cmd.CombinedOutput()
	stop()
	os.Remove(to)
	if err != nil {
		if bytes.Contains(out, []byte("No space left on device")) {
			// Said where Home Assistant's update card and the screen show it, not only in a log: the
			// usual reason is a store that never grew past the size it was installed at (#192).
			return fmt.Errorf("update: the store has no room for the new slot (%w). If it never grew after "+
				"the install (STORE=/store slotctl status, /data/techo5-linux/grow.log), grow it as your device's "+
				"install guide says (the Show 8 2nd gen's from TWRP: docs/install-athena.md, \"The store did not grow\")", err)
		}
		return fmt.Errorf("update: slotctl install: %w: %s", err, out)
	}
	slog.Warn("update: rootfs installed, ready to reboot into it", "version", m.Version, "slotctl", string(out))
	return nil
}

// rebootIntoSlot restarts the device into the slot that was just installed. It is called with the
// install lock still held, after the countdown, so the reboot is not lost to a deferred call that
// dies with the process (seen on the first over-the-air install, 2026-09-16 — the slot was ready,
// the device sat on the old one until someone rebooted it).
func rebootIntoSlot() error {
	syscall.Sync()
	if err := syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART); err != nil {
		return fmt.Errorf("update: rootfs installed but the reboot failed: %w", err)
	}
	return nil
}
