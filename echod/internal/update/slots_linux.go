//go:build linux

package update

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
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

// installRootfs fetches the rootfs the manifest offers for this architecture, hands it to
// slotctl, and reboots into it.
func installRootfs(ctx context.Context, m Manifest, progress func(float32)) error {
	b, ok := m.Rootfs[arch]
	if !ok {
		return fmt.Errorf("update: %s carries no rootfs for %s; this device updates by slot", m.Version, arch)
	}
	if err := b.valid(m.Version, arch); err != nil {
		return err
	}
	if err := os.MkdirAll(rootfsDir, 0o755); err != nil {
		return err
	}
	// Before the fetch, not after it: a tarball this size arriving on a full data partition is how the
	// device ends up with no room for its own state either.
	if err := room(rootfsDir, b.Size); err != nil {
		return err
	}
	to := filepath.Join(rootfsDir, "techo5-rootfs-"+m.Version+".tar.gz")
	if err := download(ctx, b, to, progress); err != nil {
		os.Remove(to)
		return err
	}
	slog.Info("update: installing the rootfs into the other slot", "version", m.Version, "file", to)
	cmd := exec.CommandContext(ctx, slotctl, "install", to)
	out, err := cmd.CombinedOutput()
	os.Remove(to)
	if err != nil {
		return fmt.Errorf("update: slotctl install: %w: %s", err, out)
	}
	slog.Warn("update: rootfs installed, rebooting into it", "version", m.Version, "slotctl", string(out))
	// Reboot here and now. Anything deferred loses: the caller asks the supervisor for a restart
	// as soon as this returns, and a goroutine waiting to reboot dies with the process (seen on
	// the first over-the-air install, 2026-09-16 — the slot was ready, the device sat on the old
	// one until someone rebooted it).
	syscall.Sync()
	if err := syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART); err != nil {
		return fmt.Errorf("update: rootfs installed but the reboot failed: %w", err)
	}
	return nil
}
