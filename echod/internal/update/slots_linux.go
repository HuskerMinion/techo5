//go:build linux

package update

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/HuskerMinion/techo5/echod/internal/lib/bootreason"
	"github.com/HuskerMinion/techo5/echod/internal/update/kernel"
)

// On the Linux image the daemon is not a file to swap but part of a root filesystem in one of
// two slots. An update there is a rootfs tarball handed to slotctl, which unpacks it into the
// other slot and marks it "trial"; the boot scripts commit it once the daemon has been up for a
// while, or fall back after three failed boots. So the daemon's own trial machinery is not
// involved: install, reboot, and the slots take it from there.

// The slot paths are variables so a test can point them at a store of its own and a stand-in slotctl.
var (
	// slotFile is left by the initramfs and names the booted slot; its presence is what makes this a
	// slot system.
	slotFile = "/run/techo5/slot"

	// slotctl is the slot tool on the rootfs.
	slotctl = "/usr/local/sbin/slotctl"

	// storeDir is the slot store, mounted read-only under a running slot.
	storeDir = "/store"

	// rollbackReboot restarts the device after a rollback (a stand-in in tests).
	rollbackReboot = rebootIntoSlot
)

// rootfsDir is where tarballs land: the data partition survives slot changes.
const rootfsDir = "/data/techo5-linux"

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
	bootreason.Mark(bootreason.Update)
	syscall.Sync()
	if err := syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART); err != nil {
		bootreason.Unmark()
		return fmt.Errorf("update: rootfs installed but the reboot failed: %w", err)
	}
	return nil
}

// bootedSlot is the slot the initramfs booted, a or b, or "" off a slot.
func bootedSlot() string {
	b, err := os.ReadFile(slotFile)
	if err != nil {
		return ""
	}
	return string(bytes.TrimSpace(b))
}

// OtherSlot is the slot a rollback would go back to; false on a device that does not boot from slots.
func OtherSlot() (Slot, bool) {
	if !slotSystem() {
		return Slot{}, false
	}
	return otherSlotIn(storeDir, bootedSlot()), true
}

// RollBack boots the other slot's release in place of this one: slotctl marks the booted slot bad and
// the other active (and tells the boot layer, where there is one), and the device reboots into it. It
// refuses, before anything is written, unless the other slot is good, and while an install is running,
// which is about to replace the other slot. rolled, when not nil, is called once slotctl has switched
// and before the reboot, for what has to be saved first.
func RollBack(rolled func()) error {
	if !installing.TryLock() {
		return ErrInstalling
	}
	defer installing.Unlock()

	if !slotSystem() {
		return errors.New("update: this device does not boot from slots, so there is nothing to roll back to")
	}
	booted := bootedSlot()
	other := otherSlotIn(storeDir, booted)
	if err := canRollBack(booted, other); err != nil {
		return err
	}
	slog.Warn("update: rolling back to the other slot", "from", booted, "to", other.Name, "version", other.Version)
	out, err := exec.Command(slotctl, "rollback").CombinedOutput()
	if err != nil {
		return fmt.Errorf("update: slotctl rollback: %w: %s", err, bytes.TrimSpace(out))
	}
	slog.Warn("update: rolled back, rebooting", "slotctl", string(bytes.TrimSpace(out)))
	if rolled != nil {
		rolled()
	}
	if err := rollbackReboot(); err != nil {
		return fmt.Errorf("%w (%v)", ErrRebootAfterRollback, err)
	}
	return nil
}
