//go:build panel

package privacy

import "testing"

// A panel's mute is the daemon's own, as the Spot's is: no line of the Shows' is looked for, and a
// mute saved from last time is in force before capture opens.
func TestAPanelMutesInSoftware(t *testing.T) {
	t.Cleanup(func() { softwareCut.Store(false) })
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
