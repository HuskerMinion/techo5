package detect

import (
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// Two keepers each have the peaks since their own mark: a reply said during a call does not start the
// call's peaks over, nor take them as it ends, as a second user of one shared set would.
func TestEachKeeperHasItsOwnPeaks(t *testing.T) {
	made := fakes(t)
	e := New(2, nil)
	if err := e.Use(0, model("hey_jarvis", wake.KindMicroWakeWord)); err != nil {
		t.Fatalf("Use(0): %v", err)
	}
	scores := made[wake.KindMicroWakeWord].loaded
	call, reply := e.NewPeaks(), e.NewPeaks()

	call.MarkPeaks()
	scores["hey_jarvis"] = 0.3
	e.score([]int16{0}, nil)

	reply.MarkPeaks()
	scores["hey_jarvis"] = 0.2
	e.score([]int16{0}, nil)

	if got := reply.Peaks(); len(got) != 1 || got[0].Peak != 0.2 {
		t.Fatalf("the reply's peaks are %+v, want 0.2: what scored before its mark is not its", got)
	}
	scores["hey_jarvis"] = 0.1
	e.score([]int16{0}, nil)
	if got := call.Peaks(); len(got) != 1 || got[0].Peak != 0.3 || got[0].ID != "hey_jarvis" || got[0].Slot != 0 {
		t.Fatalf("the call's peaks are %+v, want hey_jarvis at 0.3: the reply's end took them", got)
	}
	if got := call.Peaks(); got != nil {
		t.Errorf("peaks read twice came again: %+v", got)
	}

	// With nothing marked, nothing is kept.
	scores["hey_jarvis"] = 0.4
	e.score([]int16{0}, nil)
	if got := reply.Peaks(); got != nil {
		t.Errorf("peaks kept with no mark: %+v", got)
	}
}

// A peak says what it was judged against, the cutoff as it was then and not as it is when asked, and
// whether the word fired. Scores held back while the slot settles count, and say so: the peak is what
// the model made of the sound, held what could be done about it.
func TestAPeakSaysItsCutoffAndWhetherItFired(t *testing.T) {
	fakes(t)
	e := New(2, nil)
	cutoff := 0.5
	e.Threshold = func(int) float64 { return cutoff }
	for slot, id := range []string{"hey_jarvis", "stop"} {
		if err := e.Use(slot, model(id, wake.KindMicroWakeWord)); err != nil {
			t.Fatalf("Use(%d): %v", slot, err)
		}
	}
	k := e.NewPeaks()
	k.MarkPeaks()

	at := time.Now()
	e.judge(1, &e.slots[1], 0.41, at, nil)
	cutoff = 0.7
	e.judge(1, &e.slots[1], 0.30, at.Add(20*time.Millisecond), nil)
	e.judge(0, &e.slots[0], 0.92, at.Add(40*time.Millisecond), nil)

	// Held back, and still the slot's highest.
	e.Settle(time.Minute)
	e.judge(1, &e.slots[1], 0.45, time.Now(), nil)

	got := k.Peaks()
	if len(got) != 2 {
		t.Fatalf("%d peaks, want one for each word: %+v", len(got), got)
	}
	word, stop := got[0], got[1]
	if !word.Fired || word.Held || word.Peak != 0.92 || word.Cutoff != 0.7 {
		t.Errorf("the wake word's peak is %+v, want 0.92 against 0.7, fired, not held", word)
	}
	if stop.Fired || !stop.Held || stop.Peak != 0.45 || stop.Cutoff != 0.7 || stop.Canceling {
		t.Errorf("the stop word's peak is %+v, want the held back 0.45 against 0.7, not fired", stop)
	}

	// The cutoff is the peak's own, not the latest score's.
	k.MarkPeaks()
	cutoff = 0.5
	at = time.Now().Add(2 * time.Minute)
	e.judge(1, &e.slots[1], 0.41, at, nil)
	cutoff = 0.7
	e.judge(1, &e.slots[1], 0.30, at.Add(20*time.Millisecond), nil)
	if got := k.Peaks(); len(got) != 1 || got[0].Peak != 0.41 || got[0].Cutoff != 0.5 || got[0].Held || !got[0].At.Equal(at) {
		t.Errorf("peaks %+v, want 0.41 against the 0.5 it was judged by, at its own time, not held", got)
	}
}

// A word that fired just before the mark goes on scoring through its hold, over its cutoff, and cannot
// fire again: its peak is held, so a line with the peak over the cutoff and the word not heard says why.
func TestAPeakInTheHoldBeforeTheMarkIsHeld(t *testing.T) {
	fakes(t)
	e := New(2, nil)
	e.Threshold = func(int) float64 { return 0.5 }
	if err := e.Use(1, model("stop", wake.KindMicroWakeWord)); err != nil {
		t.Fatalf("Use(1): %v", err)
	}
	k := e.NewPeaks()

	at := time.Now()
	e.judge(1, &e.slots[1], 0.6, at, nil) // fires, before the mark
	k.MarkPeaks()
	e.judge(1, &e.slots[1], 0.9, at.Add(100*time.Millisecond), nil)

	got := k.Peaks()
	if len(got) != 1 || got[0].Peak != 0.9 || got[0].Fired || !got[0].Held {
		t.Errorf("peaks %+v, want 0.9 held and not fired since the mark", got)
	}
}

// Whether the canceller was running is asked as the peak comes, and kept with it: a later score that is
// not a new peak does not change it. This is the field that tells a word the canceller took from a
// cutoff set too high, so both values are seen here.
func TestAPeakSaysWhetherTheCancellerRan(t *testing.T) {
	fakes(t)
	e := New(2, nil)
	e.Threshold = func(int) float64 { return 0.5 }
	if err := e.Use(1, model("stop", wake.KindMicroWakeWord)); err != nil {
		t.Fatalf("Use(1): %v", err)
	}
	k := e.NewPeaks()
	canceling := false
	k.canceling = func() bool { return canceling }

	k.MarkPeaks()
	at := time.Now()
	e.judge(1, &e.slots[1], 0.2, at, nil)
	canceling = true
	e.judge(1, &e.slots[1], 0.1, at.Add(20*time.Millisecond), nil)
	if got := k.Peaks(); len(got) != 1 || got[0].Peak != 0.2 || got[0].Canceling {
		t.Errorf("peaks %+v, want 0.2 with the canceller not running as it came", got)
	}

	k.MarkPeaks()
	e.judge(1, &e.slots[1], 0.3, at.Add(40*time.Millisecond), nil)
	canceling = false
	e.judge(1, &e.slots[1], 0.1, at.Add(60*time.Millisecond), nil)
	if got := k.Peaks(); len(got) != 1 || got[0].Peak != 0.3 || !got[0].Canceling {
		t.Errorf("peaks %+v, want 0.3 with the canceller running as it came", got)
	}

	// The engine's own keeper asks the engine's microphones, and an engine without any is not canceling.
	k = e.NewPeaks()
	k.MarkPeaks()
	e.judge(1, &e.slots[1], 0.4, at.Add(80*time.Millisecond), nil)
	if got := k.Peaks(); len(got) != 1 || got[0].Canceling {
		t.Errorf("peaks %+v, want the canceller not running with no microphones", got)
	}
}

// A word swapped into a slot since the mark starts a peak of its own, and one taken out keeps the peak
// it had.
func TestAKeeperFollowsTheWordInTheSlot(t *testing.T) {
	made := fakes(t)
	e := New(2, nil)
	if err := e.Use(0, model("hey_jarvis", wake.KindMicroWakeWord)); err != nil {
		t.Fatalf("Use(0): %v", err)
	}
	k := e.NewPeaks()
	k.MarkPeaks()
	made[wake.KindMicroWakeWord].loaded["hey_jarvis"] = 0.6
	e.score([]int16{0}, nil)

	if err := e.Use(0, model("okay_nabu", wake.KindMicroWakeWord)); err != nil {
		t.Fatalf("Use(0): %v", err)
	}
	made[wake.KindMicroWakeWord].loaded["okay_nabu"] = 0.2
	e.score([]int16{0}, nil)
	e.Clear(0)

	if got := k.Peaks(); len(got) != 1 || got[0].ID != "okay_nabu" || got[0].Peak != 0.2 {
		t.Errorf("peaks %+v, want okay_nabu at 0.2", got)
	}
}

// Keepers are marked and read on other goroutines while the loop scores, each lock taken after the
// engine's and never before it. Run with -race.
func TestKeepersAreReadWhileTheEngineScores(t *testing.T) {
	made := fakes(t)
	e := New(2, nil)
	if err := e.Use(0, model("hey_jarvis", wake.KindMicroWakeWord)); err != nil {
		t.Fatalf("Use(0): %v", err)
	}
	made[wake.KindMicroWakeWord].loaded["hey_jarvis"] = 0.3
	keepers := []*PeakKeeper{e.NewPeaks(), e.NewPeaks()}

	done := make(chan struct{})
	var scoring sync.WaitGroup
	scoring.Add(1)
	go func() {
		defer scoring.Done()
		for {
			select {
			case <-done:
				return
			default:
				e.score([]int16{0}, nil)
			}
		}
	}()
	var wg sync.WaitGroup
	for _, k := range keepers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 500 {
				k.MarkPeaks()
				for _, p := range k.Peaks() {
					if p.ID != "hey_jarvis" || p.Peak != 0.3 {
						t.Errorf("read %+v while scoring", p)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(done)
	scoring.Wait()
}
