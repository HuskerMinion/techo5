package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// installing is held for the whole of an install, so a second request while one is running is refused
// rather than racing it onto the same file.
var installing sync.Mutex

// Install replaces this binary with the one the manifest describes for this architecture and asks for
// a restart.
//
// ErrInstalling says an install is already running, which is what a second press of the button
// gets. It is not a failure: the first install is still going, and the caller should say nothing
// rather than report the update as failed.
var ErrInstalling = errors.New("update: an install is already running")

// Nothing is written to /system until the download has been fetched whole and its hash checked. What
// this replaces is kept as echod.prev, which is what the boot hook restores if the new one never gets
// far enough to be believed.
//
// progress is called with a fraction as the download runs, for whoever is watching in Home Assistant.
// Installing is whether an install is running now.
func Installing() bool {
	if installing.TryLock() {
		installing.Unlock()
		return false
	}
	return true
}

// phase, when not nil, is called with each named step once the download has finished — installing
// what arrived, the three-second countdown, and then the restart or the reboot itself — so a display
// can show them rather than a bar stuck at 100%.
func Install(ctx context.Context, m Manifest, progress func(float32), phase func(string)) error {
	if !installing.TryLock() {
		return ErrInstalling
	}
	defer installing.Unlock()

	if err := m.Valid(); err != nil {
		return err
	}
	if err := notOlder(m.Version, layout.Version); err != nil {
		return err
	}

	// A slot install replaces the whole rootfs, so getting into it is the device's reboot. A binary
	// swap only needs this process replaced, which the supervisor does without the device going down.
	reboot := slotSystem()
	if reboot {
		if err := installRootfs(ctx, m, progress, phase); err != nil {
			return err
		}
	} else {
		b, err := m.For(arch)
		if err != nil {
			return err
		}

		staged := filepath.Join(layout.StateDir, "echod.incoming")
		defer os.Remove(staged)
		// What an earlier try fetched of another release only takes up room, and has to go before the
		// room is measured, or a nearly full partition refuses every release after it.
		dropParts(staged+".*.part", partPath(staged, b))

		// Both partitions are asked for the room before anything is fetched. The download lands on the
		// state partition and is then copied into /system beside the binary it replaces, so the space has
		// to be there twice over — and finding that out after sixteen megabytes have been written is
		// finding it out with the device's storage already full.
		if err := room(layout.StateDir, b.Size-partial(staged, b)); err != nil {
			return err
		}
		if err := room(mount, b.Size); err != nil {
			return err
		}

		if err := download(ctx, b, staged, progress); err != nil {
			return err
		}
		// The download reaching its end is not the end of the install: what arrived still has to be
		// copied into /system, and a screen left on a bar at 100% through that looks finished while the
		// install it is describing is still going.
		report(phase, installStep)
		if err := swap(staged, m.Version); err != nil {
			return err
		}
	}

	// What follows the countdown is named for what actually happens: a slot device reboots into the
	// new rootfs, while a binary swap only has this process replaced, and a screen that called both a
	// reboot would be saying the wrong thing on one of them.
	last := restartStep(reboot)
	// The install lock is held through the countdown, so a second press in this window is refused
	// rather than starting the download over. It also gives whoever is standing at the device a
	// visible three seconds before it goes away.
	for seconds := 3; seconds >= 1; seconds-- {
		report(phase, fmt.Sprintf("%s-in-%d", last, seconds))
		time.Sleep(time.Second)
	}
	report(phase, last)

	if reboot {
		return rebootIntoSlot()
	}
	Restart("update to " + m.Version)
	return nil
}

// report passes a named step to the display, if one is listening.
func report(phase func(string), name string) {
	if phase != nil {
		phase(name)
	}
}

// installStep is the named step an install is in once the download has finished and it is writing
// what arrived: slotctl unpacking the rootfs into the other slot, or the copy into /system.
const installStep = "installing"

// installingEvery is how often that step says it is still going. It has no progress of its own to
// report — slotctl says nothing at all until it returns, which is seconds on a fast device and
// minutes on a slow one — and the display drops a dialog it has heard nothing about for a few
// minutes, so the word has to keep coming well inside that.
var installingEvery = 30 * time.Second

// restartStep is the last named step of an install, and so what its countdown counts to. A slot
// device reboots into the rootfs it has just unpacked; a binary swap only has this process replaced,
// which the supervisor does without the device going down. The display words the two differently.
func restartStep(reboot bool) string {
	if reboot {
		return "rebooting"
	}
	return "restarting"
}

// stillInstalling tells the display that the step with nothing to report is still running, every
// installingEvery, until the returned function is called. That step is the one the device must not
// lose power in, so a dialog dropped for silence there would drop the warning with it. The returned
// function is called once, when the step is over, and does not return until the reports have stopped.
func stillInstalling(phase func(string)) func() {
	if phase == nil {
		return func() {}
	}
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		tick := time.NewTicker(installingEvery)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				report(phase, installStep)
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}

// notOlder refuses a manifest offering a version below the one this binary reports as running.
//
// A signature says who wrote a manifest, not when: an old release's manifest is signed just as well as
// today's, so anything able to answer for the channel — a compromised release asset, a proxy, a name
// server on the network — can serve last month's and have it believed. Without this, that walks a
// device backwards onto a version whose holes are published in its own release notes.
//
// Home Assistant already ranks the two versions and leaves the update card off when what is offered is
// older; this makes the same answer binding where the manifest actually arrives, rather than trusting a
// card in an app that is not the thing being protected. Equal is allowed through — reinstalling what is
// already running is a repair, not a downgrade — and a version neither side can rank is left alone,
// since refusing there would strand a hand-built daemon that stamped something unusual.
func notOlder(offered, running string) error {
	if rank, ok := compareVersions(offered, running); ok && rank < 0 {
		return fmt.Errorf("update: %s is older than the running %s, and an older release is not installed over a newer one", offered, running)
	}
	return nil
}

// swap puts the new binary in place, keeping what it replaced. The order matters: the old one is moved
// aside first, so at no point is there no binary at all, and the version being tried is recorded before
// anything is replaced, so a rollback can say what it took out.
func swap(staged, version string) error {
	if err := os.WriteFile(layout.UpdatingPath, []byte(version), 0o644); err != nil {
		slog.Error("recording the version being installed failed", "err", err)
	}

	if err := writable(true); err != nil {
		return fmt.Errorf("update: remounting to install: %w", err)
	}
	defer func() {
		if err := writable(false); err != nil {
			slog.Error("remounting read-only failed", "err", err)
		}
	}()

	if err := os.Rename(layout.Binary, prev); err != nil {
		return fmt.Errorf("update: keeping the running binary: %w", err)
	}
	if err := copyTo(staged, layout.Binary); err != nil {
		// Put it back rather than leaving a device with no binary at all for the next boot to find.
		if back := os.Rename(prev, layout.Binary); back != nil {
			slog.Error("could not put the previous binary back", "err", back)
		}
		return err
	}

	// The label decides whether init will start the service at all, and which firmware this is decides
	// what it has to be. prev kept its own through the rename, so the answer is already on the device.
	if err := copyLabel(prev, layout.Binary); err != nil {
		slog.Warn("labeling the new binary failed", "err", err)
	}
	slog.Warn("update installed, restarting into it", "version", version, "previous", prev)
	return nil
}

// copyTo writes the staged binary into /system. A rename would be cheaper and atomic, but /data and
// /system are different filesystems, so there is nothing to rename across.
func copyTo(from, to string) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("update: writing %s: %w", to, err)
	}
	return dst.Sync()
}

// Serves reports whether this device could install what the manifest offers: a rootfs for its
// architecture when it boots from slots, a binary otherwise. A release made for another device (the
// Show's, seen from a Dot) is then not offered at all, rather than offered and failing on install.
func (m Manifest) Serves() bool {
	return m.serves(slotSystem())
}

// serves is Serves with the question of whether this device boots from slots answered, so a test can
// ask it for either. A slot device is served by a rootfs under its own key (rootfsArch: the 2nd gen
// Show 8's is not the other Shows'), never by the binary alone.
func (m Manifest) serves(slots bool) bool {
	if slots {
		_, ok := m.Rootfs[rootfsArch()]
		return ok
	}
	_, err := m.For(arch)
	return err == nil
}
