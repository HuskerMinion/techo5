package voice

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A canceled turn says why, so a device that stood aside for another is not counted as one that failed.
func TestACanceledTurnSaysWhy(t *testing.T) {
	for _, tc := range []struct {
		name string
		then []event
		want string
	}{
		{"stopped", []event{{kind: evCancel}}, "stopped"},
		{"answered elsewhere", []event{{kind: evError, code: errDuplicate}}, "answered_elsewhere"},
		// A wake word while listening is part of the sentence; once thinking, it takes the turn over.
		{"interrupted", []event{{kind: evHeard, text: "what time is it"}, {kind: evStart}}, "interrupted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWatcher(t, config.OnHA{})
			got := turnEvents(t)

			w.handle(event{kind: evStart, at: w.at})
			id := w.turn.ID()
			for _, e := range tc.then {
				e.at = w.at
				w.handle(e)
			}

			data := ended(t, got, id)
			if data["ended"] != "canceled" || data["reason"] != tc.want {
				t.Errorf("ended %q, reason %q; want canceled, %q", data["ended"], data["reason"], tc.want)
			}
		})
	}
}

// Only a canceled turn has a reason.
func TestAnAnsweredTurnHasNoReason(t *testing.T) {
	w := newWatcher(t, config.OnHA{})
	got := turnEvents(t)

	w.handle(event{kind: evStart, at: w.at})
	id := w.turn.ID()
	for _, e := range []event{{kind: evHeard, text: "what time is it"}, {kind: evReplyText, text: "It's noon."}, {kind: evRunEnd}} {
		w.handle(e)
	}
	if data := ended(t, got, id); data["reason"] != "" {
		t.Errorf("reason is %q on an answered turn", data["reason"])
	}
}

// The wake word's peak is known only once its hold ends, which can be before the turn opens, while it is
// open, or after another device has already answered it. Each way, the turn's event carries it.
func TestTheWakeScoreReachesItsTurn(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before bool // the peak arrives before the turn opens
		lost   bool // another device answers before the peak arrives
	}{
		{name: "while open"},
		{name: "after it ended", lost: true},
		{name: "before it opened", before: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWatcher(t, config.OnHA{})
			got := turnEvents(t)

			if tc.before {
				w.handle(event{kind: evScored, slot: 0, peak: 0.934, at: w.at})
			}
			w.handle(event{kind: evStart, woke: true, at: w.at.Add(100 * time.Millisecond)})
			id := w.turn.ID()
			if tc.lost {
				w.handle(event{kind: evError, code: errDuplicate})
			}
			if !tc.before {
				w.handle(event{kind: evScored, slot: 0, peak: 0.934, at: w.at.Add(400 * time.Millisecond)})
			}
			if !tc.lost {
				w.handle(event{kind: evCancel})
			}

			if data := ended(t, got, id); data["wake_score"] != "0.93" {
				t.Errorf("wake_score is %q, want 0.93", data["wake_score"])
			}
		})
	}
}

// A peak from another slot, or from a wake long before, is not this turn's. Its event goes without a
// score once the wait for one runs out.
func TestAScoreFromAnotherWakeIsNotTaken(t *testing.T) {
	for _, tc := range []struct {
		name string
		peak event
	}{
		{"another slot", event{kind: evScored, slot: 1, peak: 0.97}},
		{"an earlier wake", event{kind: evScored, slot: 0, peak: 0.97}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWatcher(t, config.OnHA{})
			got := turnEvents(t)

			opened := w.at.Add(2 * time.Second)
			tc.peak.at = opened.Add(200 * time.Millisecond)
			if tc.name == "an earlier wake" {
				tc.peak.at = w.at
				w.handle(tc.peak)
			}
			w.handle(event{kind: evStart, woke: true, at: opened})
			id := w.turn.ID()
			if tc.name == "another slot" {
				w.handle(tc.peak)
			}
			w.handle(event{kind: evCancel})

			if data := ended(t, got, id); data["wake_score"] != "" {
				t.Errorf("wake_score is %q, want none", data["wake_score"])
			}
		})
	}
}

// A turn the wake word did not open has no score and does not wait for one.
func TestAButtonTurnSendsAtOnce(t *testing.T) {
	w := newWatcher(t, config.OnHA{})
	got := turnEvents(t)

	w.handle(event{kind: evStart, at: w.at})
	id := w.turn.ID()
	ended0 := time.Now()
	w.handle(event{kind: evCancel})

	data := ended(t, got, id)
	if waited := time.Since(ended0); waited > 500*time.Millisecond {
		t.Errorf("the event took %v, and nothing was owed", waited)
	}
	if data["wake_score"] != "" {
		t.Errorf("wake_score is %q on a button turn", data["wake_score"])
	}
}
