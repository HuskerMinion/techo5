package detect

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/detect/assets"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

func stopModelFor(id string, langs ...string) wake.Model {
	m := model(id, wake.KindMicroWakeWord)
	m.Languages, m.StopWord = langs, true
	return m
}

// The stop word listens with the library's stop word for the device's language, and with the built-in
// one without one, for another language, and when it has since been chosen as a wake word: the device
// can always be interrupted.
func TestStopWordModel(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	models := []wake.Model{model("okay_nabu", wake.KindMicroWakeWord)}
	var wakeWords []string
	lang := "de"
	s := &stopWord{
		models:    func() []wake.Model { return models },
		wakeWords: func() []string { return wakeWords },
		lang:      func() string { return lang },
		builtin:   func() (wake.Model, error) { return wake.Model{ID: stopID, Phrase: assets.StopPhrase}, nil },
	}
	uses := func() string {
		t.Helper()
		m, err := s.model()
		if err != nil {
			t.Fatal(err)
		}
		if m.ID != s.id() {
			t.Errorf("model is %q, id says %q", m.ID, s.id())
		}
		return m.ID
	}

	if got := uses(); got != stopID {
		t.Errorf("with no stop word in the library, uses %q", got)
	}
	models = append(models, stopModelFor("stopp", "de"))
	if got := uses(); got != "stopp" {
		t.Errorf("with a German stop word on a German device, uses %q, want stopp", got)
	}
	lang = "fr"
	if got := uses(); got != stopID {
		t.Errorf("on a French device, uses %q, want the built-in word", got)
	}
	lang = "de"
	wakeWords = []string{"stopp"}
	if got := uses(); got != stopID {
		t.Errorf("a wake word now, uses %q, want the built-in word", got)
	}
}

// What is loaded is loaded again only when something else would be, and not at all with the word off.
func TestStopWordChanged(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Stop().Threshold(0.7); err != nil {
		t.Fatal(err)
	}
	var models []wake.Model
	s := &stopWord{
		models:    func() []wake.Model { return models },
		wakeWords: func() []string { return nil },
		lang:      func() string { return "de" },
		loaded:    func() string { return stopID },
	}
	if s.changed() {
		t.Error("nothing changed, and it would load again")
	}
	models = []wake.Model{stopModelFor("stopp", "de")}
	if !s.changed() {
		t.Error("a German stop word added, and it would not load it")
	}
	if err := config.Set().Stop().Threshold(config.StopOff); err != nil {
		t.Fatal(err)
	}
	if s.changed() {
		t.Error("off, and it would load")
	}
}

// refusing is a fake engine that will not load one model.
type refusing struct {
	fake
	refuse string
}

func (r *refusing) load(m wake.Model) error {
	if m.ID == r.refuse {
		return errors.New("unsupported MUL")
	}
	return r.fake.load(m)
}

// stopDetect is a Detect with only what the stop word needs: an engine of fakes that refuses refuse, and a
// German stop word "stopp" in the library on a German device.
func stopDetect(t *testing.T, refuse string) *Detect {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Stop().Threshold(0.7); err != nil {
		t.Fatal(err)
	}
	was := build
	build = func(wake.Kind) (backend, error) { return &refusing{fake{loaded: map[string]float64{}}, refuse}, nil }
	t.Cleanup(func() { build = was })

	d := &Detect{engine: New(StopSlot+1, nil), wakeProblem: newWakeProblemEntity()}
	d.setWakeProblem(nil)
	path := filepath.Join(t.TempDir(), "stopp.tflite")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	stopp := stopModelFor("stopp", "de")
	stopp.Path = path
	d.stopWord = &stopWord{
		models:    func() []wake.Model { return []wake.Model{stopp} },
		wakeWords: func() []string { return nil },
		lang:      func() string { return "de" },
		builtin:   func() (wake.Model, error) { return model(stopID, wake.KindMicroWakeWord), nil },
		loaded: func() string {
			if m, ok := d.engine.Loaded(StopSlot); ok {
				return m.ID
			}
			return ""
		},
	}
	return d
}

func inStopSlot(d *Detect) string {
	m, _ := d.engine.Loaded(StopSlot)
	return m.ID
}

// A stop word that will not load leaves the built-in one in the slot, says so, and is not tried again.
func TestAStopWordThatWillNotLoadFallsBack(t *testing.T) {
	d := stopDetect(t, "stopp")
	d.loadStop()
	if got := inStopSlot(d); got != stopID {
		t.Fatalf("in the stop word's slot: %q, want the built-in word", got)
	}
	if got := d.wakeProblem.Get(); !strings.Contains(got, "Stop word (stopp)") {
		t.Errorf("problem says %q", got)
	}
	if d.stopWord.changed() {
		t.Error("the model that would not load would be tried again")
	}
}

// A stop word that fails on live audio gives the slot to the built-in one, and a change elsewhere does not
// load it again.
func TestAStopWordDroppedFallsBack(t *testing.T) {
	d := stopDetect(t, "")
	d.loadStop()
	if got := inStopSlot(d); got != "stopp" {
		t.Fatalf("in the stop word's slot: %q, want stopp", got)
	}
	d.engine.Clear(StopSlot) // as dropFailed empties it
	d.dropped(StopSlot, "stopp")
	if got := inStopSlot(d); got != stopID {
		t.Fatalf("after the drop: %q, want the built-in word", got)
	}
	d.reloadStop()
	if got := inStopSlot(d); got != stopID {
		t.Errorf("a later change loaded %q again", got)
	}
	if got := d.wakeProblem.Get(); !strings.Contains(got, "failed on live audio") {
		t.Errorf("problem says %q", got)
	}
}

// Through a selection from Home Assistant, which is loaded before it is saved: the stop word chosen as a
// wake word gives the stop word's slot to the built-in one at once, and taken off again is the stop word
// again, with no restart.
func TestAStopWordChosenAsAWakeWordLeavesTheStopSlot(t *testing.T) {
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
	fakes(t)
	busy, _ := fixture(t, warm{0: true})
	d := &Detect{engine: New(StopSlot+1, nil), busy: busy, lib: wake.NewLibrary(dir)}
	d.stopWord = &stopWord{
		models:    func() []wake.Model { return d.library().Ours() },
		wakeWords: d.wakeWordsLoaded,
		lang:      func() string { return config.Get().Screen.Language },
		builtin:   func() (wake.Model, error) { return model(stopID, wake.KindMicroWakeWord), nil },
		loaded: func() string {
			if m, ok := d.engine.Loaded(StopSlot); ok {
				return m.ID
			}
			return ""
		},
	}
	d.loadStop()
	if got := inStopSlot(d); got != "stopp" {
		t.Fatalf("in the stop word's slot: %q, want stopp", got)
	}

	// Loaded, and not yet saved: config.Wake still has no wake word.
	d.load([]string{"stopp"})
	if got := inStopSlot(d); got != stopID {
		t.Errorf("stopp a wake word, the stop word's slot has %q, want the built-in word", got)
	}
	d.load(nil)
	if got := inStopSlot(d); got != "stopp" {
		t.Errorf("stopp no longer a wake word, the stop word's slot has %q, want stopp", got)
	}
}
