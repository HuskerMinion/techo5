package camstream

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Discovery tells anyone who asks the device's name, model and MAC address, so a device starts with it
// off, and its switch is offered with the stream's.
func TestDiscoveryIsOffUntilTurnedOn(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if config.Get().CamStream.Discovery {
		t.Fatal("discovery is on in a fresh configuration")
	}
	f := build()
	f.available = func() bool { return true }
	var ids []string
	for _, e := range f.Entities() {
		if b, ok := e.(interface{ Object() string }); ok {
			ids = append(ids, b.Object())
		}
	}
	if !strings.Contains(strings.Join(ids, " "), "camera_discovery") {
		t.Errorf("entities %v: no discovery switch", ids)
	}
	if err := config.Set().CamStream().Discovery(true); err != nil {
		t.Fatal(err)
	}
	if !config.Get().CamStream.Discovery {
		t.Error("the switch was not kept")
	}
}

// A Probe is answered for a video transmitter, a device, or anything; not for another kind.
func TestDiscoveryAnswersCamerasOnly(t *testing.T) {
	for types, want := range map[string]bool{
		"":                           true,
		"dn:NetworkVideoTransmitter": true,
		"tds:Device":                 true,
		"NetworkVideoTransmitter":    true,
		"dp0:NetworkVideoDisplay":    false,
		"wsdp:Device dn:Unrelated":   true,
		"pub:Computer pub:Printer":   false,
	} {
		if got := wantedTypes(types); got != want {
			t.Errorf("wantedTypes(%q) = %v, want %v", types, got, want)
		}
	}
}
