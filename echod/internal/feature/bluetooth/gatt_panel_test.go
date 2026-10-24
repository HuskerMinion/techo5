//go:build panel

package bluetooth

import (
	"encoding/json"
	"testing"
)

// The UUID halves are the ESPHome wire order: most significant then least significant.
func TestPanelGATTUUIDWireOrder(t *testing.T) {
	got := parseUUID("00001800-0000-1000-8000-00805f9b34fb")
	if len(got) != 2 || got[0] != 0x0000180000001000 || got[1] != 0x800000805f9b34fb {
		t.Fatalf("Bluetooth base UUID halves: %#v", got)
	}
	got = parseUUID("1bc5ffa0-0200-62ab-e411-f254e005dbd4")
	if len(got) != 2 || got[0] != 0x1bc5ffa0020062ab || got[1] != 0xe411f254e005dbd4 {
		t.Fatalf("Mooshimeter UUID halves: %#v", got)
	}
}

func TestPanelGATTServiceFrame(t *testing.T) {
	frame := []byte(`{"type":"services","addr":"001122334455","services":[{"uuid":"1bc5ffa0-0200-62ab-e411-f254e005dbd4","handle":1,"characteristics":[{"uuid":"1bc5ffa1-0200-62ab-e411-f254e005dbd4","handle":2,"properties":8,"descriptors":[{"uuid":"00002902-0000-1000-8000-00805f9b34fb","handle":3}]}]}]}`)
	var event gattEvent
	if err := json.Unmarshal(frame, &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "services" || len(event.Services) != 1 {
		t.Fatalf("event: %#v", event)
	}
	service := convertService(event.Services[0])
	if service == nil || service.Handle != 1 || len(service.Characteristics) != 1 {
		t.Fatalf("service: %#v", service)
	}
	ch := service.Characteristics[0]
	if ch.Handle != 2 || ch.Properties != 8 || len(ch.Descriptors) != 1 || ch.Descriptors[0].Handle != 3 {
		t.Fatalf("characteristic: %#v", ch)
	}
}
