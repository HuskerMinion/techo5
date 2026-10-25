package api

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
)

// A sub-device numbered past the assistant slots is not about listening, so a device without microphones
// keeps it (nomic.go); the slots themselves still are.
func TestOnlyTheAssistantSlotsAreListeningDevices(t *testing.T) {
	if listeningDevice(component.DeviceTessera) {
		t.Error("the Tessera screen is taken for a listening device")
	}
	for slot := range wakeword.Slots {
		if !listeningDevice(component.AssistantDevice(slot)) {
			t.Errorf("assistant slot %d is not taken for a listening device", slot)
		}
	}
	if component.DeviceTessera < component.AssistantDevice(wakeword.Slots) {
		t.Errorf("DeviceTessera %d is among the assistant slots", component.DeviceTessera)
	}
}
