package detect

import (
	"testing"

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
