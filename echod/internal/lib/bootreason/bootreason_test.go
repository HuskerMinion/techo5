package bootreason

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAMarkIsWhatTheNextBootReads(t *testing.T) {
	dir := t.TempDir()
	markFile = filepath.Join(dir, "restart-reason")
	Mark(Update)
	if b, err := os.ReadFile(markFile); err != nil || string(b) != "update\n" {
		t.Errorf("mark reads %q (%v), want update", b, err)
	}

	markFile = filepath.Join(dir, "missing", "restart-reason")
	Mark(Restart)
	if _, err := os.Stat(markFile); err == nil {
		t.Error("a mark was written where the folder is not there")
	}
}

func TestLastIsWhatBootFound(t *testing.T) {
	lastFile = filepath.Join(t.TempDir(), "boot-reason")
	if _, ok := Last(); ok {
		t.Error("a reason with no file")
	}
	if err := os.WriteFile(lastFile, []byte("watchdog\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r, ok := Last(); !ok || r != "watchdog" {
		t.Errorf("Last is %q, %v; want watchdog", r, ok)
	}
}
