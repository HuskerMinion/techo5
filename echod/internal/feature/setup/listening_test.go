package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// A model chosen as the volume down word counts as listened for, so the page will neither remove it nor
// replace it under the engine that has it loaded.
func TestTheVolumeDownWordCountsAsListening(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if listening("turn_it_down") {
		t.Fatal("listening before it was chosen")
	}
	if err := config.Set().VolumeDown().ID("turn_it_down"); err != nil {
		t.Fatal(err)
	}
	if !listening("turn_it_down") {
		t.Error("the volume down word's model does not count as listening")
	}
	if listening("hey_jarvis") {
		t.Error("another model counts as listening")
	}
}

// The stop word counts as listened for while it is in the stop word's slot, and not once it failed and
// the built-in word took its place: then it can be removed or sent again.
func TestTheStopWordCountsAsListeningOnlyWhileLoaded(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Screen().Language("de"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, body := range map[string]string{
		"stopp.tflite": "model",
		"stopp.json":   `{"type":"micro","wake_word":"Stopp","trained_languages":["de"],"stop_word":true,"micro":{}}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lib := wake.NewLibrary(dir)
	wasModels, wasLoaded := models, stopWordLoaded
	t.Cleanup(func() { models, stopWordLoaded = wasModels, wasLoaded })
	models = func() *wake.Library { return lib }

	loaded := "stopp"
	stopWordLoaded = func() string { return loaded }
	if !listening("stopp") {
		t.Error("the stop word in its slot does not count as listening")
	}
	loaded = ""
	if listening("stopp") {
		t.Error("a stop word that failed still counts as listening")
	}
}
