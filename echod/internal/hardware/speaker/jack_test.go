package speaker

import (
	"reflect"
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
	// So does a sound that came to want the speaker, and a wake that got there first.
	p := &Player{out: OutputHeadphone, bell: []int16{1}}
	p.SetJackSleep(true)
	p.jackIdle.state = ampSleeping
	p.jackDown()
	if !p.jackIdle.is(ampAwake) {
		t.Error("the jack went to sleep under a sound")
	}
	p = &Player{out: OutputHeadphone}
	p.SetJackSleep(true)
	p.jackIdle.state = ampWaking
	p.jackDown()
	if !p.jackIdle.is(ampAwake) {
		t.Error("a sleep overtaken by a wake took the jack down")
	}
}

// Going down writes the headphone path's off sequence, and coming back writes the path itself.
func TestJackWritesItsOwnSequences(t *testing.T) {
	var wrote [][]kctl
	was := gateWrite
	gateWrite = func(_ *Player, seq []kctl) bool { wrote = append(wrote, seq); return true }
	t.Cleanup(func() { gateWrite = was })
	p := &Player{out: OutputHeadphone}
	p.SetJackSleep(true)
	p.jackIdle.state = ampSleeping
	p.jackDown()
	if !p.jackIdle.is(ampAsleep) {
		t.Fatal("the jack did not go to sleep")
	}
	p.jackUp()
	if len(wrote) != 2 || !reflect.DeepEqual(wrote[0], headphoneOff) || !reflect.DeepEqual(wrote[1], p.path(OutputHeadphone)) {
		t.Errorf("wrote %v, want the off sequence and then the headphone path", wrote)
	}
}

// A sleep whose writes fail counts as asleep, so the next sound's wake writes the whole path back. A
// wake whose writes fail leaves the jack asleep and the sound held, to be tried again; after
// wakeTries in a row the sound is let out anyway.
func TestJackWritesThatFail(t *testing.T) {
	writesWork(t, false)
	p := &Player{out: OutputHeadphone}
	p.SetJackSleep(true)
	p.jackIdle.state = ampSleeping
	p.jackDown()
	if !p.jackIdle.is(ampAsleep) {
		t.Fatal("a sleep whose writes failed does not count as asleep")
	}
	for i := 1; i < wakeTries; i++ {
		p.jackUp()
		if !p.jackIdle.is(ampAsleep) {
			t.Fatalf("failed wake %d let the sound out", i)
		}
	}
	p.jackUp()
	if !p.jackIdle.is(ampAwake) || p.jackWakeFails.Load() != 0 {
		t.Errorf("after %d failed wakes the sound is still held (fails %d)", wakeTries, p.jackWakeFails.Load())
	}
}

// A sound queued for a jack asleep or coming back starts jackSettle late, and one for a jack that is
// up, or with the switch off, does not.
func TestWakeDelay(t *testing.T) {
	if !HasJack {
		t.Skip("no jack on this build")
	}
	p := &Player{out: OutputHeadphone}
	p.SetJackSleep(true)
	if d := p.WakeDelay(); d != 0 {
		t.Errorf("a jack that is up delays a sound by %v", d)
	}
	p.jackIdle.settled(ampAsleep)
	if d := p.WakeDelay(); d != jackSettle {
		t.Errorf("a sleeping jack delays a sound by %v, want %v", d, jackSettle)
	}
	p.SetJackSleep(false)
	if d := p.WakeDelay(); d != 0 {
		t.Errorf("with the switch off a sound is delayed by %v", d)
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
