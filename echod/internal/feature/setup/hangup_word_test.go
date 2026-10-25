package setup

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A model chosen as the hang-up word counts as listened for, so the page will not remove it from under
// the call that needs it. The stop word and None name no model.
func TestTheHangupWordIsListenedFor(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if listening("hang_up") {
		t.Fatal("a model nothing chose counts as listened for")
	}
	if err := config.Set().Hangup().ID("hang_up"); err != nil {
		t.Fatal(err)
	}
	if !listening("hang_up") {
		t.Error("the hang-up word's model can be removed from the page")
	}
	if err := config.Set().Hangup().ID(config.HangupStop); err != nil {
		t.Fatal(err)
	}
	if listening(config.HangupStop) {
		t.Error("the stop word chosen counts as a model listened for")
	}
}
