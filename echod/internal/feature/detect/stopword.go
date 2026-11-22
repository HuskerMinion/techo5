package detect

import (
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// stopWord is which model the stop word listens with. The built-in one is trained on English "stop", and
// a German "Stopp" hardly reaches its threshold, so a model in the library marked as a stop word for the
// device's language is heard in its place (wake.StopWordFor). There is nothing to choose: one that fits
// is used, and without one the built-in word is.
//
// Its job is that a ringing alarm can always be stopped by voice, so a library model that will not load,
// or fails on live audio, gives the slot to the built-in word and is not tried again until its file
// changes.
type stopWord struct {
	// models is what the device has, the same library the wake words come from, wakeWords the ids
	// loaded as wake words, lang the device's language and builtin the built-in word. Tests stand in
	// for all of them.
	models    func() []wake.Model
	wakeWords func() []string
	lang      func() string
	builtin   func() (wake.Model, error)

	// loaded is the id in the stop word's slot now, empty with none, so a change elsewhere loads it again
	// only when it would load something else.
	loaded func() string

	// load is held through a load of the slot, from Start, the threshold, the library, the language, the
	// wake words and a failure, so two close together cannot leave the slot with the one decided first.
	load sync.Mutex

	// failed is the library models that would not load or failed on the audio, by id, with the time
	// their file was changed then: the same file is left out, one sent again is tried.
	mu     sync.Mutex
	failed map[string]time.Time
}

func newStopWord(d *Detect) *stopWord {
	s := &stopWord{
		models:    func() []wake.Model { return d.library().Ours() },
		wakeWords: d.wakeWordsLoaded,
		lang:      func() string { return config.Get().Screen.Language },
		builtin:   stopModel,
		loaded: func() string {
			if m, ok := d.engine.Loaded(StopSlot); ok {
				return m.ID
			}
			return ""
		},
	}
	// A stop word added or removed, or another language, may change what is heard. Hooks' listeners
	// must not block, and a load reads a model off disk.
	wake.Lib().Changed.Listen(func(struct{}) { go d.reloadStop() })
	config.LanguageChanged.Listen(func(string) { go d.reloadStop() })
	return s
}

// model is the model the stop word listens with: the library's stop word for the device's language,
// unless there is none, and then the built-in word, so the device can always be interrupted.
func (s *stopWord) model() (wake.Model, error) {
	if m, ok := s.fitting(); ok {
		return m, nil
	}
	return s.builtin()
}

// id is what model would load, by id, without reading the built-in one off disk.
func (s *stopWord) id() string {
	if m, ok := s.fitting(); ok {
		return m.ID
	}
	return stopID
}

// fitting is the library's stop word for the device's language, leaving out one that failed.
func (s *stopWord) fitting() (wake.Model, bool) {
	var models []wake.Model
	for _, m := range s.models() {
		if !s.hasFailed(m) {
			models = append(models, m)
		}
	}
	return wake.StopWordFor(models, s.lang(), s.wakeWords())
}

// changed is whether the stop word is listening with something other than what it would listen with
// now. Off, nothing changes: the threshold turns it back on, and loads it then.
func (s *stopWord) changed() bool {
	return config.Get().Wake.Stop.Listening() && s.loaded() != s.id()
}

// fail remembers a library model that would not load or failed on the audio.
func (s *stopWord) fail(m wake.Model) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed == nil {
		s.failed = map[string]time.Time{}
	}
	s.failed[m.ID] = modTime(m.Path)
}

func (s *stopWord) hasFailed(m wake.Model) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.failed[m.ID]
	return ok && at.Equal(modTime(m.Path))
}

func modTime(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// wakeWordsLoaded are the ids in the wake word slots now. Not the saved selection: a selection from Home
// Assistant is loaded (loadFrom) before it is saved, and the stop word is decided as part of that load.
func (d *Detect) wakeWordsLoaded() []string {
	var ids []string
	for slot := range wakeword.Slots {
		if m, ok := d.engine.Loaded(slot); ok {
			ids = append(ids, m.ID)
		}
	}
	return ids
}

// reloadStop loads the stop word again if it would now load something else.
func (d *Detect) reloadStop() {
	if d.stopWord == nil {
		return
	}
	d.stopWord.load.Lock()
	defer d.stopWord.load.Unlock()
	if d.stopWord.changed() {
		d.loadStopLocked()
	}
}

// stopDropped is the engine having taken the stop word's model out for failing on the audio
// (Engine.OnDropped). A library model is left out from then on and the built-in word put in its place;
// the built-in one is put back after a while, as a wake word is.
func (d *Detect) stopDropped(id string) {
	s := d.stopWord
	if id != stopID {
		if m, ok := wake.Find(s.models(), id); ok {
			s.fail(m)
		}
		slog.Error("the stop word's model failed on live audio; listening for the built-in one", "id", id)
		d.addWakeProblem(fmt.Sprintf("Stop word (%s): its model failed on live audio; listening for the built-in Stop", id))
		d.reloadStop()
		return
	}
	slog.Error("the built-in stop word failed on live audio; putting it back")
	d.addWakeProblem("Stop word: the built-in model failed on live audio; putting it back")
	later(reloadAfter, func() { go d.reloadStop() })
}

// StopModelID is the library model in the stop word's slot now, empty for the built-in one or none: one
// that failed is not in it, and so is not kept from being removed or sent again.
func (d *Detect) StopModelID() string {
	if m, ok := d.engine.Loaded(StopSlot); ok && m.ID != stopID {
		return m.ID
	}
	return ""
}
