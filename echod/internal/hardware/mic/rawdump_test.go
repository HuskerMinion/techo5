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

// useSpots points the dumps at folders under a temporary directory for the test.
func useSpots(t *testing.T, spots ...dumpSpot) {
	old, oldMem := dumpSpots, memAvailable
	t.Cleanup(func() { dumpSpots, memAvailable = old, oldMem })
	dumpSpots = spots
}

func TestDumpFolderIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "micdump")
	useSpots(t, dumpSpot{dir: dir})
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "rawdump-1.s24.part")
	if err := os.WriteFile(stale, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	got, _, err := prepareDumpDir(0)
	if err != nil {
		t.Fatal(err)
	}
	// A dump running beside the next one keeps its .part.
	running := filepath.Join(dir, "rawnative-1.s32.part")
	if err := os.WriteFile(running, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareDumpDir(0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(running); err != nil {
		t.Errorf("a second dump took the running one's .part: %v", err)
	}
	if got != dir {
		t.Fatalf("folder %s, want %s", got, dir)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("folder mode %o, want 700", fi.Mode().Perm())
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale .part still there: %v", err)
	}

	path := filepath.Join(dir, "rawdump-2.s24")
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
	dir := filepath.Join(t.TempDir(), "micdump")
	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Skip(err)
	}
	useSpots(t, dumpSpot{dir: dir})
	if _, _, err := prepareDumpDir(0); err == nil {
		t.Error("prepareDumpDir took a link")
	}
}

// Flash too full for the dump (the Dot 5's /data): it goes to the folder in memory, cut to the
// memory available less the reserve.
func TestDumpFallsBackToMemory(t *testing.T) {
	base := t.TempDir()
	flash, ram := filepath.Join(base, "flash"), filepath.Join(base, "ram")
	useSpots(t, dumpSpot{dir: flash}, dumpSpot{dir: ram, ram: true})
	memAvailable = func() (int64, error) { return ramReserve + 1000, nil }

	// More than any test machine has free on its disk.
	dir, room, err := prepareDumpDir(1 << 60)
	if err != nil {
		t.Fatal(err)
	}
	if dir != flash {
		// The disk always has more room than the 1000 bytes the memory leaves.
		t.Errorf("folder %s, want the one with the most room, %s", dir, flash)
	}
	dir, room, err = prepareDumpDir(1)
	if err != nil || dir != flash || room <= 0 {
		t.Errorf("a small dump: %s %d %v, want flash", dir, room, err)
	}

	// Flash unusable: memory, cut to the reserve.
	useSpots(t, dumpSpot{dir: filepath.Join(base, "missing", "flash")}, dumpSpot{dir: ram, ram: true})
	memAvailable = func() (int64, error) { return ramReserve + 1000, nil }
	dir, room, err = prepareDumpDir(5000)
	if err != nil {
		t.Fatal(err)
	}
	if dir != ram || room != 1000 {
		t.Errorf("folder %s with %d bytes, want %s with 1000", dir, room, ram)
	}
}
