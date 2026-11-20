package update

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A slot device keeps the release it ran before in its other slot, and slotctl rollback marks the
// booted slot bad and boots that one. It is what to do about a regression that shows up after the
// new slot has committed itself, when the trial is over and nothing rolls back by itself. Home
// Assistant gets it as a button (feature/firmware); what is here is the part that decides whether
// a rollback is safe, and the slot files it decides from.

// ErrRebootAfterRollback is a rollback that slotctl made but the device did not restart into: the
// other slot boots next, whenever the device next starts.
var ErrRebootAfterRollback = errors.New("update: rolled back, but the reboot failed; restart the device")

// Slot is the slot a rollback would go back to, as the store describes it.
type Slot struct {
	// Name is a or b.
	Name string
	// State is good, bad or trial <tries left> from slots/<name>.state, or "none" for a slot never
	// installed.
	State string
	// Version is the release in the slot, from its etc/techo5-release, or "" when there is none.
	Version string
}

// String is the slot for a sensor: "v1.6.40 (good)", or "empty" for a slot never installed.
func (s Slot) String() string {
	if s.State == "none" && s.Version == "" {
		return "empty"
	}
	v := s.Version
	if v == "" {
		v = "unknown version"
	}
	return v + " (" + s.State + ")"
}

// otherSlotIn reads the slot that is not booted from the store at store. booted is the slot the
// initramfs booted (a or b); anything else leaves the other slot unnamed.
func otherSlotIn(store, booted string) Slot {
	var name string
	switch booted {
	case "a":
		name = "b"
	case "b":
		name = "a"
	default:
		return Slot{State: "none"}
	}
	s := Slot{Name: name, State: "none"}
	if b, err := os.ReadFile(filepath.Join(store, "slots", name+".state")); err == nil {
		if st := strings.TrimSpace(string(b)); st != "" {
			s.State = st
		}
	}
	if b, err := os.ReadFile(filepath.Join(store, "slots", name, "etc", "techo5-release")); err == nil {
		s.Version = releaseVersion(string(b))
	}
	return s
}

// releaseVersion is the version in an etc/techo5-release line ("techo5 rootfs v1.6.40 built
// 2026-11-10T09:12Z, daemon ..."), or the line itself when it is not in that form.
func releaseVersion(line string) string {
	line = strings.TrimSpace(strings.SplitN(line, "\n", 2)[0])
	f := strings.Fields(line)
	if len(f) >= 3 && f[0] == "techo5" && f[1] == "rootfs" {
		return f[2]
	}
	return line
}

// canRollBack says why a rollback from booted to other would leave the device without a system it
// knows to work, or nil when it would not. Only a slot that committed itself (good) is one to go
// back to: an empty slot has nothing in it, a bad one failed, and one on trial never proved itself.
// slotctl refuses the same cases; asking first gives Home Assistant a reason rather than an exit
// status.
func canRollBack(booted string, other Slot) error {
	if booted != "a" && booted != "b" {
		return fmt.Errorf("update: this device did not boot from a slot (%q), so there is no other one to roll back to", booted)
	}
	switch {
	case other.State == "good":
		return nil
	case other.State == "none":
		return fmt.Errorf("update: slot %s has never been installed; there is nothing to roll back to", other.Name)
	case other.State == "bad":
		return fmt.Errorf("update: slot %s (%s) is marked bad; rolling back to it is refused", other.Name, other.Version)
	case strings.HasPrefix(other.State, "trial"):
		return fmt.Errorf("update: slot %s (%s) is on trial and never committed; rolling back to it is refused", other.Name, other.Version)
	default:
		return fmt.Errorf("update: slot %s is %q, not good; rolling back to it is refused", other.Name, other.State)
	}
}

// HeldBack says whether offered is the release a rollback went back from, or one no newer than it,
// so installing updates by themselves leaves it alone until a newer one is out. held is "" when
// nothing was rolled back from. A version neither can be ranked against is held only when it is
// the same one.
func HeldBack(offered, held string) bool {
	if held == "" || offered == "" {
		return false
	}
	if offered == held {
		return true
	}
	rank, ok := compareVersions(offered, held)
	return ok && rank <= 0
}
