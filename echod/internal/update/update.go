// Package update owns replacing echod with a newer echod, and getting back to a working one when
// that goes wrong.
//
// The shape of it is that nothing trusts a new binary until it has run for a while. An update leaves
// the one it replaced beside it as echod.prev, and only a process that has been alive long enough to
// be believed deletes it. So echod.prev existing means an update is still on trial, and that single
// fact drives everything here and the boot hook that backs it up.
package update

import (
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/android/prop"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// Paths and properties are variables rather than constants so a test can point them somewhere it is
// allowed to write.
var (
	prev = layout.PrevBinary
	old  = layout.OldBinary

	mount = mountHolding(layout.Binary)

	// writable is the remount, kept as a variable so a test can run everything around it somewhere it
	// is already allowed to write.
	writable = remount
)

// mountHolding is what has to be made writable to replace a binary at this path. Which filesystem
// that is depends on the firmware: Fire OS 5 mounts the system partition at /system, Fire OS 6 runs
// it as the root filesystem, and devices in the field are on both.
func mountHolding(path string) string {
	mounts, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return "/"
	}
	return mountIn(string(mounts), path)
}

// mountIn is the longest mountpoint in /proc/mounts that contains path.
func mountIn(mounts, path string) string {
	at := "/"
	for line := range strings.SplitSeq(mounts, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		// A prefix only counts when it ends at a separator, or /sys would claim /system/app.
		point := fields[1]
		if point == path || strings.HasPrefix(path, strings.TrimSuffix(point, "/")+"/") {
			if len(point) > len(at) {
				at = point
			}
		}
	}
	return at
}

// wanted carries a request to restart into whatever is supervising the process, which is the only
// thing that can unwind the hardware cleanly. Buffered and dropped when full: two requests are one
// restart.
var wanted = make(chan string, 1)

// Restart asks for the process to be replaced by a new one of itself. It does not exit: the caller has
// no idea what state the speaker or the ring are in, and finishing with the amplifier still driven is
// what makes the device pop.
func Restart(why string) {
	select {
	case wanted <- why:
	default:
	}
}

// Wanted is how the supervisor hears about it.
func Wanted() <-chan string { return wanted }

// CallWait caps how long WaitWhile holds, and CallPoll is how often it looks; variables so a test need
// not wait real minutes.
var (
	CallWait = 10 * time.Minute
	CallPoll = time.Second
)

// WaitWhile holds until busy reports false, or until CallWait is up. A reset or a leave calls it before
// it tears the network down and restarts, with busy reporting a call or a camera talk in progress, so a
// reset accepted mid-call does not cut it off; a call that never ends still lets the reset through once
// the wait is up. A nil busy, or one already false, returns at once.
func WaitWhile(busy func() bool) {
	if busy == nil {
		return
	}
	for end := time.Now().Add(CallWait); busy() && time.Now().Before(end); {
		time.Sleep(CallPoll)
	}
}

// Remount makes the system partition writable, or puts it back. Nothing on the device can express the
// second from a shell — the kernel names the root filesystem's source /dev/root and there is no such
// node — and `adb remount` only does the first, so an installer needs this to undo itself.
func Remount(rw bool) error { return writable(rw) }

// OnTrial reports whether an update is waiting to be believed.
func OnTrial() bool {
	_, err := os.Stat(prev)
	return err == nil
}

// Start is the first thing echod does about an update it may have installed, and it either carries on
// or reboots.
//
// A trial is opened by the process that installed the update; if this process finds one already open,
// the last one took the binary and died without committing, and trying again would join init's restart
// loop forever. Rebooting hands the decision to the boot hook, which is outside the binary and can put
// the old one back.
//
// It reports whether echod is on trial, so a caller can say so and commit later, and whether a reboot
// has been asked for — in which case there is no point taking the hardware, and the caller should get
// out of the way of it.
func Start() (onTrial, rebooting bool) {
	if !OnTrial() {
		return false, false
	}

	if was, _ := prop.Get(layout.TrialProp); was != "" {
		if len(layout.AnimationScripts) == 0 {
			// No boot hook exists on this device to put the old binary back, so a reboot would only
			// run the same binary again. Do the rollback here: the previous binary goes back in place,
			// this process gets out of the way, and init starts it.
			slog.Error("this binary was already tried this boot and never settled, putting the previous one back",
				"trial", was, "prev", prev)
			rollback()
			return true, true
		}
		slog.Error("this binary was already tried this boot and never settled, rebooting to go back",
			"trial", was, "prev", prev)
		reboot()
		return true, true
	}

	if err := prop.Set(layout.TrialProp, "1"); err != nil {
		slog.Error("marking the update as on trial failed", "err", err)
	}
	slog.Warn("running an update on trial", "prev", prev)
	return true, false
}

// Commit keeps an update: the binary it replaced becomes one generation back rather than the thing a
// boot would restore. Called once echod has been running long enough to be worth believing, which is
// the only evidence available — there is nothing else to ask.
//
// Doing nothing is the normal case, since most starts have no update behind them.
func Commit() {
	if !OnTrial() {
		return
	}

	if err := writable(true); err != nil {
		slog.Error("remounting to keep an update failed", "err", err)
		return
	}
	defer func() {
		if err := writable(false); err != nil {
			slog.Error("remounting read-only failed", "err", err)
		}
	}()

	if err := os.Rename(prev, old); err != nil {
		slog.Error("keeping an update failed", "from", prev, "to", old, "err", err)
		return
	}
	if err := prop.Set(layout.TrialProp, ""); err != nil {
		slog.Error("clearing the trial property failed", "err", err)
	}
	slog.Info("update kept", "previous", old)
}

// RolledBack is what the boot hook left behind if it had to put the previous binary back, and clearing
// it is this process saying it has been noticed. Empty means the last boot was ordinary.
func RolledBack() string {
	was, err := prop.Get(layout.RolledBackProp)
	if err != nil || was == "" {
		return ""
	}

	slog.Warn("an update was rolled back before this boot", "version", was)
	if err := prop.Set(layout.RolledBackProp, ""); err != nil {
		slog.Error("clearing the rollback property failed", "err", err)
	}
	return was
}

// Stopped is a trial binary going away on purpose — a stop or restart asked for by an operator, not a
// crash. It clears the trial marker so the next start is a first start again: without this, stopping
// the daemon once during its trial would be read as the update having died, and rolled back.
func Stopped() {
	if !OnTrial() {
		return
	}
	if err := prop.Set(layout.TrialProp, ""); err != nil {
		slog.Error("clearing the trial property failed", "err", err)
	}
}

// rollback puts the previous binary back over the one on trial, for a device with no boot hook to do
// it. The version that was tried is left in the rollback property so Home Assistant hears about it.
func rollback() {
	version := "unknown"
	if b, err := os.ReadFile(layout.UpdatingPath); err == nil {
		version = strings.TrimSpace(string(b))
	}

	if err := writable(true); err != nil {
		slog.Error("remounting to roll back failed", "err", err)
		return
	}
	defer func() {
		if err := writable(false); err != nil {
			slog.Error("remounting read-only failed", "err", err)
		}
	}()

	if err := os.Rename(prev, layout.Binary); err != nil {
		slog.Error("rolling back failed", "from", prev, "to", layout.Binary, "err", err)
		return
	}
	_ = os.Remove(layout.UpdatingPath)
	if err := prop.Set(layout.TrialProp, ""); err != nil {
		slog.Error("clearing the trial property failed", "err", err)
	}
	if err := prop.Set(layout.RolledBackProp, version); err != nil {
		slog.Error("recording the rollback failed", "err", err)
	}
	slog.Warn("rolled back", "version", version, "binary", layout.Binary)
}

// reboot asks init for a clean one, which unwinds the services it started rather than dropping the
// device where it stands.
func reboot() {
	if err := prop.Set("sys.powerctl", "reboot"); err != nil {
		slog.Error("asking init to reboot failed", "err", err)
	}
}
