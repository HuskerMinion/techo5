//go:build linux

package bluez

import "testing"

// Pairing is answered only in pairing mode; connections only for the audio profiles.
func TestAgentRules(t *testing.T) {
	on := false
	var heard []string
	g := agent{pairing: func() bool { return on }, paired: func(p string) { heard = append(heard, p) }}

	if g.RequestConfirmation("/x", 123456) == nil || g.RequestAuthorization("/x") == nil {
		t.Error("a pairing was accepted outside pairing mode")
	}
	if _, err := g.RequestPinCode("/x"); err == nil {
		t.Error("a PIN was given outside pairing mode")
	}
	if len(heard) != 0 {
		t.Errorf("refused pairings were reported as paired: %v", heard)
	}
	on = true
	if g.RequestConfirmation("/dev_a", 123456) != nil || g.RequestAuthorization("/dev_b") != nil {
		t.Error("a pairing was refused in pairing mode")
	}
	if len(heard) != 2 || heard[0] != "/dev_a" || heard[1] != "/dev_b" {
		t.Errorf("accepted pairings reported as %v", heard)
	}

	for uuid, want := range map[string]bool{
		"0000110B-0000-1000-8000-00805F9B34FB": true,  // Audio Sink, upper case
		"0000110e-0000-1000-8000-00805f9b34fb": true,  // AVRCP
		"00001124-0000-1000-8000-00805f9b34fb": false, // HID
		"00001116-0000-1000-8000-00805f9b34fb": false, // PAN NAP
		"00001101-0000-1000-8000-00805f9b34fb": false, // Serial Port
	} {
		if got := g.AuthorizeService("/x", uuid) == nil; got != want {
			t.Errorf("%s allowed=%v, want %v", uuid, got, want)
		}
	}
}

// A pairing Home Assistant asked the proxy for is answered yes outside pairing mode, for that device
// only, and only while it is under way.
func TestAgentAllowsProxyPairing(t *testing.T) {
	g := agent{pairing: func() bool { return false }}
	if g.RequestAuthorization("/dev_bulb") == nil {
		t.Fatal("accepted with nothing asked")
	}
	proxyPairingSet("/dev_bulb", true)
	if g.RequestAuthorization("/dev_bulb") != nil || g.RequestConfirmation("/dev_bulb", 1) != nil {
		t.Error("refused the pairing the proxy started")
	}
	if g.RequestAuthorization("/dev_phone") == nil {
		t.Error("accepted another device")
	}
	proxyPairingSet("/dev_bulb", false)
	if g.RequestAuthorization("/dev_bulb") == nil {
		t.Error("still accepting after the pairing ended")
	}
}

// A feature pairing one device of its own — the wall clock — gets that device through and nothing
// else, until the window it opened closes.
func TestAgentAllowsOneDevicePairing(t *testing.T) {
	g := agent{pairing: func() bool { return false }}
	done := AllowPairing("/dev_clock")

	if g.RequestConfirmation("/dev_clock", 123456) != nil || g.RequestAuthorization("/dev_clock") != nil {
		t.Error("refused the device the window was opened for")
	}
	if g.RequestAuthorization("/dev_phone") == nil {
		t.Error("accepted another device")
	}

	done()
	if g.RequestConfirmation("/dev_clock", 123456) == nil {
		t.Error("still accepting after the window closed")
	}
	done() // idempotent: a caller may both defer it and call it early
}
