package detect

import (
	"log/slog"
	"slices"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/detect/assets"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// HangupSlot is where the hang-up word listens, next to the stop word's and as far from Home Assistant's.
const HangupSlot = StopSlot + 1

// hangupNone is the hang-up word's select switched off.
const hangupNone = "None"

// hangUp is the hang-up word, or the stop word standing in for it, heard: it hangs up if hangupHeard
// says so, and reports whether it did. what names which, for the log.
func hangUp(what string) bool {
	phase, farEnd, replying := phone.Get().State().Phase, phone.Get().FarEndTalking(), phone.Get().Replying()
	own := ownSound(speaker.Sound().Holder(), speaker.Get().Overlaying())
	if !hangupHeard(phase, talkback.Get().Busy(), farEnd, replying, own) {
		slog.Info(what+" ignored", "phase", phase, "far_end_talking", farEnd, "reply_playing", replying,
			"own_sound", own)
		return false
	}
	slog.Info(what + ", hanging up")
	phone.Get().Hangup()
	return true
}

// ownSound reports whether the device is making a sound of its own that could have said the word: any
// claim on the speaker but the call's audio and its ring, which are the far end's and a tone, or a chime
// or beep mixed in (speaker.Player.Overlaying).
func ownSound(holder string, overlaying bool) bool {
	return overlaying || holder != "" && holder != "call" && holder != "ringing"
}

// hangupHeard reports whether the hang-up word, heard now, hangs up. Only a call that can be ended by
// voice (phone.Answerable): one ringing here or one that is up, not one being placed, and not just after
// a call has ended. Not over a talk through a camera, and not while the far end was talking: their voice
// comes out of the speaker, and the word in it is not somebody in the room hanging up. Nor while the
// device makes a sound of its own (ownSound), a turn's reply included (replying, which also covers its
// echo dying away): an announcement or an answer that says the word is the device's own speaker, which
// the far-end check does not cover and the echo canceller, not learning through a turn, hardly takes off.
func hangupHeard(phase phone.Phase, talkBack, farEnd, replying, ownSound bool) bool {
	return phone.Answerable(phase) && !talkBack && !farEnd && !replying && !ownSound
}

// hangup is the hang-up word's select and threshold, and loading its model while a call needs it.
type hangup struct {
	d         *Detect
	word      *esphome.Select
	threshold *esphome.Number

	// models is what the device has, the same library the wake words come from, and wakeWords the ids
	// chosen as wake words. Tests stand in for both.
	models    func() []wake.Model
	wakeWords func() []string

	// mu keeps one load or clear at a time.
	mu sync.Mutex
}

func newHangup(d *Detect) *hangup {
	h := &hangup{d: d, models: wake.Lib().Ours, wakeWords: chosenWakeWords}
	h.word = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "hangup_word",
			DeviceID: component.DeviceMicrophone,
			Name:     "Hang-up word",
			Icon:     "mdi:phone-hangup",
			Category: esphome.CategoryConfig,
		},
		Options: h.options(),
	}
	h.word.OnCommand = func(v string) {
		id, ok := h.idFor(v)
		if !ok {
			return
		}
		if err := config.Set().Hangup().ID(id); err != nil {
			slog.Error("saving the hang-up word failed", "err", err)
			return
		}
		h.word.Set(v)
		slog.Info("setting changed", "setting", h.word.ObjectID, "using", v)
		h.follow(hangupListens(phone.Get().State()))
	}
	h.threshold = &esphome.Number{
		Base: esphome.Base{
			ObjectID: "hangup_word_threshold",
			DeviceID: component.DeviceMicrophone,
			Name:     "Hang-up word threshold",
			Icon:     "mdi:tune-vertical",
			Category: esphome.CategoryConfig,
		},
		Min: 0.5, Max: 0.99, Step: 0.01,
		Mode: esphome.NumberBox,
	}
	h.threshold.OnCommand = func(v float32) {
		if err := config.Set().Hangup().Threshold(float64(v)); err != nil {
			slog.Error("saving the hang-up word threshold failed", "err", err)
			return
		}
		h.threshold.Set(v)
	}
	// Words added or removed change what is offered. Changed's listeners must not block.
	wake.Lib().Changed.Listen(func(struct{}) { go h.refreshOptions() })
	// Loaded while a call can be ended by voice or called off (hangupListens), and cleared otherwise. Changed's listeners must not
	// block, and a load reads a model off disk.
	// Each follow reads the phase as it runs, not as the event had it: two events close together (a
	// call declined as it rings in) may run out of order, and the later state must win.
	phone.Get().Changed.Listen(func(phone.State) {
		go h.follow(hangupListens(phone.Get().State()))
	})
	return h
}

// options are None, the stop word, and the models the device has, by phrase. Left out: one of the
// user's called "Stop", as it could not be told apart from the stop word in the list, and the wake words
// chosen now, which as the hang-up word would be a second copy of the same word, hanging up and opening
// a turn at once.
func (h *hangup) options() []string {
	out := []string{hangupNone, assets.StopPhrase}
	for _, m := range h.models() {
		if m.Phrase != assets.StopPhrase && !h.isWakeWord(m.ID) {
			out = append(out, m.Phrase)
		}
	}
	return out
}

// isWakeWord reports whether id is one of the wake words chosen now.
func (h *hangup) isWakeWord(id string) bool {
	return slices.Contains(h.wakeWords(), id)
}

// chosenWakeWords are the ids of the wake words chosen in Home Assistant's slots.
func chosenWakeWords() []string {
	var ids []string
	for _, w := range config.Get().Wake.Words {
		if w.ID != "" {
			ids = append(ids, w.ID)
		}
	}
	return ids
}

// idFor is what a select option is saved as, and whether it names anything: empty for None,
// config.HangupStop for the stop word, a model's id for one of the library's.
func (h *hangup) idFor(phrase string) (string, bool) {
	switch phrase {
	case hangupNone:
		return "", true
	case assets.StopPhrase:
		return config.HangupStop, true
	}
	for _, m := range h.models() {
		if m.Phrase == phrase {
			return m.ID, true
		}
	}
	return "", false
}

// phraseFor is the select option for what was saved. A model the device no longer has shows as None,
// which is what the device then does.
func (h *hangup) phraseFor(id string) string {
	switch id {
	case "":
		return hangupNone
	case config.HangupStop:
		return assets.StopPhrase
	}
	if m, ok := wake.Find(h.models(), id); ok {
		return m.Phrase
	}
	return hangupNone
}

// refreshOptions offers the select's options again, from what the device has and the wake words chosen
// now, and shows what is chosen: after a word is added or removed (the library's Changed), and after the
// wake words are chosen again (loadLocked).
func (h *hangup) refreshOptions() {
	h.word.SetOptions(h.options())
	h.word.Set(h.phraseFor(config.Get().Wake.Hangup.ID))
}

func (h *hangup) restore(c config.Hangup) {
	h.word.SetOptions(h.options())
	h.word.Set(h.phraseFor(c.ID))
	h.threshold.Set(float32(c.ThresholdAt()))
	slog.Info("restored", "what", h.word.ObjectID, "using", h.phraseFor(c.ID))
}

// hangupListens is whether the hang-up word is listened for: while a call can be ended by voice, and
// while a call asked for by voice waits to be dialed, which the word calls off (routeCancelCall).
func hangupListens(st phone.State) bool { return phone.Answerable(st.Phase) || st.Waiting }

// wanted is the model the slot should hold: none off a call, with None (the default) or the stop word
// (it has a slot of its own, loaded already), or with a word that has since been chosen as a wake word;
// otherwise the library's model.
func (h *hangup) wanted(onCall bool) (wake.Model, bool) {
	c := config.Get().Wake.Hangup
	if !onCall || !c.On() || c.StopWord() {
		return wake.Model{}, false
	}
	id := c.ID
	if h.isWakeWord(id) {
		slog.Warn("the hang-up word is a wake word now, so it is not listened for as the hang-up word", "id", id)
		return wake.Model{}, false
	}
	m, ok := wake.Find(h.models(), id)
	if !ok {
		slog.Warn("the hang-up word is not on the device", "id", id)
	}
	return m, ok
}

// follow puts the chosen model in the slot while a call is on, and takes it out otherwise.
func (h *hangup) follow(onCall bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	want, ok := h.wanted(onCall)
	// What the engine has, not what was last put there: a reload of the wake words empties every slot.
	have, loaded := h.d.engine.Loaded(HangupSlot)
	switch {
	case !ok && !loaded:
		return
	case !ok:
		h.d.engine.Clear(HangupSlot)
		slog.Info("hang-up word off")
		return
	case loaded && have.ID == want.ID:
		return
	}
	if err := h.d.engine.Use(HangupSlot, want); err != nil {
		slog.Error("loading the hang-up word failed", "id", want.ID, "err", err)
		h.d.engine.Clear(HangupSlot)
		return
	}
	slog.Info("hang-up word listening", "id", want.ID, "threshold", config.Get().Wake.Hangup.ThresholdAt())
}
