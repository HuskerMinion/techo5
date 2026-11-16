package voice

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// lastShown is the last state the screen was told.
func lastShown(t *testing.T, got *[]State) State {
	t.Helper()
	if len(*got) == 0 {
		t.Fatal("the screen was told nothing")
	}
	return (*got)[len(*got)-1]
}

// A turn that ends in an error or runs out of time tells the screen why with the idle that ends it; one
// that ends any other way says nothing of the kind.
func TestAFailedTurnSaysWhy(t *testing.T) {
	for _, tc := range []struct {
		name string
		then []event
		want string
	}{
		{"pipeline error", []event{{kind: evError, code: "stt-stream-failed", msg: "no audio"}}, FailedError},
		{"refused", []event{{kind: evError, code: "refused"}}, FailedError},
		{"send failed", []event{{kind: evError, code: "send", msg: "write: broken pipe"}}, FailedNoHome},
		{"timed out listening", []event{{kind: evTimeout}}, FailedTimeout},
		{"timed out thinking", []event{{kind: evHeard, text: "what time is it"}, {kind: evTimeout}}, FailedTimeout},
		{"canceled", []event{{kind: evCancel}}, ""},
		{"nothing said", []event{{kind: evError, code: errNoText}}, ""},
		{"answered elsewhere", []event{{kind: evError, code: errDuplicate}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWatcher(t, config.OnHA{})
			got := shownStates(t)
			w.handle(event{kind: evStart, at: w.at})
			if w.phase != phaseListening {
				t.Fatalf("the turn did not open: phase %s", w.phase)
			}
			for _, e := range tc.then {
				w.handle(e)
			}
			last := lastShown(t, got)
			if last.Phase != "idle" || last.Failed != tc.want {
				t.Errorf("the turn ended as %+v, want idle and failed %q", last, tc.want)
			}
			for _, s := range (*got)[:len(*got)-1] {
				if s.Failed != "" {
					t.Errorf("failed %q was said before the turn ended, in %+v", s.Failed, s)
				}
			}
		})
	}
}

// What was heard before the failure stays with it, for the screen to show over the reason.
func TestAFailedTurnKeepsWhatWasHeard(t *testing.T) {
	w := newWatcher(t, config.OnHA{})
	got := shownStates(t)
	w.handle(event{kind: evStart, at: w.at})
	w.handle(event{kind: evHeard, text: "what time is it"})
	w.handle(event{kind: evError, code: "intent-failed"})
	if last := lastShown(t, got); last.Heard != "what time is it" || last.Failed != FailedError {
		t.Errorf("the turn ended as %+v, want what was heard and the failure", last)
	}
}

// The reason goes with the next turn: its states carry none, and neither does anything told after the
// failure with no turn open.
func TestTheNextTurnClearsTheFailure(t *testing.T) {
	w := newWatcher(t, config.OnHA{})
	got := shownStates(t)
	w.handle(event{kind: evStart, at: w.at})
	w.handle(event{kind: evTimeout})
	if last := lastShown(t, got); last.Failed != FailedTimeout {
		t.Fatalf("the turn ended as %+v, want timed out", last)
	}
	failed := len(*got)
	w.handle(event{kind: evStart, at: w.at})
	if w.phase != phaseListening {
		t.Fatalf("the next turn did not open: phase %s", w.phase)
	}
	w.handle(event{kind: evCancel})
	if len(*got) == failed {
		t.Fatal("the next turn told the screen nothing")
	}
	for _, s := range (*got)[failed:] {
		if s.Failed != "" {
			t.Errorf("the next turn still says failed %q, in %+v", s.Failed, s)
		}
	}
}

// A late error with no turn open is not a request failing: the screen is not told it is.
func TestAnErrorWithNoTurnOpenIsNotAFailedRequest(t *testing.T) {
	w := newWatcher(t, config.OnHA{})
	got := shownStates(t)
	w.handle(event{kind: evError, code: "send", msg: "write: broken pipe"})
	for _, s := range *got {
		if s.Failed != "" {
			t.Errorf("with no turn open the screen was told failed %q", s.Failed)
		}
	}
}

// A request with nothing listening for it is said at once, with no turn opening: Home Assistant's
// pipeline not ready is one reason, and it clears what the turn before left on the screen.
func TestARequestWithNoPipelineSaysSo(t *testing.T) {
	w := newWatcher(t, config.OnHA{})
	got := shownStates(t)
	w.handle(event{kind: evStart, at: w.at})
	w.handle(event{kind: evHeard, text: "what time is it"})
	w.handle(event{kind: evCancel})

	w.ready = false
	w.handle(event{kind: evStart, at: w.at})
	if w.phase != phaseIdle {
		t.Fatalf("a turn opened with no pipeline ready: phase %s", w.phase)
	}
	want := State{Phase: "idle", Failed: FailedNotReady}
	if last := lastShown(t, got); last != want {
		t.Errorf("the screen was told %+v, want %+v", last, want)
	}
}

// A follow-up nobody asked for that cannot open is not a failed request.
func TestAFollowUpThatCannotOpenSaysNothing(t *testing.T) {
	w := newWatcher(t, config.OnHA{})
	got := shownStates(t)
	w.ready = false
	if w.start(nextTurn{followUp: true, at: w.at}) {
		t.Fatal("a follow-up opened with no pipeline ready")
	}
	for _, s := range *got {
		if s.Failed != "" {
			t.Errorf("a follow-up that could not open told the screen failed %q", s.Failed)
		}
	}
}
