package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A device saved when ca held the Valencian catalog wakes up as ca-valencia, once: the flag says the
// file has been seen, so a ca chosen after the move is the Central Catalan it now means and stays.
// A device that never chose ca is left alone, and a new device is born with the look already done.
func TestLanguageMovesOnce(t *testing.T) {
	dir := t.TempDir()

	old := filepath.Join(dir, "valencian.json")
	if err := os.WriteFile(old, []byte(`{"screen":{"language":"ca"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Load(old)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Get().Screen; got.Language != "ca-valencia" || !got.LanguageMoved || st.announceLang != "Català" {
		t.Errorf("a saved ca came back as %+v", got)
	}
	Use(old)
	if LanguageAnnounce() != "Català" {
		t.Error("the start-up cannot tell what the move added to the menu")
	}

	// The move goes to disk with the next change, and after it a chosen ca means what it now says.
	if err := st.Update(func(c *Config) { c.Screen.Language = "ca" }); err != nil {
		t.Fatal(err)
	}
	after, err := Load(old)
	if err != nil {
		t.Fatal(err)
	}
	if got := after.Get().Screen; got.Language != "ca" || !got.LanguageMoved || after.announceLang != "" {
		t.Errorf("a ca chosen after the move came back as %+v", got)
	}

	// Somebody who never chose ca keeps exactly what they had.
	other := filepath.Join(dir, "other.json")
	if err := os.WriteFile(other, []byte(`{"screen":{"language":"de"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err = Load(other)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Get().Screen; got.Language != "de" || !got.LanguageMoved || st.announceLang != "" {
		t.Errorf("a German device was moved: %+v", got)
	}

	fresh, err := Load(filepath.Join(dir, "none.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !fresh.Get().Screen.LanguageMoved {
		t.Error("a new device has a move still to do")
	}
}
