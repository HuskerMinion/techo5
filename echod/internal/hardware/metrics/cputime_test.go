package metrics

import (
	"os"
	"path/filepath"
	"testing"
)

// Idle and waiting for I/O are not busy: threads asleep in a driver push the load average up and
// cost nothing.
func TestCPUTimeCountsOnlyBusyTime(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "proc"), 0o755); err != nil {
		t.Fatal(err)
	}
	stat := "cpu  100 20 30 800 50 0 0 0 0 0\ncpu0 50 10 15 400 25 0 0 0 0 0\nbtime 1700000000\n"
	if err := os.WriteFile(filepath.Join(root, "proc", "stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}

	busy, total, ok := Reader{Root: root}.CPUTime()
	if !ok || busy != 150 || total != 1000 {
		t.Errorf("busy %d of %d (ok %v), want 150 of 1000", busy, total, ok)
	}
	if _, _, ok := (Reader{Root: t.TempDir()}).CPUTime(); ok {
		t.Error("read a CPU time with no /proc/stat")
	}
}
