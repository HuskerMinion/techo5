package setup

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A model chosen as the volume down word counts as listened for, so the page will neither remove it nor
// replace it under the engine that has it loaded.
func TestTheVolumeDownWordCountsAsListening(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if listening("turn_it_down") {
		t.Fatal("listening before it was chosen")
	}
	if err := config.Set().VolumeDown().ID("turn_it_down"); err != nil {
		t.Fatal(err)
	}
	if !listening("turn_it_down") {
		t.Error("the volume down word's model does not count as listening")
	}
	if listening("hey_jarvis") {
		t.Error("another model counts as listening")
	}
}
