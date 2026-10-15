package detect

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// A wake reports the highest score it reached once the hold is over, not the score that first crossed
// the cutoff: the peak is what says how far over the threshold it was. The cutoff it reports is the one
// it crossed, even if the threshold has dropped since, as it does when the wake tone starts the canceller.
func TestAWakeReportsItsPeak(t *testing.T) {
	fakes(t)
	e := New(2, nil)
	e.Threshold = func(int) float64 { return 0.9 }
	if err := e.Use(0, model("hey_jarvis", wake.KindMicroWakeWord)); err != nil {
		t.Fatalf("Use(0): %v", err)
	}

	type report struct {
		slot         int
		peak, cutoff float64
	}
	got := make(chan report, 2)
	e.OnScored = func(slot int, peak, cutoff float64) { got <- report{slot, peak, cutoff} }

	at := time.Now()
	s := &e.slots[0]
	e.judge(0, s, 0.92, at, nil)
	e.Threshold = func(int) float64 { return 0.8 }
	e.judge(0, s, 0.97, at.Add(100*time.Millisecond), nil)
	e.judge(0, s, 0.95, at.Add(200*time.Millisecond), nil)
	select {
	case r := <-got:
		t.Fatalf("reported %+v before the hold was over", r)
	case <-time.After(50 * time.Millisecond):
	}

	e.judge(0, s, 0.4, at.Add(Hold+time.Millisecond), nil)
	select {
	case r := <-got:
		if r != (report{0, 0.97, 0.9}) {
			t.Errorf("reported %+v, want slot 0 at its peak 0.97 against 0.9", r)
		}
	case <-time.After(time.Second):
		t.Fatal("no score was reported after the hold")
	}
}

// A score between Candidate and the threshold is asked about once the utterance falls away, with the
// peak it reached; one that goes on over the threshold is a wake and nothing else.
func TestACandidateIsAskedAboutOnceItFallsAway(t *testing.T) {
	fakes(t)
	e := New(2, nil)
	e.Threshold = func(int) float64 { return 0.9 }
	e.Candidate = func(int) float64 { return 0.7 }
	if err := e.Use(0, model("hey_jarvis", wake.KindMicroWakeWord)); err != nil {
		t.Fatalf("Use(0): %v", err)
	}
	asked := make(chan float64, 2)
	e.OnCandidate = func(_ int, peak float64) { asked <- peak }
	woke := make(chan int, 2)
	e.OnDetect = func(slot int) { woke <- slot }

	at := time.Now()
	s := &e.slots[0]
	e.judge(0, s, 0.72, at, nil)
	e.judge(0, s, 0.81, at.Add(80*time.Millisecond), nil)
	select {
	case p := <-asked:
		t.Fatalf("asked about %v while the utterance was still going", p)
	case <-time.After(50 * time.Millisecond):
	}
	e.judge(0, s, 0.3, at.Add(160*time.Millisecond), nil)
	select {
	case p := <-asked:
		if p != 0.81 {
			t.Errorf("asked about a peak of %v, want 0.81", p)
		}
	case <-time.After(time.Second):
		t.Fatal("the candidate was not asked about")
	}

	// Past the refractory, one that goes on to cross the threshold wakes, and is not asked about.
	at = at.Add(Refractory + time.Second)
	e.judge(0, s, 0.75, at, nil)
	e.judge(0, s, 0.95, at.Add(80*time.Millisecond), nil)
	e.judge(0, s, 0.2, at.Add(80*time.Millisecond+Hold+time.Millisecond), nil)
	select {
	case <-woke:
	case <-time.After(time.Second):
		t.Fatal("the word over the threshold did not wake")
	}
	select {
	case p := <-asked:
		t.Errorf("a wake was also asked about, at %v", p)
	case <-time.After(100 * time.Millisecond):
	}
}

// A sound that stays between the two is asked about after CandidateFor rather than when it ends, and
// with no Candidate nothing is ever asked.
func TestACandidateThatGoesOnIsAskedAnyway(t *testing.T) {
	fakes(t)
	e := New(2, nil)
	e.Threshold = func(int) float64 { return 0.9 }
	if err := e.Use(0, model("hey_jarvis", wake.KindMicroWakeWord)); err != nil {
		t.Fatalf("Use(0): %v", err)
	}
	asked := make(chan float64, 2)
	e.OnCandidate = func(_ int, peak float64) { asked <- peak }

	at := time.Now()
	s := &e.slots[0]
	e.judge(0, s, 0.8, at, nil)
	e.judge(0, s, 0.3, at.Add(100*time.Millisecond), nil)
	select {
	case p := <-asked:
		t.Fatalf("asked about %v with no Candidate set", p)
	case <-time.After(50 * time.Millisecond):
	}

	e.Candidate = func(int) float64 { return 0.7 }
	at = at.Add(time.Second)
	e.judge(0, s, 0.8, at, nil)
	e.judge(0, s, 0.8, at.Add(CandidateFor), nil)
	select {
	case <-asked:
	case <-time.After(time.Second):
		t.Fatal("a candidate that went on was never asked about")
	}
}

// A word said weakly and then again clearly wakes on the clear one: asking about the first pauses only
// the candidates, not detection. And a score under MinCandidate never asks what Candidate is.
func TestAClearRepeatAfterACandidateStillWakes(t *testing.T) {
	fakes(t)
	e := New(2, nil)
	e.Threshold = func(int) float64 { return 0.9 }
	looked := 0
	e.Candidate = func(int) float64 { looked++; return 0.7 }
	if err := e.Use(0, model("hey_jarvis", wake.KindMicroWakeWord)); err != nil {
		t.Fatalf("Use(0): %v", err)
	}
	asked := make(chan float64, 4)
	e.OnCandidate = func(_ int, peak float64) { asked <- peak }
	woke := make(chan int, 2)
	e.OnDetect = func(slot int) { woke <- slot }

	at := time.Now()
	s := &e.slots[0]
	e.judge(0, s, 0.2, at, nil)
	e.judge(0, s, 0.4, at.Add(80*time.Millisecond), nil)
	if looked != 0 {
		t.Fatalf("Candidate was asked %d times for scores under MinCandidate", looked)
	}
	e.judge(0, s, 0.8, at.Add(160*time.Millisecond), nil)
	e.judge(0, s, 0.3, at.Add(240*time.Millisecond), nil)
	select {
	case <-asked:
	case <-time.After(time.Second):
		t.Fatal("the weak word was not asked about")
	}

	// Well inside Refractory: the clear repeat wakes, and is not asked about again.
	at = at.Add(400 * time.Millisecond)
	e.judge(0, s, 0.8, at, nil)
	e.judge(0, s, 0.97, at.Add(80*time.Millisecond), nil)
	select {
	case <-woke:
	case <-time.After(time.Second):
		t.Fatal("the clear repeat did not wake")
	}
	select {
	case p := <-asked:
		t.Errorf("the repeat was asked about too, at %v", p)
	case <-time.After(100 * time.Millisecond):
	}
}
