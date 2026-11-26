package voice

import (
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// clarityOf is the open turn's clarity, failing when no turn is open.
func (w *watcher) clarityOf() (float64, bool) {
	w.t.Helper()
	if w.turn == nil {
		w.t.Fatalf("no turn open: phase %s", w.phase)
	}
	return w.turn.Clarity()
}

// A wake Nearest device weighed opens a turn with its clarity, for the event to report (#727); a wake it
// did not, and a button, open one with none.
func TestAWeighedWakeGivesItsTurnItsClarity(t *testing.T) {
	cases := []struct {
		name  string
		e     event
		want  float64
		given bool
	}{
		{"weighed", event{kind: evStart, slot: 0, woke: true, clarity: wakeClarity{dB: 14.2, measured: true}}, 14.2, true},
		{"weighed, heard in a quiet room", event{kind: evStart, slot: 0, woke: true, clarity: wakeClarity{measured: true}}, 0, true},
		{"not weighed", event{kind: evStart, slot: 0, woke: true}, 0, false},
		{"a button", event{kind: evStart, slot: 0, clarity: wakeClarity{dB: 14.2, measured: true}}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWatcher(t, config.OnHA{})
			tc.e.at = w.at
			w.handle(tc.e)
			if dB, given := w.clarityOf(); given != tc.given || dB != tc.want {
				t.Errorf("clarity %v (given %v), want %v (given %v)", dB, given, tc.want, tc.given)
			}
		})
	}
}

// WokeClear posts the wake with what Nearest device measured; WokeAt with nothing.
func TestWokeClearCarriesTheClarity(t *testing.T) {
	c := newConversation(&esphome.VoiceSatellite{})
	at := time.Now().Add(-300 * time.Millisecond)
	c.wokeAt(0, at, wakeClarity{dB: 9.4, measured: true})
	if e := <-c.events; e.kind != evStart || !e.woke || e.clarity != (wakeClarity{dB: 9.4, measured: true}) {
		t.Fatalf("posted %+v", e)
	}
	c.WokeAt(0, at)
	if e := <-c.events; e.clarity.measured {
		t.Fatalf("WokeAt posted a clarity: %+v", e.clarity)
	}
}

// A faint wake Home Assistant heard opens its turn with the clarity it was claimed with.
func TestAConfirmedFaintWakeGivesItsTurnItsClarity(t *testing.T) {
	w := faintWatcher(t, 8)
	w.handle(event{kind: evFaint, slot: 0, at: w.at, clarity: wakeClarity{dB: 3.7, measured: true},
		then: func() { t.Error("opened a second turn") }})
	w.handle(event{kind: evWakeEnd})
	if dB, given := w.clarityOf(); !given || dB != 3.7 {
		t.Errorf("clarity %v (given %v), want 3.7", dB, given)
	}
}

// A confirmed wake's clarity goes to its turn only when the devices weighed it in a claim: answered at
// once, with Nearest device off or nobody to ask, it was not.
func TestAConfirmedWakeGivesItsTurnItsClarityOnlyWhenClaimed(t *testing.T) {
	// Each its own test: a watcher's cleanup waits on every stream, the other's too.
	for _, claimed := range []bool{true, false} {
		t.Run(map[bool]string{true: "claimed", false: "not claimed"}[claimed], func(t *testing.T) {
			ct := withContest(t)
			w := newWatcher(t, config.OnHA{Confirm: true})
			w.handle(event{kind: evCandidate, slot: 0, peak: 0.8})
			if w.watch == nil {
				t.Fatal("the candidate was not asked about")
			}
			w.watch.clarity = 11.5 // what the microphones measured as it was asked about
			w.at = w.at.Add(900 * time.Millisecond)
			w.handle(event{kind: evWakeEnd})
			w.decisionClaimed(ct, true, claimed)
			if ct.clarity != 11.5 {
				t.Errorf("claimed with %v, want 11.5", ct.clarity)
			}
			dB, given := w.clarityOf()
			if given != claimed || claimed && dB != 11.5 {
				t.Errorf("clarity %v (given %v)", dB, given)
			}
		})
	}
}
