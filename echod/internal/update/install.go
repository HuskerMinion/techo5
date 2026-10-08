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

func Install(ctx context.Context, m Manifest, progress func(float32)) error {
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
	if slotSystem() {
		return installRootfs(ctx, m, progress)
	}
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
	return swap(staged, m.Version)
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
	if slotSystem() {
		_, ok := m.Rootfs[arch]
		return ok
	}
	_, err := m.For(arch)
	return err == nil
}
