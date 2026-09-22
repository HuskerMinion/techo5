package detect

import "testing"

// Going quiet drops the engines and coming back builds them again.
//
// A cut microphone hands on silence, so nothing can be detected in it — but the models were run
// over that silence anyway, frame after frame, which on a Dot is a third of the time it has. The
// slots are kept either way: what goes is the work, not the choice of wake word.
func TestGoingQuietDropsTheEngines(t *testing.T) {
	e := New(2, nil)

	e.Quiet(true)
	if got := len(e.backends); got != 0 {
		t.Errorf("%d engines still up while muted, want none", got)
	}
	if !e.quiet {
		t.Error("not quiet after being told to be")
	}

	e.Quiet(false)
	if e.quiet {
		t.Error("still quiet after coming back")
	}
}

// Saying it twice is not two of anything: mute state arrives from a button, a switch and a
// restore, and they do not agree about how many times they should say so.
func TestSayingItTwice(t *testing.T) {
	e := New(2, nil)

	e.Quiet(true)
	e.Quiet(true)
	if !e.quiet {
		t.Error("lost the quiet on the second telling")
	}

	e.Quiet(false)
	e.Quiet(false)
	if e.quiet {
		t.Error("went quiet again on being told to come back twice")
	}
}

// Nothing is scored while quiet. This is the whole point: the frames are silence, and running the
// models over them is work that cannot find anything.
func TestNothingIsScoredWhileQuiet(t *testing.T) {
	e := New(2, nil)
	e.Quiet(true)

	// A frame of silence, which is what a cut microphone produces.
	e.score(make([]int16, 320), nil)

	if len(e.backends) != 0 {
		t.Error("scoring while quiet built an engine")
	}
}
