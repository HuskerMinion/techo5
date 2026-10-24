//go:build panel

package privacy

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/micsilent"
)

func TestPanelHardwareAndSoftwareMuteRemainSeparate(t *testing.T) {
	Seed(false)
	micsilent.SetCut(false)
	micsilent.Set(false)
	t.Cleanup(func() { Seed(false); micsilent.SetCut(false); micsilent.Set(false) })
	m, err := Microphone()
	if err != nil {
		t.Fatal(err)
	}

	micsilent.SetCut(true)
	if got, _ := m.Get(); !got {
		t.Fatal("physical silence was not reported")
	}
	if m.(panelMute).Persisted() {
		t.Fatal("physical silence became a saved software mute")
	}
	if err := m.Set(false); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Get(); !got {
		t.Fatal("software unmute overrode the physical cut")
	}

	micsilent.SetCut(false)
	if got, _ := m.Get(); got {
		t.Fatal("unmuted hardware still reports muted")
	}
	if err := m.Set(true); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Get(); !got || !SoftwareCut() {
		t.Fatal("software mute did not cut capture")
	}
	Seed(false)
	if got, _ := m.Get(); got {
		t.Fatal("clearing the saved software cut failed")
	}
}

func TestPanelSoftwareMuteToggleAndSeed(t *testing.T) {
	Seed(false)
	micsilent.SetCut(false)
	micsilent.Set(false)
	t.Cleanup(func() { Seed(false); micsilent.SetCut(false); micsilent.Set(false) })
	m, err := Microphone()
	if err != nil || m == nil {
		t.Fatalf("no mute on a panel: %v, %v", m, err)
	}
	if err := m.Set(true); err != nil || !SoftwareCut() {
		t.Fatalf("muted: cut %v, err %v", SoftwareCut(), err)
	}
	if now, err := m.Toggle(); err != nil || now || SoftwareCut() {
		t.Errorf("toggled: %v, cut %v, err %v", now, SoftwareCut(), err)
	}
	Seed(true)
	if !SoftwareCut() {
		t.Error("a saved mute was not seeded")
	}
}
