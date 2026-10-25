//go:build !panel

package logd

import (
	"io"
	"log/slog"
	"sync"
	"testing"
)

// Off a panel the log is in its file, and nothing is kept in memory.
func TestOnlyAPanelKeepsItsLog(t *testing.T) {
	slog.New(&Handler{fallback: io.Discard, mu: new(sync.Mutex)}).Warn("playback underrun", "times", 3)
	if got := Recent(); len(got) != 0 {
		t.Errorf("kept %q", got)
	}
}
