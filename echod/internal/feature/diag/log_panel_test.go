//go:build panel

package diag

import (
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/android/logd"
	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A panel's bundle has the daemon's log, from what the handler kept rather than the log file nothing
// writes on a tablet, with its addresses taken out like the rest (techo5-panel#1).
func TestAPanelsBundleHasItsLog(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	log := slog.New(logd.NewHandler("techo5", io.Discard))
	log.Info("speaker ready", "host", "192.168.1.10")
	log.Info("wake detected slot=1 id=alexa peak=0.97 crossing=0.9 cutoff=0.85 dropped=0")

	got := Bundle()
	_, logPart, ok := strings.Cut(got, "=== daemon log")
	if !ok {
		t.Fatalf("no log section in %q", got)
	}
	logPart, wakes, _ := strings.Cut(logPart, "=== wake words")
	if !strings.Contains(logPart, "] speaker ready host=") {
		t.Errorf("the log section has no line from the daemon: %q", logPart)
	}
	if strings.Contains(got, "192.168.1.10") {
		t.Error("the address was left in")
	}
	if !strings.Contains(wakes, "wake detected slot=1") {
		t.Errorf("the wake section has no wake: %q", wakes)
	}
}
