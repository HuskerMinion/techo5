package api

import (
	"os"
	"path/filepath"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Leaving forgets the access and puts a new key in place of the old one: a real key, never zeros
// (which would let any Home Assistant set its own), and one the device can read back.
func TestLeaveForgetsTheAccessAndChangesTheKey(t *testing.T) {
	dir := t.TempDir()
	config.Use(filepath.Join(dir, "state.json"))
	_ = config.Set().Realtime().SetKey("sk-old-owner")
	_ = config.Set().Realtime().SetMCPToken("ha-old-owner")
	keyPath, hassPath := filepath.Join(dir, "psk"), filepath.Join(dir, "hass.json")
	var old esphome.PSK
	old[0] = 7
	if err := writePSK(keyPath, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hassPath, []byte(`{"url":"http://ha:8123","token":"t"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	discarded := 0
	wasDiscard := discardSendspin
	discardSendspin = func() error { discarded++; return nil }
	t.Cleanup(func() { discardSendspin = wasDiscard })

	if err := leave(keyPath, hassPath); err != nil {
		t.Fatal(err)
	}
	if discarded != 1 {
		t.Errorf("the Sendspin identity was discarded %d times, want once", discarded)
	}
	if _, err := os.Stat(hassPath); !os.IsNotExist(err) {
		t.Errorf("the Home Assistant access is still there (%v)", err)
	}
	if rt := config.Get().Realtime; rt.Key != "" || rt.MCPToken != "" {
		t.Errorf("realtime voice's key or token is still there: %v %v", rt.Key != "", rt.MCPToken != "")
	}
	k, err := loadPSK(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if *k == old || *k == (esphome.PSK{}) {
		t.Errorf("the key after leaving is %v", *k)
	}
	if _, err := os.Stat(keyPath + ".new"); !os.IsNotExist(err) {
		t.Error("the key's temporary file was left behind")
	}

	// A device with no access saved leaves just the same.
	if err := leave(keyPath, hassPath); err != nil {
		t.Errorf("leaving twice: %v", err)
	}
}
