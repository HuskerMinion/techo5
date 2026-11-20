//go:build linux

package update

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rollbackRig points RollBack at a store of its own, a booted slot, a stand-in slotctl that records
// that it ran (and exits with status), and a reboot that only counts. It returns whether slotctl ran
// and how many reboots were asked for.
func rollbackRig(t *testing.T, booted string, slots map[string][2]string, status int, rebootErr error) (ran func() bool, reboots *int) {
	t.Helper()
	dir := store(t, slots)
	run := t.TempDir()

	slot := filepath.Join(run, "slot")
	if booted != "" {
		if err := os.WriteFile(slot, []byte(booted+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(run, "slotctl-ran")
	tool := filepath.Join(run, "slotctl")
	script := "#!/bin/sh\necho \"$@\" > " + marker + "\necho 'slotctl: slot b marked bad; next boot: slot a'\nexit " +
		string(rune('0'+status)) + "\n"
	if err := os.WriteFile(tool, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	n := 0
	restore := []func(){}
	for _, r := range []struct {
		p *string
		v string
	}{{&slotFile, slot}, {&storeDir, dir}, {&slotctl, tool}} {
		old := *r.p
		*r.p = r.v
		p := r.p
		restore = append(restore, func() { *p = old })
	}
	oldReboot := rollbackReboot
	rollbackReboot = func() error { n++; return rebootErr }
	t.Cleanup(func() {
		for _, f := range restore {
			f()
		}
		rollbackReboot = oldReboot
	})
	return func() bool {
		b, err := os.ReadFile(marker)
		return err == nil && strings.TrimSpace(string(b)) == "rollback"
	}, &n
}

// A press is turned away before slotctl runs whenever the other slot is not good, the device did not
// boot from a slot, or an install (or another rollback) holds the lock: nothing is written, nothing
// is saved, and the device does not restart.
func TestRollBackRefusesBeforeSlotctlRuns(t *testing.T) {
	good := [2]string{"good", release40}
	cases := []struct {
		why    string
		booted string
		other  [2]string
		locked bool
		want   string
	}{
		{"the other slot is bad", "b", [2]string{"bad", release40}, false, "marked bad"},
		{"the other slot is on trial", "b", [2]string{"trial 2", release40}, false, "on trial"},
		{"the other slot was never installed", "b", [2]string{"", ""}, false, "never been installed"},
		{"an install or a rollback is running", "b", good, true, "already running"},
	}
	for _, c := range cases {
		t.Run(c.why, func(t *testing.T) {
			ran, reboots := rollbackRig(t, c.booted, map[string][2]string{"a": c.other, "b": good}, 0, nil)
			if c.locked {
				installing.Lock()
				defer installing.Unlock()
			}
			saved := false
			err := RollBack(func() { saved = true })
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("RollBack = %v, want it to say %q", err, c.want)
			}
			if c.locked && !errors.Is(err, ErrInstalling) {
				t.Errorf("a press under the lock: %v, want ErrInstalling", err)
			}
			if ran() || saved || *reboots != 0 {
				t.Errorf("refused, yet slotctl ran=%v, saved=%v, reboots=%d", ran(), saved, *reboots)
			}
		})
	}
}

// Off a slot (no /run/techo5/slot) there is nothing to roll back to, and slotctl is not asked.
func TestRollBackOffASlotDevice(t *testing.T) {
	ran, reboots := rollbackRig(t, "", map[string][2]string{"a": {"good", release40}}, 0, nil)
	if err := RollBack(nil); err == nil || !strings.Contains(err.Error(), "does not boot from slots") {
		t.Fatalf("RollBack off a slot device = %v", err)
	}
	if ran() || *reboots != 0 {
		t.Errorf("slotctl ran=%v, reboots=%d", ran(), *reboots)
	}
}

// To a good slot: slotctl rollback runs, what has to be saved is saved after it and before the
// reboot, and the device restarts.
func TestRollBackToAGoodSlotRunsSlotctlAndReboots(t *testing.T) {
	ran, reboots := rollbackRig(t, "b", map[string][2]string{"a": {"good", release40}, "b": {"good", release40}}, 0, nil)
	saved := false
	if err := RollBack(func() {
		if !ran() {
			t.Error("saved before slotctl ran")
		}
		saved = true
	}); err != nil {
		t.Fatalf("RollBack to a good slot: %v", err)
	}
	if !ran() || !saved || *reboots != 1 {
		t.Errorf("slotctl ran=%v, saved=%v, reboots=%d", ran(), saved, *reboots)
	}
	if Installing() {
		t.Error("the lock is still held after the rollback")
	}
}

// slotctl refusing (it checks the slots again) is a failure, with nothing saved and no restart.
func TestRollBackWhenSlotctlFails(t *testing.T) {
	ran, reboots := rollbackRig(t, "b", map[string][2]string{"a": {"good", release40}, "b": {"good", release40}}, 1, nil)
	saved := false
	err := RollBack(func() { saved = true })
	if err == nil || !strings.Contains(err.Error(), "slotctl rollback") {
		t.Fatalf("RollBack with slotctl failing = %v", err)
	}
	if !ran() || saved || *reboots != 0 {
		t.Errorf("slotctl ran=%v, saved=%v, reboots=%d", ran(), saved, *reboots)
	}
}

// A reboot that fails after slotctl switched says the rollback happened and the device needs a
// restart, rather than that it was refused.
func TestRollBackWhenTheRebootFails(t *testing.T) {
	ran, reboots := rollbackRig(t, "b", map[string][2]string{"a": {"good", release40}, "b": {"good", release40}}, 0,
		errors.New("operation not permitted"))
	err := RollBack(nil)
	if !errors.Is(err, ErrRebootAfterRollback) {
		t.Fatalf("RollBack with the reboot failing = %v, want ErrRebootAfterRollback", err)
	}
	if !strings.Contains(err.Error(), "rolled back, but the reboot failed; restart the device") {
		t.Errorf("message = %q", err)
	}
	if !ran() || *reboots != 1 {
		t.Errorf("slotctl ran=%v, reboots=%d", ran(), *reboots)
	}
}
