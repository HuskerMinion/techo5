package detect

import (
	"path/filepath"
	"slices"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/detect/assets"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// The hang-up word hangs up only a call ringing here or one that is up, never one being placed, never
// over a talk through a camera, not while the far end was talking, and not while a turn's reply or any
// other sound of the device's own plays, which might say the word itself.
func TestHangupHeard(t *testing.T) {
	for _, c := range []struct {
		name                                 string
		phase                                phone.Phase
		talkBack, farEnd, replying, ownSound bool
		want                                 bool
	}{
		{"a call", phone.Talking, false, false, false, false, true},
		{"ringing in", phone.Ringing, false, false, false, false, true},
		{"placing a call", phone.Dialing, false, false, false, false, false},
		{"no call", phone.Idle, false, false, false, false, false},
		{"the far end talking", phone.Talking, false, true, false, false, false},
		{"a talk through a camera", phone.Talking, true, false, false, false, false},
		{"a reply playing", phone.Talking, false, false, true, false, false},
		{"an announcement or a chime playing", phone.Talking, false, false, false, true, false},
	} {
		if got := hangupHeard(c.phase, c.talkBack, c.farEnd, c.replying, c.ownSound); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// Our own sound is anything holding the speaker but the call and its ring, or a chime or beep mixed in.
func TestOwnSound(t *testing.T) {
	for _, c := range []struct {
		holder  string
		overlay bool
		want    bool
	}{
		{"", false, false}, {"call", false, false}, {"ringing", false, false},
		{"announce", false, true}, {"reply", false, true}, {"call", true, true},
	} {
		if got := ownSound(c.holder, c.overlay); got != c.want {
			t.Errorf("holder %q, overlay %v: got %v, want %v", c.holder, c.overlay, got, c.want)
		}
	}
}

// A model is in its slot only while a call can be ended by voice, and only one of the library's: None,
// the default, and the stop word, which has its own slot already loaded, load nothing, and neither does a
// word chosen since as a wake word. A slot a reload of the wake words emptied is filled again rather than
// taken as still loaded, and a model the device no longer has loads nothing.
func TestHangupFollowsTheCall(t *testing.T) {
	fakes(t)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	e := New(HangupSlot+1, nil)
	var wakeWords []string
	h := &hangup{
		d:         &Detect{engine: e},
		models:    func() []wake.Model { return []wake.Model{model("computer", wake.KindMicroWakeWord)} },
		wakeWords: func() []string { return wakeWords },
	}
	loaded := func() string {
		m, ok := e.Loaded(HangupSlot)
		if !ok {
			return ""
		}
		return m.ID
	}
	choose := func(id string) {
		t.Helper()
		if err := config.Set().Hangup().ID(id); err != nil {
			t.Fatal(err)
		}
	}

	h.follow(true)
	if got := loaded(); got != "" {
		t.Fatalf("by default, loaded %q", got)
	}

	choose("computer")
	h.follow(false)
	if got := loaded(); got != "" {
		t.Fatalf("no call, loaded %q", got)
	}
	h.follow(true)
	if got := loaded(); got != "computer" {
		t.Fatalf("on a call, loaded %q, want computer", got)
	}
	e.Clear(HangupSlot) // as a reload of the wake words does
	h.follow(true)
	if got := loaded(); got != "computer" {
		t.Fatalf("after the slot was emptied, loaded %q, want computer", got)
	}

	wakeWords = []string{"computer"}
	h.follow(true)
	if got := loaded(); got != "" {
		t.Fatalf("a wake word now, still loaded %q as the hang-up word", got)
	}
	wakeWords = nil

	choose(config.HangupStop)
	h.follow(true)
	if got := loaded(); got != "" {
		t.Fatalf("the stop word chosen, loaded %q", got)
	}

	choose("")
	h.follow(true)
	if got := loaded(); got != "" {
		t.Fatalf("None chosen, loaded %q", got)
	}

	choose("gone")
	h.follow(true)
	if got := loaded(); got != "" {
		t.Fatalf("a model the device does not have, loaded %q", got)
	}
}

// The select offers None and the stop word first, then the device's models but for the wake words
// chosen now and one of the user's called "Stop". None is saved as empty, the default.
func TestHangupOptions(t *testing.T) {
	h := &hangup{
		models: func() []wake.Model {
			return []wake.Model{model("computer", wake.KindMicroWakeWord), model("okay_nabu", wake.KindMicroWakeWord),
				{ID: "my_stop", Phrase: assets.StopPhrase, Kind: wake.KindMicroWakeWord}}
		},
		wakeWords: func() []string { return []string{"okay_nabu"} },
	}
	want := []string{hangupNone, assets.StopPhrase, "computer"}
	if got := h.options(); !slices.Equal(got, want) {
		t.Errorf("options %v, want %v", got, want)
	}
	for _, c := range []struct{ phrase, id string }{
		{hangupNone, ""}, {assets.StopPhrase, config.HangupStop}, {"computer", "computer"},
	} {
		if id, ok := h.idFor(c.phrase); !ok || id != c.id {
			t.Errorf("idFor(%q) = %q, %v; want %q", c.phrase, id, ok, c.id)
		}
		if got := h.phraseFor(c.id); got != c.phrase {
			t.Errorf("phraseFor(%q) = %q, want %q", c.id, got, c.phrase)
		}
	}
	if _, ok := h.idFor("nothing like it"); ok {
		t.Error("an unknown option was taken")
	}
	if got := h.phraseFor("gone"); got != hangupNone {
		t.Errorf("a model the device no longer has shows as %q", got)
	}
}

// Its threshold is a wake word's unless one is set.
func TestHangupThreshold(t *testing.T) {
	if got := (config.Hangup{}).ThresholdAt(); got != config.DefaultThreshold {
		t.Errorf("unset: %v, want %v", got, config.DefaultThreshold)
	}
	if got := (config.Hangup{Threshold: 0.93}).ThresholdAt(); got != 0.93 {
		t.Errorf("set: %v", got)
	}
}

// The select's options follow the library and the wake words chosen, and what is chosen stays shown.
func TestHangupOptionsRefresh(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	models := []wake.Model{model("computer", wake.KindMicroWakeWord)}
	wakeWords := []string{}
	h := &hangup{
		word:      &esphome.Select{},
		models:    func() []wake.Model { return models },
		wakeWords: func() []string { return wakeWords },
	}
	if err := config.Set().Hangup().ID("computer"); err != nil {
		t.Fatal(err)
	}
	h.refreshOptions()
	if got, want := h.word.Options, []string{hangupNone, assets.StopPhrase, "computer"}; !slices.Equal(got, want) {
		t.Fatalf("options %v, want %v", got, want)
	}

	models = append(models, model("jarvis", wake.KindMicroWakeWord))
	wakeWords = []string{"computer"}
	h.refreshOptions()
	if got, want := h.word.Options, []string{hangupNone, assets.StopPhrase, "jarvis"}; !slices.Equal(got, want) {
		t.Errorf("after a word was added and the chosen one became a wake word: options %v, want %v", got, want)
	}
}

// The hang-up word is listened for while a call rings or is up, and while a call asked for by voice
// waits to be dialed; not while one is dialed or nothing is on.
func TestHangupListens(t *testing.T) {
	for _, c := range []struct {
		st   phone.State
		want bool
	}{
		{phone.State{Phase: phone.Idle}, false},
		{phone.State{Phase: phone.Ringing}, true},
		{phone.State{Phase: phone.Talking}, true},
		{phone.State{Phase: phone.Dialing}, false},
		{phone.State{Phase: phone.Dialing, Waiting: true}, true},
	} {
		if got := hangupListens(c.st); got != c.want {
			t.Errorf("%+v: listens %v, want %v", c.st, got, c.want)
		}
	}
}
