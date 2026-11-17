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

// A run's takes are of the first slot with a word in it: a Show set up with only its second wake word
// (the first slot empty) records that one, with its own front end.
func TestARunsWordIsTheFirstLoadedSlots(t *testing.T) {
	e := New(2, nil)
	if w, f := firstWakeWord(e); w != "" || f != "" {
		t.Errorf("no word loaded gives %q on %q", w, f)
	}
	e.mu.Lock()
	e.slots[1] = slot{model: model("alexa", wake.KindMicroWakeWord), loaded: true}
	e.backends[wake.KindMicroWakeWord] = &microBackend{step: map[string]frontKey{"alexa": {exact: false}}}
	e.mu.Unlock()
	if w, f := firstWakeWord(e); w != "alexa" || f != "zserge" {
		t.Errorf("slot 0 empty and alexa in slot 1 gives %q on %q", w, f)
	}
	e.mu.Lock()
	e.slots[0] = slot{model: model("okay_nabu", wake.KindMicroWakeWord), loaded: true}
	e.backends[wake.KindMicroWakeWord].(*microBackend).step["okay_nabu"] = frontKey{exact: true}
	e.mu.Unlock()
	if w, f := firstWakeWord(e); w != "okay_nabu" || f != "tflm" {
		t.Errorf("with both slots loaded it gives %q on %q, want the first", w, f)
	}
}

// The hook is set under the engine's lock, which slotsChanged reads it under: a word loading as the
// recorder is wired up is not a race.
func TestWiringTheWordWhileAWordLoadsIsNotARace(t *testing.T) {
	fakes(t)
	e := New(2, nil)
	rec := takes.Get()
	t.Cleanup(func() { rec.SetWakeWord(func() (string, string) { return "", "" }, func(takes.Capture) {}) })
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
	t.Cleanup(func() { rec.SetWakeWord(func() (string, string) { return "", "" }, func(takes.Capture) {}) })
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
