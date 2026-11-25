package diag

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Purge cache leaves the volume down word's model alone, as it does the wake words': deleting it would
// leave the word chosen and silent.
func TestThePurgeKeepsTheVolumeDownWord(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if slices.Contains(inUse(), "turn_it_down") {
		t.Fatal("in use before it was chosen")
	}
	if err := config.Set().VolumeDown().ID("turn_it_down"); err != nil {
		t.Fatal(err)
	}
	if got := inUse(); !slices.Contains(got, "turn_it_down") {
		t.Errorf("in use %v, want the volume down word among them", got)
	}
}

// And the stop word's, when one of the device's models is chosen for it (#741).
func TestThePurgeKeepsTheStopWord(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if slices.Contains(inUse(), "stopp") {
		t.Fatal("in use before it was chosen")
	}
	if err := config.Set().Stop().ID("stopp"); err != nil {
		t.Fatal(err)
	}
	if got := inUse(); !slices.Contains(got, "stopp") {
		t.Errorf("in use %v, want the stop word among them", got)
	}
}
