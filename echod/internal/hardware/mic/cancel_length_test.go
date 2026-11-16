package mic

import "testing"

// The filter is as long as the output asks for when a run starts, and stays that long until the next
// run: the jack's long path gets cancelTapsJack on the builds with longJack and cancelTaps on the
// others, the speaker cancelTaps, and a filter told to stop learning is still told so after it is
// rebuilt.
func TestCancellerLengthFollowsTheOutput(t *testing.T) {
	jackTaps := cancelTaps
	if longJack {
		jackTaps = cancelTapsJack
	}

	jack := false
	LongEchoPath = func() bool { return jack }
	t.Cleanup(func() { LongEchoPath = nil })

	c := newCanceller()
	if c == nil {
		t.Fatal("no canceller")
	}
	n := 0
	feed := func(playing bool, frames int) {
		for range frames {
			raw := rawFrame(n, playing)
			c.apply(raw, Average{}.Mix(Decode(raw)))
			n++
		}
	}
	// Enough silence to let the hold run out, so the next playback starts a run.
	quiet := refHold/FrameSamples + 2

	feed(true, 3)
	if got := c.filter.Load().Taps(); got != cancelTaps {
		t.Fatalf("speaker: %d taps, want %d", got, cancelTaps)
	}

	jack = true
	feed(true, 3)
	if got := c.filter.Load().Taps(); got != cancelTaps {
		t.Fatalf("the output changed in the middle of a run: %d taps, want the run's %d", got, cancelTaps)
	}

	c.adapting.Store(false)
	c.filter.Load().SetAdapting(false)
	feed(false, quiet)
	feed(true, 3)
	f := c.filter.Load()
	if got := f.Taps(); got != jackTaps {
		t.Fatalf("jack: %d taps, want %d", got, jackTaps)
	}
	if f.Adapting() {
		t.Error("the rebuilt filter learns, but learning was off")
	}

	jack = false
	feed(false, quiet)
	feed(true, 3)
	if got := c.filter.Load().Taps(); got != cancelTaps {
		t.Errorf("back on the speaker: %d taps, want %d", got, cancelTaps)
	}
}
