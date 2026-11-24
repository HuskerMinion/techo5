//go:build linux

package bootreason

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Every reboot runs what was registered first, and a reboot that fails takes its mark back.
func TestRebootRunsTheHooksFirst(t *testing.T) {
	oldReboot, oldMark, oldHooks := reboot, markFile, hooks
	t.Cleanup(func() { reboot, markFile, hooks = oldReboot, oldMark, oldHooks })
	markFile = filepath.Join(t.TempDir(), "restart-reason")
	hooks = nil
	var order []string
	BeforeReboot(func() { order = append(order, "hook") })
	reboot = func() error { order = append(order, "reboot"); return errors.New("refused") }
	if err := Reboot(Restart); err == nil {
		t.Fatal("a failed reboot said nothing")
	}
	if len(order) != 2 || order[0] != "hook" || order[1] != "reboot" {
		t.Errorf("ran %v", order)
	}
	if _, err := os.Stat(markFile); err == nil {
		t.Error("a failed reboot left its mark")
	}
}

// A hook that panics or never returns does not stop the reboot.
func TestAStuckOrPanickingHookDoesNotStopTheReboot(t *testing.T) {
	oldReboot, oldMark, oldHooks := reboot, markFile, hooks
	t.Cleanup(func() { reboot, markFile, hooks = oldReboot, oldMark, oldHooks })
	markFile = filepath.Join(t.TempDir(), "restart-reason")
	hooks = nil
	BeforeReboot(func() { panic("a hook went wrong") })
	BeforeReboot(func() { select {} })
	rebooted := false
	reboot = func() error { rebooted = true; return nil }
	start := time.Now()
	if err := Reboot(Restart); err != nil {
		t.Fatal(err)
	}
	if !rebooted {
		t.Fatal("did not reboot")
	}
	if took := time.Since(start); took > hookTime+2*time.Second {
		t.Errorf("waited %v for the hooks", took)
	}
}
