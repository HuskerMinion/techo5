package wakeword

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The Cancel request button asks the conversation once per press.
func TestTheCancelButtonAsks(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	asked := 0
	t.Cleanup(CancelRequested.Listen(func(struct{}) { asked++ }))

	Get().cancel.OnPress()
	if asked != 1 {
		t.Errorf("one press asked %d times, want once", asked)
	}
}
