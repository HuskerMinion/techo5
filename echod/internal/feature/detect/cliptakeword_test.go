package detect

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/takes"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// A run's takes can be of any slot with a word in it, each with its own front end, in slot order: a Show
// set up with only its second wake word (the first slot empty) offers that one alone.
func TestARunsWordsAreTheLoadedSlots(t *testing.T) {
	e := New(2, nil)
	if w := slotWords(e); len(w) != 0 {
		t.Errorf("no word loaded gives %+v", w)
	}
	e.mu.Lock()
	e.slots[1] = slot{model: model("alexa", wake.KindMicroWakeWord), loaded: true}
	e.backends[wake.KindMicroWakeWord] = &microBackend{step: map[string]frontKey{"alexa": {exact: false}}}
	e.mu.Unlock()
	if w := slotWords(e); len(w) != 1 || w[0].Slot != 1 || w[0].ID != "alexa" || w[0].Front != "zserge" {
		t.Errorf("slot 0 empty and alexa in slot 1 gives %+v", w)
	}
	e.mu.Lock()
	e.slots[0] = slot{model: model("okay_nabu", wake.KindMicroWakeWord), loaded: true}
	e.backends[wake.KindMicroWakeWord].(*microBackend).step["okay_nabu"] = frontKey{exact: true}
	e.mu.Unlock()
	w := slotWords(e)
	if len(w) != 2 || w[0].ID != "okay_nabu" || w[0].Front != "tflm" || w[0].Slot != 0 || w[1].ID != "alexa" || w[1].Slot != 1 {
		t.Errorf("with both slots loaded it gives %+v, want both in slot order", w)
	}
}

// The hook is set under the engine's lock, which slotsChanged reads it under: a word loading as the
// recorder is wired up is not a race.
func TestWiringTheWordWhileAWordLoadsIsNotARace(t *testing.T) {
	fakes(t)
	e := New(2, nil)
	rec := takes.Get()
	t.Cleanup(func() { rec.SetWakeWords(func() []takes.SlotWord { return nil }, func(takes.Capture) {}) })
	done := make(chan struct{})
	go func() { e.Use(0, model("alexa", wake.KindMicroWakeWord)); close(done) }()
	wireTakeWord(e, func(takes.Capture) {}, rec)
	<-done
}

// The recorder's copy of the wake word is taken again when a word loads: a word that loads after the
// clip store was wired up lets a run start, where the copy taken at the start had none.
func TestAWordLoadedLaterLetsARunStart(t *testing.T) {
	fakes(t)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().SendUpdate(func(w *config.WakeSend) {
		w.Target, w.URL = config.SendWakesmith, "http://trainer.local:8765"
	}); err != nil {
		t.Fatal(err)
	}
	rec := takes.Get()
	t.Cleanup(func() { rec.SetWakeWords(func() []takes.SlotWord { return nil }, func(takes.Capture) {}) })
	e := New(2, nil)
	wireTakeWord(e, func(takes.Capture) {}, rec)
	if !layout.HasMicrophone() {
		t.Skip("no microphones on this device: no run either way")
	}
	if b := rec.RunBlocked(); b != takes.RunNoWakeWord {
		t.Fatalf("blocked %v before any word loaded, want no wake word", b)
	}
	if err := e.Use(1, model("alexa", wake.KindMicroWakeWord)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for rec.RunBlocked() == takes.RunNoWakeWord {
		if time.Now().After(deadline) {
			t.Fatal("a word that loaded later still blocks the run as no wake word")
		}
		time.Sleep(5 * time.Millisecond)
	}
	e.Clear(1)
	deadline = time.Now().Add(5 * time.Second)
	for rec.RunBlocked() != takes.RunNoWakeWord {
		if time.Now().After(deadline) {
			t.Fatalf("the word cleared, and the run is blocked %v", rec.RunBlocked())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
