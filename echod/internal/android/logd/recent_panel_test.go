//go:build panel

package logd

import (
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// A panel's handler keeps its lines as a log file has them, for the diagnostics bundle.
func TestAPanelKeepsItsLog(t *testing.T) {
	slog.New(&Handler{fallback: io.Discard, mu: new(sync.Mutex)}).Warn("playback underrun", "times", 3)
	got := Recent()
	if len(got) == 0 || !strings.HasPrefix(got[len(got)-1], "W [") || !strings.HasSuffix(got[len(got)-1], "] playback underrun times=3") {
		t.Errorf("kept %q, want the line as a log file has it", got)
	}
}
