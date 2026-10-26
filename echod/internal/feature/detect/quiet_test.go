package detect

import (
	"reflect"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// Going quiet drops the engines and coming back builds them again.
//
// A cut microphone hands on silence, so nothing can be detected in it — but the models were run
// over that silence anyway, frame after frame, which on a Dot is a third of the time it has. The
// slots are kept either way: what goes is the work, not the choice of wake word.
func TestGoingQuietDropsTheEngines(t *testing.T) {
	made := fakes(t)
	e := New(2, nil)

	if err := e.Use(0, model("hey_jarvis", wake.KindMicroWakeWord)); err != nil {
		t.Fatalf("Use(0): %v", err)
	}
	if len(e.backends) != 1 {
		t.Fatalf("%d engines with a slot loaded, want 1", len(e.backends))
	}

	e.Quiet(true)
	if got := len(e.backends); got != 0 {
		t.Errorf("%d engines still up while muted, want none", got)
	}
	if !made[wake.KindMicroWakeWord].closed {
		t.Error("the engine was dropped without being closed")
	}
	if !e.quiet {
		t.Error("not quiet after being told to be")
	}
	if !e.slots[0].loaded {
		t.Error("the slot was emptied; going quiet should keep the choice of wake word")
	}

	e.Quiet(false)
	if e.quiet {
		t.Error("still quiet after coming back")
	}
	if len(e.backends) != 1 {
		t.Errorf("%d engines after coming back, want the slot's one", len(e.backends))
	}
	if _, ok := made[wake.KindMicroWakeWord].loaded["hey_jarvis"]; !ok {
		t.Error("the wake word was not loaded back into the engine")
	}
}

// Saying it twice is not two of anything: mute state arrives from a button, a switch and a
// restore, and they do not agree about how many times they should say so.
func TestSayingItTwice(t *testing.T) {
	fakes(t)
	e := New(2, nil)

	if err := e.Use(0, model("hey_jarvis", wake.KindMicroWakeWord)); err != nil {
		t.Fatalf("Use(0): %v", err)
	}

	e.Quiet(true)
	e.Quiet(true)
	if !e.quiet {
		t.Error("lost the quiet on the second telling")
	}
	if got := len(e.backends); got != 0 {
		t.Errorf("%d engines after being told twice, want none", got)
	}

	e.Quiet(false)
	e.Quiet(false)
	if e.quiet {
		t.Error("went quiet again on being told to come back twice")
	}
	if got := len(e.backends); got != 1 {
		t.Errorf("%d engines after coming back twice, want the slot's one", got)
	}
}

// Nothing is scored while quiet. This is the whole point: the frames are silence, and running the
// models over them is work that cannot find anything.
func TestNothingIsScoredWhileQuiet(t *testing.T) {
	made := fakes(t)
	e := New(2, nil)

	if err := e.Use(0, model("hey_jarvis", wake.KindMicroWakeWord)); err != nil {
		t.Fatalf("Use(0): %v", err)
	}
	f := made[wake.KindMicroWakeWord]

	// Over its threshold, so a frame that reached the engine would fire the slot.
	f.loaded["hey_jarvis"] = 0.99
	e.OnDetect = func(int) { t.Error("a slot fired while the microphones were cut") }

	e.Quiet(true)

	// A frame of silence, which is what a cut microphone produces.
	e.score(make([]int16, 320), nil)

	if f.fed != 0 {
		t.Errorf("the engine was fed %d frames while quiet, want none", f.fed)
	}
	if len(e.backends) != 0 {
		t.Error("scoring while quiet built an engine")
	}
}

// A backend rebuilt after mute has no scores yet. Its slot must not retain an
// utterance, readiness, or refractory period from the backend that was closed.
func TestQuietClearsUtteranceState(t *testing.T) {
	fakes(t)
	e := New(2, nil)
	m := model("test_word", wake.KindMicroWakeWord)
	if err := e.Use(0, m); err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	e.slots[0] = slot{model: m, loaded: true, scored: true, quiet: at.Add(time.Hour),
		peak: 0.9, peakAt: at, suppressed: 5, holding: true, holdEnds: at.Add(Hold),
		crossing: 0.8, crossCutoff: 0.7, candidate: true, candidateAt: at,
		candidatePeak: 0.6, askedUntil: at.Add(Refractory)}
	e.Quiet(true)
	if got, want := e.slots[0], (slot{model: m, loaded: true}); !reflect.DeepEqual(got, want) {
		t.Errorf("muted slot retains utterance state: %+v", got)
	}
	if e.Ready(0) {
		t.Error("closed model is still ready")
	}
	// The unmute protection is a new hold, installed after Quiet(false).
	e.Quiet(false)
	if e.Ready(0) {
		t.Error("reloaded model is ready before scoring")
	}
	e.Settle(time.Second)
	if !e.slots[0].quiet.After(at) {
		t.Error("unmute settling was lost")
	}
	if got, loaded := e.Loaded(0); !loaded || !reflect.DeepEqual(got, m) {
		t.Error("model choice was lost")
	}
}

type warmingAfterQuiet struct {
	fake
	frames int
}

func (f *warmingAfterQuiet) feed(frame []int16) (map[string]float64, bool) {
	f.frames++
	if f.frames < 4 {
		return nil, false
	}
	return f.loaded, true
}

func TestQuietReadinessWaitsForNewScores(t *testing.T) {
	was := build
	build = func(wake.Kind) (backend, error) {
		return &warmingAfterQuiet{fake: fake{loaded: map[string]float64{}}}, nil
	}
	t.Cleanup(func() { build = was })
	e := New(1, nil)
	if err := e.Use(0, model("test_word", wake.KindMicroWakeWord)); err != nil {
		t.Fatal(err)
	}
	ready := make(chan int, 2)
	e.OnReady = func(n int) { ready <- n }
	for range 4 {
		e.score([]int16{0}, nil)
	}
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("initial score did not report ready")
	}
	e.Quiet(true)
	e.Quiet(false)
	if e.Ready(0) {
		t.Error("old readiness survived reload")
	}
	for range 3 {
		e.score([]int16{0}, nil)
		if e.Ready(0) {
			t.Error("ready before new model's first score")
		}
	}
	e.score([]int16{0}, nil)
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("reloaded model did not report ready")
	}
	if !e.Ready(0) {
		t.Error("not ready after new score")
	}
}
