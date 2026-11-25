package detect

import (
	"log/slog"
	"slices"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/detect/assets"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// stopWord is the stop word's select (#741): which model listens in StopSlot. The built-in "Stop" is
// the default and stays one of the options, so a device nobody touches listens as it always has. Any
// other option is one of the device's own models, from the same library as the wake words, the hang-up
// word and the command words, such as a "Stopp" trained on German voices: the built-in model is the
// English one, and a German "Stopp" said over a ring is not what it was trained on.
//
// Only the word changes. The threshold, the allowance over a reply or a ring, the slot and everything
// a detection there does (route.go) are the stop word's whichever model it is.
type stopWord struct {
	d    *Detect
	word *esphome.Select

	// models is what the device has, and wakeWords the ids chosen as wake words. Tests stand in for both.
	models    func() []wake.Model
	wakeWords func() []string
}

func newStopWord(d *Detect) *stopWord {
	s := &stopWord{d: d, models: wake.Lib().Ours, wakeWords: chosenWakeWords}
	s.word = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "stop_word",
			DeviceID: component.DeviceMicrophone,
			Name:     "Stop word",
			Icon:     "mdi:hand-back-left",
			Category: esphome.CategoryConfig,
		},
		Options: s.options(),
	}
	s.word.OnCommand = s.choose
	// A word added or removed changes what is offered, and a chosen one removed has the built-in word
	// listen in its place. Changed's listeners must not block, and a load reads a model off disk.
	wake.Lib().Changed.Listen(func(struct{}) {
		go func() {
			s.refreshOptions()
			d.followStop()
		}()
	})
	return s
}

// choose is an option chosen in Home Assistant: saved, shown, and listened for from now.
func (s *stopWord) choose(v string) {
	id, ok := s.idFor(v)
	if !ok {
		return
	}
	if err := config.Set().Stop().ID(id); err != nil {
		slog.Error("saving the stop word failed", "err", err)
		return
	}
	s.word.Set(v)
	slog.Info("setting changed", "setting", s.word.ObjectID, "using", v)
	s.d.loadStop()
}

// options are the built-in stop word, then the models the device has, by phrase. Left out, as from the
// hang-up word's list: one of the user's called "Stop", which could not be told apart from the built-in
// one in the list, and the wake words chosen now, which as the stop word too would stop the ring and
// open a turn at once.
func (s *stopWord) options() []string {
	out := []string{assets.StopPhrase}
	for _, m := range s.models() {
		if m.Phrase != assets.StopPhrase && !s.isWakeWord(m.ID) {
			out = append(out, m.Phrase)
		}
	}
	return out
}

func (s *stopWord) isWakeWord(id string) bool { return slices.Contains(s.wakeWords(), id) }

// idFor is what a select option is saved as, and whether it names anything: empty for the built-in
// word, a model's id for one of the library's.
func (s *stopWord) idFor(phrase string) (string, bool) {
	if phrase == assets.StopPhrase {
		return "", true
	}
	for _, m := range s.models() {
		if m.Phrase == phrase {
			return m.ID, true
		}
	}
	return "", false
}

// phraseFor is the select option for what was saved, as the device then listens: a model it no longer
// has, or one chosen since as a wake word, shows as the built-in "Stop", which is what listens in its
// place (ownModel). The saved choice is kept, so the word comes back if the model does.
func (s *stopWord) phraseFor(id string) string {
	if m, ok := s.ownModel(id); ok {
		return m.Phrase
	}
	return assets.StopPhrase
}

// ownModel is the library's model saved as id, when it can listen as the stop word: one the device has
// and not a wake word now. Empty is the built-in word, and so is anything else.
func (s *stopWord) ownModel(id string) (wake.Model, bool) {
	if id == "" || s.isWakeWord(id) {
		return wake.Model{}, false
	}
	return wake.Find(s.models(), id)
}

// wanted is the model the stop slot should hold. A choice that cannot listen says why in the log, once
// for each load, and the built-in word is listened for in its place: a device whose stop word went
// with a deleted model would otherwise have no way to be stopped by voice at all.
func (s *stopWord) wanted() (wake.Model, error) {
	id := config.Get().Wake.Stop.ID
	if m, ok := s.ownModel(id); ok {
		return m, nil
	}
	switch {
	case id == "":
	case s.isWakeWord(id):
		slog.Warn("the stop word chosen is a wake word now, so the built-in stop word listens in its place", "id", id)
	default:
		slog.Warn("the stop word chosen is not on the device, so the built-in stop word listens in its place", "id", id)
	}
	return builtInStop()
}

// builtInStop is the built-in stop word, written into the state folder as it always has been
// (assets.Stop); a variable for the tests, whose machine has no state folder to write it to.
var builtInStop = stopModel

// refreshOptions offers the select's options again and shows what listens: after a word is added or
// removed (the library's Changed), and after the wake words are chosen again (loadFrom).
func (s *stopWord) refreshOptions() {
	s.word.SetOptions(s.options())
	s.word.Set(s.phraseFor(config.Get().Wake.Stop.ID))
}

func (s *stopWord) restore(c config.Stop) {
	s.word.SetOptions(s.options())
	s.word.Set(s.phraseFor(c.ID))
	slog.Info("restored", "what", s.word.ObjectID, "using", s.phraseFor(c.ID))
}
