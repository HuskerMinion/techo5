//go:build linux

package btaudio

import "testing"

func TestSameState(t *testing.T) {
	base := State{Available: true, Connected: "Speaker", Devices: []Device{{Address: "a", RSSI: -50}}}
	same := base
	same.Devices = []Device{{Address: "a", RSSI: -50}}
	if !sameState(base, same) {
		t.Error("equal states compare different")
	}
	for name, change := range map[string]func(*State){
		"pairing":   func(s *State) { s.Pairing = true },
		"phone":     func(s *State) { s.Phone = true },
		"connected": func(s *State) { s.Connected = "" },
		"status":    func(s *State) { s.Status = "Connecting" },
		"rssi":      func(s *State) { s.Devices = []Device{{Address: "a", RSSI: -40}} },
		"devices":   func(s *State) { s.Devices = nil },
	} {
		s := base
		change(&s)
		if sameState(base, s) {
			t.Errorf("%s: a change compares equal", name)
		}
	}
}
