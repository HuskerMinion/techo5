package security

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// /screen.png's key is made the first time Screen web access goes on, kept while it is off, and
// replaced on request; a device that had the switch on before there was a key gets one at start.
func TestTheScreenPagesKey(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	f := build()

	if s := config.Get().Security; s.Screen || s.ScreenKey != "" {
		t.Fatalf("on a new device: screen %v, key %q", s.Screen, s.ScreenKey)
	}
	f.SetScreen(false)
	if config.Get().Security.ScreenKey != "" {
		t.Error("a key was made by turning it off")
	}
	f.SetScreen(true)
	first := config.Get().Security.ScreenKey
	if !config.Get().Security.Screen || len(first) < 22 {
		t.Fatalf("after turning it on: on %v, key %q", config.Get().Security.Screen, first)
	}
	f.SetScreen(false)
	f.SetScreen(true)
	if config.Get().Security.ScreenKey != first {
		t.Error("turning it off and on changed the key, and with it the address in Home Assistant")
	}
	if err := f.ReplaceScreenKey(); err != nil || config.Get().Security.ScreenKey == first {
		t.Errorf("the key was not replaced: %v", err)
	}

	// The update that brought the key finds the switch on and no key: it makes one at start, where the
	// device has the page at all.
	if err := config.Set().Security().ScreenKey(""); err != nil {
		t.Fatal(err)
	}
	f.Restore(config.Get())
	if got := config.Get().Security.ScreenKey; (got != "") != webPages {
		t.Errorf("after a start with the switch on and no key: key %q on a device with screen pages = %t", got, webPages)
	}
}
