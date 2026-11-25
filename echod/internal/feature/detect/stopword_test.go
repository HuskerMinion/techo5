package detect

import (
	"path/filepath"
	"slices"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/detect/assets"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// stopWordFor is a stop word select over models and wake words the test sets, with the built-in model
// standing in for the embedded one, which the test machine has no state folder to write.
func stopWordFor(t *testing.T, models *[]wake.Model, wakeWords *[]string) (*Detect, *Engine) {
	t.Helper()
	fakes(t)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	was := builtInStop
	builtInStop = func() (wake.Model, error) {
		return wake.Model{ID: "stop", Phrase: assets.StopPhrase, Kind: wake.KindMicroWakeWord, Path: "builtin/stop.tflite"}, nil
	}
	t.Cleanup(func() { builtInStop = was })
	e := New(StopSlot+1, nil)
	d := &Detect{engine: e}
	d.stopWord = &stopWord{
		d:         d,
		word:      &esphome.Select{},
		models:    func() []wake.Model { return *models },
		wakeWords: func() []string { return *wakeWords },
	}
	return d, e
}

func stopLoaded(e *Engine) wake.Model {
	m, _ := e.Loaded(StopSlot)
	return m
}

// The built-in "Stop" comes first and is saved as nothing, the default; after it the device's models,
// but for the wake words chosen now and one of the user's called "Stop". A model the device no longer
// has, or one chosen as a wake word since, shows as the built-in word, which is what listens then.
func TestStopWordOptions(t *testing.T) {
	models := []wake.Model{model("stopp", wake.KindMicroWakeWord), model("okay_nabu", wake.KindMicroWakeWord),
		{ID: "my_stop", Phrase: assets.StopPhrase, Kind: wake.KindMicroWakeWord}}
	wakeWords := []string{"okay_nabu"}
	d, _ := stopWordFor(t, &models, &wakeWords)
	s := d.stopWord

	if got, want := s.options(), []string{assets.StopPhrase, "stopp"}; !slices.Equal(got, want) {
		t.Errorf("options %v, want %v", got, want)
	}
	for _, c := range []struct{ phrase, id string }{{assets.StopPhrase, ""}, {"stopp", "stopp"}} {
		if id, ok := s.idFor(c.phrase); !ok || id != c.id {
			t.Errorf("idFor(%q) = %q, %v; want %q", c.phrase, id, ok, c.id)
		}
		if got := s.phraseFor(c.id); got != c.phrase {
			t.Errorf("phraseFor(%q) = %q, want %q", c.id, got, c.phrase)
		}
	}
	if _, ok := s.idFor("nothing like it"); ok {
		t.Error("an unknown option was taken")
	}
	if got := s.phraseFor("gone"); got != assets.StopPhrase {
		t.Errorf("a model the device no longer has shows as %q", got)
	}
	if got := s.phraseFor("okay_nabu"); got != assets.StopPhrase {
		t.Errorf("a wake word shows as %q", got)
	}
}

// Nothing chosen, the built-in word listens, as it always has. A model of one's own chosen takes the
// slot in its place, at the same threshold; deleted, or chosen as a wake word, the built-in word comes
// back, and the choice is kept for when the model does. Off is off whichever word is chosen.
func TestStopWordLoadsWhatIsChosen(t *testing.T) {
	models := []wake.Model{model("stopp", wake.KindMicroWakeWord)}
	var wakeWords []string
	d, e := stopWordFor(t, &models, &wakeWords)

	d.loadStop()
	if got := stopLoaded(e); got.Path != "builtin/stop.tflite" {
		t.Fatalf("by default, loaded %+v, want the built-in word", got)
	}

	if err := config.Set().Stop().ID("stopp"); err != nil {
		t.Fatal(err)
	}
	d.loadStop()
	if got := stopLoaded(e); got.ID != "stopp" {
		t.Fatalf("chosen, loaded %q, want stopp", got.ID)
	}
	// The threshold goes by the slot, so it is the stop word's whichever model is in it.
	if got := d.threshold(StopSlot); got != config.DefaultStopThreshold {
		t.Errorf("chosen, the threshold is %v, want the stop word's %v", got, config.DefaultStopThreshold)
	}

	models = nil
	d.stopWord.refreshOptions()
	d.followStop()
	if got := stopLoaded(e); got.Path != "builtin/stop.tflite" {
		t.Fatalf("its model deleted, loaded %+v, want the built-in word", got)
	}
	if got := d.stopWord.word.Get(); got != assets.StopPhrase {
		t.Errorf("its model deleted, the select shows %q", got)
	}
	if config.Get().Wake.Stop.ID != "stopp" {
		t.Error("the choice was forgotten")
	}

	models = []wake.Model{model("stopp", wake.KindMicroWakeWord)}
	d.followStop()
	if got := stopLoaded(e); got.ID != "stopp" {
		t.Fatalf("the model back, loaded %q, want stopp", got.ID)
	}

	wakeWords = []string{"stopp"}
	d.followStop()
	if got := stopLoaded(e); got.Path != "builtin/stop.tflite" {
		t.Fatalf("a wake word now, loaded %+v, want the built-in word", got)
	}
	wakeWords = nil

	if err := config.Set().Stop().Threshold(config.StopOff); err != nil {
		t.Fatal(err)
	}
	d.loadStop()
	if _, ok := e.Loaded(StopSlot); ok {
		t.Error("off, the stop slot still holds a word")
	}
}

// A model of one's own that has the built-in word's id is still put in the slot: the engine would
// otherwise keep the built-in file, taking the two for the same model.
func TestStopWordOfTheSameID(t *testing.T) {
	models := []wake.Model{{ID: "stop", Phrase: "Stopp", Kind: wake.KindMicroWakeWord, Path: "own/stop.tflite"}}
	var wakeWords []string
	d, e := stopWordFor(t, &models, &wakeWords)

	d.loadStop()
	if err := config.Set().Stop().ID("stop"); err != nil {
		t.Fatal(err)
	}
	d.loadStop()
	if got := stopLoaded(e); got.Path != "own/stop.tflite" {
		t.Errorf("loaded %q, want the model of one's own", got.Path)
	}
}

// The select saves what is chosen, and the built-in word is saved as nothing, so a device that goes
// back to it is the same as one that never left.
func TestStopWordSelectSaves(t *testing.T) {
	models := []wake.Model{model("stopp", wake.KindMicroWakeWord)}
	var wakeWords []string
	d, e := stopWordFor(t, &models, &wakeWords)
	s := d.stopWord

	s.choose("stopp")
	if got := config.Get().Wake.Stop.ID; got != "stopp" {
		t.Fatalf("saved %q", got)
	}
	if got := stopLoaded(e); got.ID != "stopp" {
		t.Fatalf("loaded %q", got.ID)
	}
	if got := s.word.Get(); got != "stopp" {
		t.Errorf("the select shows %q", got)
	}
	s.choose("nothing like it")
	if got := config.Get().Wake.Stop.ID; got != "stopp" {
		t.Errorf("an unknown option changed the choice to %q", got)
	}
	s.choose(assets.StopPhrase)
	if got := config.Get().Wake.Stop.ID; got != "" {
		t.Errorf("the built-in word saved as %q, want nothing", got)
	}
}

// A word of one's own chosen as the second wake word leaves the stop word and the hang-up word's list
// once the selection is saved. The load runs before the save, so it still sees the wake words before,
// and only the look after the save (selected) can move the stop word off it (#741).
func TestAWakeWordChosenLaterLeavesTheStopWord(t *testing.T) {
	models := []wake.Model{model("okay_nabu", wake.KindMicroWakeWord), model("stopp", wake.KindMicroWakeWord)}
	wakeWords := []string{"okay_nabu"}
	d, e := stopWordFor(t, &models, &wakeWords)
	d.busy, _ = fixture(t, warm{0: true, 1: true})
	d.hangup = &hangup{
		d:         d,
		word:      &esphome.Select{},
		models:    func() []wake.Model { return models },
		wakeWords: func() []string { return wakeWords },
	}
	if err := config.Set().Stop().ID("stopp"); err != nil {
		t.Fatal(err)
	}
	d.loadStop()
	if got := stopLoaded(e); got.ID != "stopp" {
		t.Fatalf("chosen, loaded %q, want stopp", got.ID)
	}

	// The load, with the saved wake words still the old ones, as OnWakeWord has them then.
	resolve := func([]string) []wake.Model { return models }
	if got := d.loadFrom([]string{"okay_nabu", "stopp"}, resolve); len(got) != 2 {
		t.Fatalf("loaded %v, want both", got)
	}
	if got := stopLoaded(e); got.ID != "stopp" {
		t.Fatalf("before the save, loaded %q; the test no longer shows the order it is about", got.ID)
	}

	// Then the save, and selected.
	wakeWords = []string{"okay_nabu", "stopp"}
	d.selected()
	if got := stopLoaded(e); got.Path != "builtin/stop.tflite" {
		t.Errorf("a wake word now, the stop slot holds %+v, want the built-in word", got)
	}
	if got := d.stopWord.word.Get(); got != assets.StopPhrase {
		t.Errorf("a wake word now, the stop word select shows %q", got)
	}
	if slices.Contains(d.stopWord.word.Options, "stopp") {
		t.Errorf("a wake word now, still offered as the stop word: %v", d.stopWord.word.Options)
	}
	if slices.Contains(d.hangup.word.Options, "stopp") {
		t.Errorf("a wake word now, still offered as the hang-up word: %v", d.hangup.word.Options)
	}
}
