package mic

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDumpLengthFitsTheRoom(t *testing.T) {
	// 10 s of the Dot 5's native stream: 96 kHz, six slots of four bytes.
	if got := dumpLength(10, 96000, 24, 1<<40); got != 23040000 {
		t.Errorf("dumpLength = %d, want 23040000", got)
	}
	// Cut to the room, and to whole frames.
	if got := dumpLength(10, 96000, 24, 1000); got != 984 {
		t.Errorf("dumpLength in 1000 bytes = %d, want 984", got)
	}
	if got := dumpRoom(10<<20, 64<<20); got != 0 {
		t.Errorf("dumpRoom with less free than the reserve = %d, want 0", got)
	}
}

func TestDumpFolderIsPrivate(t *testing.T) {
	old := rawDumpDir
	t.Cleanup(func() { rawDumpDir = old })
	rawDumpDir = filepath.Join(t.TempDir(), "micdump")
	if err := os.Mkdir(rawDumpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(rawDumpDir, "rawdump-1.s24.part")
	if err := os.WriteFile(stale, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := prepareDumpDir(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(rawDumpDir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("folder mode %o, want 700", fi.Mode().Perm())
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale .part still there: %v", err)
	}

	path := filepath.Join(rawDumpDir, "rawdump-2.s24")
	f, err := createDump(path)
	if err != nil {
		t.Fatal(err)
	}
	finishDump(f, path)
	fi, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode %o, want 600", fi.Mode().Perm())
	}
	if f, err := createDump(path); err == nil {
		_ = f.Close()
		if _, err := createDump(path); err == nil {
			t.Error("a second create of the same .part succeeded")
		}
	}
}

func TestDumpFolderRefusesALink(t *testing.T) {
	old := rawDumpDir
	t.Cleanup(func() { rawDumpDir = old })
	dir := t.TempDir()
	rawDumpDir = filepath.Join(dir, "micdump")
	if err := os.Symlink(t.TempDir(), rawDumpDir); err != nil {
		t.Skip(err)
	}
	if _, err := prepareDumpDir(); err == nil {
		t.Error("prepareDumpDir took a link")
	}
}
