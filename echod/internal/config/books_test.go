package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A state.json from when the device kept its own places in audiobooks still loads, the places in it
// read as they were, though nothing uses them any more.
func TestOldBookPlacesStillLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	old := `{"book_places":{"list":[{"uri":"library://audiobook/1","seconds":1200,"at":1700000120}]}}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Load(path)
	if err != nil {
		t.Fatalf("a state with book places did not load: %v", err)
	}
	if got := st.Get().BookPlaces.List; len(got) != 1 || got[0].URI != "library://audiobook/1" || got[0].Seconds != 1200 {
		t.Errorf("places = %+v", got)
	}
}
