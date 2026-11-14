package speaker

import (
	"testing"
	"time"
)

// With the switch on and the jack the output, the headphone path comes down once nothing has wanted
// the speaker for idleAfter; a sound then wakes it and is held until the path is back.
func TestJackSleepsAndWakes(t *testing.T) {
	writesWork(t, true)
	p := &Player{out: OutputHeadphone}
	p.outNow.Store(OutputHeadphone)
	p.SetJackSleep(true)
	p.jackIdle.since = time.Now().Add(-2 * idleAfter)
	if p.jackStep() {
		t.Fatal("going down held the audio")
	}
	waitJack(t, p, ampAsleep)

	p.pending = []int16{1, 1}
	if !p.jackStep() {
		t.Fatal("a sound for a sleeping jack was not held")
	}
	deadline := time.Now().Add(2 * time.Second)
	for p.jackStep() {
		if time.Now().After(deadline) {
			t.Fatal("the jack never woke")
		}
		time.Sleep(time.Millisecond)
	}
}

// The switch off, or another output, keeps the jack up, and wakes one asleep.
func TestJackStaysUpUnlessAsked(t *testing.T) {
	writesWork(t, true)
	for _, c := range []struct {
		on  bool
		out Output
	}{{false, OutputHeadphone}, {true, OutputSpeaker}, {true, OutputBoth}} {
		p := &Player{out: c.out}
		p.outNow.Store(c.out)
		p.SetJackSleep(c.on)
		p.jackIdle.since = time.Now().Add(-2 * idleAfter)
		p.jackStep()
		if !p.jackIdle.is(ampAwake) {
			t.Errorf("switch %v on %s: the jack went to sleep", c.on, c.out)
		}
		p.jackIdle.settled(ampAsleep)
		p.jackStep()
		waitJack(t, p, ampAwake)
	}
}

// A sleep overtaken while it waited, by the output moving or the switch going off, leaves the jack up.
func TestJackDownOvertaken(t *testing.T) {
	writesWork(t, true)
	for _, p := range []*Player{{out: OutputSpeaker}, {out: OutputHeadphone, draining: true}, {out: OutputHeadphone}} {
		p.SetJackSleep(p.out == OutputSpeaker || p.draining)
		p.jackIdle.state = ampSleeping
		p.jackDown()
		if !p.jackIdle.is(ampAwake) {
			t.Errorf("out %s, draining %v: the jack went to sleep", p.out, p.draining)
		}
	}
}

// A path written whole leaves the jack awake, so a later wake does not write it over a new output.
func TestAPathWrittenWakesTheJack(t *testing.T) {
	p := &Player{}
	p.jackIdle.settled(ampAsleep)
	p.jackAwake()
	if !p.jackIdle.is(ampAwake) {
		t.Error("the jack still counts as asleep after its path was written")
	}
}

func waitJack(t *testing.T, p *Player, s ampState) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !p.jackIdle.is(s) {
		if time.Now().After(deadline) {
			t.Fatalf("the jack never reached %v", s)
		}
		time.Sleep(time.Millisecond)
	}
}
