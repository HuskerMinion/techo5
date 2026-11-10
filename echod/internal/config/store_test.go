package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSTTWakeTotalsSurviveReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := st.Set().OnHA()
	for _, verdict := range [][2]bool{{true, false}, {false, true}, {false, false}} {
		if _, err := writer.RecordSTTWake(verdict[0], verdict[1]); err != nil {
			t.Fatal(err)
		}
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := reloaded.Get().Wake.OnHA.STTWakes, (STTWakeCounts{Sent: 3, Allowed: 1, Blocked: 1}); got != want {
		t.Fatalf("saved wake totals = %+v, want %+v", got, want)
	}
}

// A checked change is written before it reports success, and comes back after a load: an occurrence
// somebody was told is left out must not live only in memory.
func TestACheckedChangeIsSavedBeforeItIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 19, 7, 0, 0, 0, time.UTC)
	if err := st.UpdateChecked(func(c *Config) error {
		c.Alarms.Skipped = []Skip{{ID: "a", At: at}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := st.Get().Alarms.Skipped; len(got) != 1 || got[0].ID != "a" || !got[0].At.Equal(at) {
		t.Fatalf("saved change reads %+v", got)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Get().Alarms.Skipped; len(got) != 1 || got[0].ID != "a" || !got[0].At.Equal(at) {
		t.Fatalf("the saved change did not come back: %+v", got)
	}
}

// A checked change the caller refuses leaves memory and disk as they were, even if it changed them
// before refusing.
func TestACheckedChangeThatIsRefusedIsNotWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 19, 7, 0, 0, 0, time.UTC)
	if err := st.UpdateChecked(func(c *Config) error {
		c.Alarms.Skipped = []Skip{{ID: "a", At: at}}
		return errors.New("not now")
	}); err == nil {
		t.Fatal("a refused change said it saved")
	}
	if got := st.Get().Alarms.Skipped; len(got) != 0 {
		t.Fatalf("a refused change held: %+v", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a refused change wrote the file: %v", err)
	}
}

// A checked change that could not be written is taken back, and the next successful change does not
// carry it to disk behind everyone's back. Unlike UpdateOrUndo there is no exception for a file that
// was never readable: a change reported as saved has to be on disk.
func TestACheckedChangeThatCouldNotBeSavedIsTakenBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	unblock := failWrites(t, path)
	at := time.Date(2026, 10, 19, 7, 0, 0, 0, time.UTC)
	if err := st.UpdateChecked(func(c *Config) error {
		c.Alarms.Skipped = []Skip{{ID: "a", At: at}}
		return nil
	}); err == nil {
		t.Fatal("a write that could not happen said it saved")
	}
	if got := st.Get().Alarms.Skipped; len(got) != 0 {
		t.Fatalf("a change that failed to save held: %+v", got)
	}
	unblock()
	if err := st.Set().NoneChosen(true); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Get().Alarms.Skipped; len(got) != 0 {
		t.Fatalf("an unrelated save wrote the change that had failed: %+v", got)
	}

	// And the same on a file that was never readable: nothing holds in memory, and the file is left
	// exactly as it was.
	const original = "{not json"
	bad := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(bad, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	unreadable, err := Load(bad)
	if err == nil {
		t.Fatal("an unreadable file loaded")
	}
	if err := unreadable.UpdateChecked(func(c *Config) error {
		c.Alarms.Skipped = []Skip{{ID: "a", At: at}}
		return nil
	}); err == nil {
		t.Fatal("a change to a file that was never readable said it saved")
	}
	if got := unreadable.Get().Alarms.Skipped; len(got) != 0 {
		t.Fatalf("a change that could not be saved held in memory: %+v", got)
	}
	if b, readErr := os.ReadFile(bad); readErr != nil || string(b) != original {
		t.Errorf("the unreadable file was touched: %q %v", b, readErr)
	}
}

// A checked change whose new file is in place but whose folder could not be flushed after is on
// disk: memory keeps it to match, rather than reporting failure and taking back something the file
// already has. This is afterRename, as UpdateOrUndo has always treated it: a power cut immediately
// after could still lose the rename, but the running device does not disagree with its own file.
func TestACheckedChangeOnDiskIsNotTakenBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }()
	if d, err := os.Open(dir); err == nil {
		_ = d.Close()
		t.Skip("the folder opens anyway (running as root?)")
	}
	at := time.Date(2026, 10, 19, 7, 0, 0, 0, time.UTC)
	if err := st.UpdateChecked(func(c *Config) error {
		c.Alarms.Skipped = []Skip{{ID: "a", At: at}}
		return nil
	}); err != nil {
		t.Fatalf("a change that reached the disk was reported failed: %v", err)
	}
	if got := st.Get().Alarms.Skipped; len(got) != 1 {
		t.Fatalf("a change already on disk was taken back: %+v", got)
	}
	_ = os.Chmod(dir, 0o700)
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Get().Alarms.Skipped; len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("the change was not on disk: %+v", got)
	}
}
