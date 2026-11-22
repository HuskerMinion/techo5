package voice

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// contest stands in for feature/nearest's decision on a confirmed wake (#685): it records what was
// claimed and hands the decision to the test.
type contest struct {
	slot    int
	at      time.Time
	clarity float64
	decide  chan func(bool)
}

func withContest(t *testing.T) *contest {
	t.Helper()
	ct := &contest{decide: make(chan func(bool), 1)}
	SetConfirmedContest(func(slot int, at time.Time, clarity float64, decide func(bool)) {
		ct.slot, ct.at, ct.clarity = slot, at, clarity
		ct.decide <- decide
	})
	t.Cleanup(func() { confirmedContest.Store(nil) })
	return ct
}

// decision waits for the claim, decides it, and hands the conversation what it posted.
func (w *watcher) decision(ct *contest, answer bool) {
	w.t.Helper()
	select {
	case decide := <-ct.decide:
		decide(answer)
	case <-time.After(2 * time.Second):
		w.t.Fatal("the confirmed wake was never claimed")
	}
	w.drain()
}

// drain hands the conversation every event it posted itself.
func (w *watcher) drain() {
	for {
		select {
		case e := <-w.events:
			w.handle(e)
		default:
			return
		}
	}
}

// confirmedWake asks about a candidate heard now, then has Home Assistant hear it a moment later.
func (w *watcher) confirmedWake() (heard time.Time) {
	w.t.Helper()
	heard = w.at
	w.handle(event{kind: evCandidate, slot: 0, peak: 0.8})
	if w.watch == nil || w.watch.mode != watchConfirm {
		w.t.Fatalf("the candidate was not asked about: %+v", w.watch)
	}
	w.at = w.at.Add(900 * time.Millisecond)
	w.handle(event{kind: evWakeEnd})
	return heard
}

func TestAConfirmedWakeWaitsForTheDevicesAndOpensWhenItAnswers(t *testing.T) {
	ct := withContest(t)
	w := newWatcher(t, config.OnHA{Confirm: true})
	heard := w.confirmedWake()
	if w.phase != phaseIdle || w.deciding == nil || w.watch != nil {
		t.Fatalf("while the devices decide: phase %s, deciding %+v, watch %+v", w.phase, w.deciding, w.watch)
	}

	// Nothing else is asked about meanwhile, however soon the next question could otherwise go.
	w.nextConfirm = time.Time{}
	w.at = w.at.Add(100 * time.Millisecond)
	w.handle(event{kind: evCandidate, slot: 0, peak: 0.85})
	if w.watch != nil {
		t.Fatal("a candidate was asked about while the devices decided")
	}

	w.decision(ct, true)
	if !ct.at.Equal(heard) || ct.slot != 0 {
		t.Errorf("claimed slot %d at %v, want slot 0 at the candidate %v", ct.slot, ct.at, heard)
	}
	if w.phase != phaseListening || !w.woke || w.deciding != nil {
		t.Fatalf("answering: phase %s, woke %v, deciding %+v", w.phase, w.woke, w.deciding)
	}
	if !w.conversation.opened.Equal(heard) {
		t.Errorf("the turn opened at %v, want the candidate's %v", w.conversation.opened, heard)
	}
}

func TestAConfirmedWakeAnotherDeviceAnswersOpensNothing(t *testing.T) {
	ct := withContest(t)
	w := newWatcher(t, config.OnHA{Confirm: true})
	w.confirmedWake()
	w.decision(ct, false)
	if w.phase != phaseIdle || w.deciding != nil || !w.draining {
		t.Fatalf("another device answers: phase %s, deciding %+v, draining %v; want the run stopped",
			w.phase, w.deciding, w.draining)
	}
	if w.woke {
		t.Error("a turn opened for a wake another device answers")
	}
}

func TestALateDecisionOpensNothing(t *testing.T) {
	ct := withContest(t)
	w := newWatcher(t, config.OnHA{Confirm: true})
	w.confirmedWake()

	// The run ends before the devices have decided.
	w.handle(event{kind: evRunEnd})
	if w.deciding != nil {
		t.Fatal("still deciding after the run ended")
	}
	w.decision(ct, true)
	if w.phase != phaseIdle {
		t.Fatalf("a decision after the run ended opened a turn: phase %s", w.phase)
	}
}

func TestADecisionNeverMadeGivesTheRunUp(t *testing.T) {
	withContest(t)
	w := newWatcher(t, config.OnHA{Confirm: true})
	w.confirmedWake()
	w.later(decideMost + time.Second)
	if w.deciding != nil || !w.draining || w.phase != phaseIdle {
		t.Fatalf("no decision in time: deciding %+v, draining %v, phase %s", w.deciding, w.draining, w.phase)
	}
}

// Without the decision (feature/nearest not there), a confirmed wake opens as it did.
func TestAConfirmedWakeWithoutTheDecisionOpensAtOnce(t *testing.T) {
	w := newWatcher(t, config.OnHA{Confirm: true})
	w.confirmedWake()
	if w.phase != phaseListening || w.deciding != nil {
		t.Fatalf("no decision to wait on: phase %s, deciding %+v", w.phase, w.deciding)
	}
}

// A faint wake was claimed before it was asked about (#503): Home Assistant hearing it opens the turn.
func TestAFaintWakeIsNotClaimedAgain(t *testing.T) {
	ct := withContest(t)
	w := newWatcher(t, config.OnHA{Confirm: true})
	if err := config.Set().OnHA().ConfirmBelowScore(8); err != nil {
		t.Fatal(err)
	}
	if !w.confirmFaint(0, w.at) {
		t.Fatal("the faint wake was not asked about")
	}
	w.handle(event{kind: evWakeEnd})
	if w.phase != phaseListening || w.deciding != nil {
		t.Fatalf("a faint wake Home Assistant heard: phase %s, deciding %+v", w.phase, w.deciding)
	}
	select {
	case <-ct.decide:
		t.Error("a faint wake was claimed a second time")
	default:
	}
}
