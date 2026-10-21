package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// On an existing unit the missing new field must keep original mono;
// on an opted-in unit it must survive a new daemon's Load.
func TestAthenaStereoSettingPersistsAndDefaultsToMono(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state.json")
	st, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if st.Get().Speaker.Stereo {
		t.Fatal("new install must start mono")
	}
	if err = st.Set().Speaker().Stereo(true); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "\"stereo\": true") &&
		!strings.Contains(string(contents), "\"stereo\":true") {
		t.Fatalf("saved config does not contain stereo opt-in: %s", contents)
	}
	second, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Get().Speaker.Stereo {
		t.Fatal("opt-in did not survive reload")
	}
	if err = second.Set().Speaker().Stereo(false); err != nil {
		t.Fatal(err)
	}
	third, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if third.Get().Speaker.Stereo {
		t.Fatal("mono-off mode did not persist")
	}
}
