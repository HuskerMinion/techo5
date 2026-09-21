package setup

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The word is what the devices in one house share, so it has to survive the trip through the form
// exactly as it was typed apart from the spaces somebody leaves on the end of a paste.
func TestKeepingTheHouseWord(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if problem := saveHouse("  costilla \t"); problem != "" {
		t.Fatalf("refused: %s", problem)
	}
	if got := config.Get().Home.HouseWord; got != "costilla" {
		t.Errorf("kept %q, want it trimmed", got)
	}
}

// Empty is not a mistake: it is how announcements are turned off on one device without touching the
// rest of the house.
func TestClearingTheHouseWord(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if problem := saveHouse("costilla"); problem != "" {
		t.Fatalf("refused: %s", problem)
	}
	if problem := saveHouse("   "); problem != "" {
		t.Fatalf("refused the empty one: %s", problem)
	}
	if got := config.Get().Home.HouseWord; got != "" {
		t.Errorf("kept %q, want nothing", got)
	}
}

// A word pasted out of something that wrapped it brings the newline with it, and a word with a
// newline in the middle is not the word the other devices have.
func TestAHouseWordWithALineBreak(t *testing.T) {
	if problem := saveHouse("cost\nilla"); problem == "" {
		t.Error("took it, want a complaint")
	}
}
