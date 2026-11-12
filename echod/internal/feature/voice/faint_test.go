package voice

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// faintWatcher is a watcher with confirming on and Confirm below nearest-device score set.
func faintWatcher(t *testing.T, below int) *watcher {
	t.Helper()
	w := newWatcher(t, config.OnHA{Confirm: true})
	if err := config.Set().OnHA().ConfirmBelowScore(below); err != nil {
		t.Fatal(err)
	}
	return w
}

// A wake won faintly is asked about (#503), and Home Assistant hearing it opens the turn reaching back to
// when the word was heard, not to when it was asked: the nearest-device window came in between.
func TestAFaintWakeIsAskedAboutAndReachesBack(t *testing.T) {
	w := faintWatcher(t, 8)
	heard := w.at.Add(-300 * time.Millisecond)
	ran := false
	w.handle(event{kind: evFaint, slot: 0, at: heard, then: func() { ran = true }})
	if w.watch == nil || w.watch.mode != watchConfirm || !w.watch.faint || !w.watch.from.Equal(heard) || w.opened() != 1 {
		t.Fatalf("a faint wake: watch %+v, runs %d", w.watch, w.opened())
	}
	w.handle(event{kind: evWakeEnd})
	if w.phase != phaseListening || !w.woke {
		t.Fatalf("a confirmed faint wake: phase %s, woke %v", w.phase, w.woke)
	}
	if ran {
		t.Error("the turn was opened again besides Home Assistant's")
	}
}

// One Home Assistant doesn't hear opens nothing: that is the TV the setting is for.
func TestAFaintWakeHomeAssistantDoesNotHearOpensNothing(t *testing.T) {
	w := faintWatcher(t, 8)
	w.handle(event{kind: evFaint, slot: 0, at: w.at, then: func() { t.Error("opened a turn Home Assistant didn't hear") }})
	w.handle(event{kind: evUnconfirmed})
	w.handle(event{kind: evRunEnd})
	if w.watch != nil || w.phase != phaseIdle {
		t.Fatalf("not heard: watch %+v, phase %s", w.watch, w.phase)
	}
	// The setting doing its job: no longer wait before the next ordinary question, and no step towards
	// the warning about a wake word the engine doesn't list.
	if w.confirmGap != 0 || w.unheard != 0 || w.nextConfirm.After(w.at) {
		t.Errorf("a faint wake not heard held back the ordinary ones: gap %v, unheard %d, next %v",
			w.confirmGap, w.unheard, w.nextConfirm.Sub(w.at))
	}
}

// One that can't be asked is answered as any wake is, so the setting never loses a request: Home
// Assistant away, the setting off, the second wake word. The spacing between questions doesn't hold one
// back either.
func TestAFaintWakeThatCannotBeAskedIsAnswered(t *testing.T) {
	cases := []struct {
		name  string
		below int
		slot  int
		setup func(*watcher)
	}{
		{"home assistant away", 8, 0, func(w *watcher) { w.ready = false }},
		{"setting off", 0, 0, nil},
		{"second wake word", 8, 1, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := faintWatcher(t, tc.below)
			if tc.setup != nil {
				tc.setup(w)
			}
			ran := make(chan struct{}, 1)
			w.handle(event{kind: evFaint, slot: tc.slot, at: w.at, then: func() { ran <- struct{}{} }})
			select {
			case <-ran:
			case <-time.After(time.Second):
				t.Fatal("not answered")
			}
			if w.watch != nil {
				t.Errorf("asked about all the same: %+v", w.watch)
			}
		})
	}

	t.Run("asked too recently", func(t *testing.T) {
		w := faintWatcher(t, 8)
		w.handle(event{kind: evCandidate, slot: 0, peak: 0.8})
		w.handle(event{kind: evUnconfirmed})
		w.handle(event{kind: evRunEnd})
		w.at = w.at.Add(time.Second)
		w.handle(event{kind: evFaint, slot: 0, at: w.at, then: func() { t.Error("answered without asking") }})
		if w.watch == nil || !w.watch.faint {
			t.Fatalf("held back by the spacing: watch %+v", w.watch)
		}
	})
}
