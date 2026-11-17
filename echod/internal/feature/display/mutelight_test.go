//go:build !dot && !spot

package display

import "testing"

// noMuteLight makes the device one without a mute light (a Skylight, a tablet) for the length of t, so
// the header says the microphone is off: the tests of the pill itself.
func noMuteLight(t *testing.T) {
	t.Helper()
	was := muteLightSaysIt
	muteLightSaysIt = func() bool { return false }
	t.Cleanup(func() { muteLightSaysIt = was })
}

// Where the red mute light says the microphone is off, the header doesn't say it again; what the device
// has lost it still says (techo5#621).
func TestTheMuteLightSaysItAndTheHeaderDoesNot(t *testing.T) {
	was := muteLightSaysIt
	muteLightSaysIt = func() bool { return true }
	t.Cleanup(func() { muteLightSaysIt = was })
	if got := headerLine(scene{muted: true}); got != "" {
		t.Errorf("muted with a mute light: the header says %q", got)
	}
	if got := headerLine(scene{muted: true, linkDown: linkNoWifi}); got != tr("no_wifi") {
		t.Errorf("muted with a mute light, Wi-Fi lost: the header says %q", got)
	}
	noMuteLight(t)
	if got := headerLine(scene{muted: true}); got != tr("microphone_off") {
		t.Errorf("muted with no mute light: the header says %q", got)
	}
}
