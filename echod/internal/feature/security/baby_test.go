package security

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The baby monitor starts off with no link, makes its key the first time it is turned on, keeps it
// while it is off, and replaces it on request.
func TestTheBabyMonitorSwitch(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	f := Get()

	if s := config.Get().Security; s.Baby || s.BabyKey != "" {
		t.Fatalf("on a new device: baby %v, key %q", s.Baby, s.BabyKey)
	}
	if f.State().Baby {
		t.Error("the screen would show it on")
	}
	found := false
	for _, e := range f.Entities() {
		found = found || e.Object() == "baby_monitor"
	}
	if !found {
		t.Error("the switch is not offered on a device with microphones")
	}

	f.SetBaby(false)
	if config.Get().Security.BabyKey != "" {
		t.Error("a key was made by turning it off")
	}
	f.SetBaby(true)
	first := config.Get().Security.BabyKey
	if !config.Get().Security.Baby || len(first) < 22 {
		t.Fatalf("after turning it on: on %v, key %q", config.Get().Security.Baby, first)
	}
	f.SetBaby(false)
	if config.Get().Security.Baby || config.Get().Security.BabyKey != first {
		t.Error("turning it off lost the key or left it on")
	}
	f.SetBaby(true)
	if config.Get().Security.BabyKey != first {
		t.Error("turning it on again changed the link")
	}
	if err := f.ReplaceBabyKey(); err != nil || config.Get().Security.BabyKey == first {
		t.Errorf("the key was not replaced: %v", err)
	}
	if a, b := NewBabyKey(), NewBabyKey(); a == b {
		t.Error("two keys alike")
	}

	// Restored after a restart as the device's own setting.
	f.Restore(config.Get())
	if !f.State().Baby && !config.Get().Security.Baby {
		t.Error("lost across a restart")
	}
}
