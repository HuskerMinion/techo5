//go:build spot

package privacy

import "testing"

// The Spot's mute is the daemon's alone: setting it is what the capture source reads, and nothing but
// the daemon moves it.
func TestSoftwareMuteIsWhatTheCaptureReads(t *testing.T) {
	m, err := Microphone()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Set(false) })

	if m.HardwareActs(false) || m.HardwareActs(true) {
		t.Fatal("a software mute must be toggled by the daemon, not reported as already toggled")
	}
	if SoftwareCut() {
		t.Fatal("starts cut")
	}
	if err := m.Set(true); err != nil {
		t.Fatal(err)
	}
	if muted, _ := m.Get(); !muted || !SoftwareCut() {
		t.Fatalf("after Set(true): Get %v, SoftwareCut %v", muted, SoftwareCut())
	}
	if now, err := m.Toggle(); err != nil || now || SoftwareCut() {
		t.Fatalf("after Toggle: now %v, err %v, SoftwareCut %v", now, err, SoftwareCut())
	}
}
