package diag

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Purge cache leaves the hang-up word's model alone, as it does the wake words'.
func TestPurgeKeepsTheHangupWord(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Hangup().ID("hang_up"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(inUse(), "hang_up") {
		t.Errorf("in use %v, without the hang-up word", inUse())
	}
	if err := config.Set().Hangup().ID(config.HangupStop); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(inUse(), config.HangupStop) {
		t.Error("the stop word chosen is kept as a model")
	}
}
