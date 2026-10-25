package speaker

import (
	"testing"
	"time"
)

// The checkers gate as idle.go uses it, built here so the tests run on every build.
var testGate = &ampGate{control: "Ext_Speaker_Amp_Switch", on: "Off", off: "On", mute: "OUT Playback Switch"}

// Going to sleep mutes the line-out before the amplifier goes off, and waking turns the amplifier on
// before the line-out is let go: the order that does not pop either way.
func TestAmpGateOrder(t *testing.T) {
	sleep := testGate.sleep()
	if len(sleep) != 2 || sleep[0].name != "OUT Playback Switch" || sleep[0].level != 0 || sleep[0].value != "" ||
		sleep[1].name != "Ext_Speaker_Amp_Switch" || sleep[1].value != "On" {
		t.Errorf("sleep: %+v", sleep)
	}
	on, unmute := testGate.wake(true)
	if len(on) != 1 || on[0].name != "Ext_Speaker_Amp_Switch" || on[0].value != "Off" {
		t.Errorf("wake, on: %+v", on)
	}
	if len(unmute) != 1 || unmute[0].name != "OUT Playback Switch" || unmute[0].level != 1 || unmute[0].value != "" {
		t.Errorf("wake, unmute: %+v", unmute)
	}
}

// On the jack the line-out's mute is the headphone path's: waking only brings the amplifier back.
func TestAmpGateWakeLeavesTheJacksMute(t *testing.T) {
	on, unmute := testGate.wake(false)
	if len(on) != 1 || len(unmute) != 0 {
		t.Errorf("wake on the jack: on %+v, unmute %+v", on, unmute)
	}
}

// The amplifier sleeps idleAfter after the speaker was last wanted, not before, and anything wanting
// it starts the count again.
func TestIdleAmpSleepsAfterIdle(t *testing.T) {
	var a idleAmp
	t0 := time.Unix(0, 0)
	if act, hold := a.step(t0, false); act != ampNone || hold {
		t.Fatalf("first idle period: %v %v", act, hold)
	}
	if act, _ := a.step(t0.Add(idleAfter-time.Millisecond), false); act != ampNone {
		t.Fatal("asleep before idleAfter")
	}
	a.step(t0.Add(idleAfter-time.Millisecond), true) // wanted again: the count starts over
	if act, _ := a.step(t0.Add(idleAfter), false); act != ampNone {
		t.Fatal("a period that wanted the speaker did not restart the count")
	}
	t1 := t0.Add(idleAfter)
	if act, hold := a.step(t1.Add(idleAfter), false); act != ampSleep || hold {
		t.Fatalf("idleAfter idle: %v %v, want a sleep with nothing held", act, hold)
	}
	if act, _ := a.step(t1.Add(2*idleAfter), false); act != ampNone {
		t.Fatal("a second sleep started while the first was in progress")
	}
}

// A sound for a sleeping amplifier wakes it and waits, period after period, until it is awake: held,
// not dropped.
func TestIdleAmpHoldsTheFirstSoundUntilAwake(t *testing.T) {
	var a idleAmp
	now := time.Unix(0, 0)
	a.settled(ampAsleep)
	if act, hold := a.step(now, false); act != ampNone || hold {
		t.Fatalf("asleep and idle: %v %v", act, hold)
	}
	if act, hold := a.step(now, true); act != ampWake || !hold {
		t.Fatalf("asleep and wanted: %v %v, want a wake with the audio held", act, hold)
	}
	for range 3 {
		if act, hold := a.step(now, true); act != ampNone || !hold {
			t.Fatalf("waking: %v %v, want the audio still held and no second wake", act, hold)
		}
	}
	a.settled(ampAwake)
	if act, hold := a.step(now, true); act != ampNone || hold {
		t.Fatalf("awake: %v %v", act, hold)
	}
}

// Audio arriving while the amplifier goes down waits for the sleep to finish and the wake after it.
func TestIdleAmpHoldsWhatArrivesWhileGoingDown(t *testing.T) {
	var a idleAmp
	a.state = ampSleeping
	if _, hold := a.step(time.Now(), false); hold {
		t.Error("going down with nothing to play held something")
	}
	if act, hold := a.step(time.Now(), true); act != ampNone || !hold {
		t.Errorf("going down with audio: %v %v", act, hold)
	}
	a.settled(ampAsleep)
	if act, hold := a.step(time.Now(), true); act != ampWake || !hold {
		t.Errorf("asleep with audio waiting: %v %v", act, hold)
	}
}

// Every lane counts as wanting the speaker, and so does a claim holding it; audio going to a sink does
// not want the codec's amplifier.
func TestPlayerWanted(t *testing.T) {
	p := &Player{}
	if p.wanted() {
		t.Fatal("an empty player wants the speaker")
	}
	p.Hold(true)
	if !p.wanted() {
		t.Error("a claim holding the speaker does not count")
	}
	p.Hold(false)
	for name, set := range map[string]func(){
		"pending": func() { p.pending = []int16{1, 1} },
		"bed":     func() { p.bed = []int16{1, 1} },
		"under":   func() { p.under = []int16{1, 1} },
		"bell":    func() { p.bell = []int16{1, 1} },
		"ringing": func() { p.ringing.Store(true) },
		"source":  func() { p.Attach(silentSource{}) },
	} {
		p.pending, p.bed, p.under, p.bell = nil, nil, nil, nil
		p.ringing.Store(false)
		p.Attach(nil)
		set()
		if !p.wanted() {
			t.Errorf("%s does not count", name)
		}
	}
	p.sink.Store(&sinkState{})
	if p.wanted() {
		t.Error("audio going to a sink wants the codec's amplifier")
	}
}

type silentSource struct{}

func (silentSource) Render(uint64, []int16) {}

// The write loop's side: a sleeping amplifier with audio queued is woken, and the audio held until the
// wake has finished; then it goes out.
func TestIdleStepWakesAndReleases(t *testing.T) {
	p := &Player{gate: testGate, out: OutputSpeaker}
	p.outNow.Store(OutputSpeaker)
	p.idle.settled(ampAsleep)
	p.pending = []int16{1, 1}
	if !p.idleStep() {
		t.Fatal("audio for a sleeping amplifier was not held")
	}
	deadline := time.Now().Add(2 * time.Second)
	for p.idleStep() {
		if time.Now().After(deadline) {
			t.Fatal("the amplifier never woke")
		}
		time.Sleep(time.Millisecond)
	}
}

// The amplifier is not put to sleep on the jack, where the line-out mute is the headphone path's, nor
// while a switch to the jack drains: it stays awake and is tried again later.
func TestAmpSleepOnlyOnTheSpeaker(t *testing.T) {
	writesWork(t, true)
	for _, c := range []struct {
		out      Output
		draining bool
	}{{OutputHeadphone, false}, {OutputBoth, false}, {OutputSpeaker, true}} {
		p := &Player{gate: testGate, out: c.out, draining: c.draining}
		p.idle.state = ampSleeping
		p.ampSleep()
		if p.idle.state != ampAwake {
			t.Errorf("%s (draining %v): the amplifier went to sleep", c.out, c.draining)
		}
	}
	p := &Player{gate: testGate, out: OutputSpeaker}
	p.idle.state = ampSleeping
	p.ampSleep()
	if p.idle.state != ampAsleep {
		t.Error("the speaker's amplifier did not go to sleep")
	}
}

// writesWork makes the gated amplifier's writes succeed, or fail, for the length of a test.
func writesWork(t *testing.T, ok bool) {
	was := gateWrite
	gateWrite = func(*Player, []kctl) bool { return ok }
	t.Cleanup(func() { gateWrite = was })
}

// A sleep overtaken while it waited, by a wake or by something wanting the speaker, leaves the
// amplifier on. One whose writes fail counts as asleep, so the next sound's wake puts back whatever
// half of it went through.
func TestAmpSleepOvertaken(t *testing.T) {
	writesWork(t, true)
	p := &Player{gate: testGate, out: OutputSpeaker}
	p.idle.state = ampAwake // a wake got there first
	p.ampSleep()
	if p.idle.state != ampAwake {
		t.Error("a sleep after a wake put the amplifier down")
	}
	p.idle.state = ampSleeping
	p.bell = []int16{1}
	p.ampSleep()
	if p.idle.state != ampAwake {
		t.Error("the amplifier went to sleep under a ring")
	}
	writesWork(t, false)
	p = &Player{gate: testGate, out: OutputSpeaker}
	p.idle.state = ampSleeping
	p.ampSleep()
	if p.idle.state != ampAsleep {
		t.Error("a sleep that failed counts as awake: half of it would never be undone")
	}
}

// A wake whose writes fail leaves the amplifier asleep, so the next period tries again with the audio
// held, until wakeTries have failed; then the audio is let out anyway.
func TestAmpWakeFails(t *testing.T) {
	writesWork(t, false)
	p := &Player{gate: testGate, out: OutputHeadphone}
	for i := 1; i < wakeTries; i++ {
		p.idle.state = ampWaking
		p.ampWake()
		if p.idle.state != ampAsleep {
			t.Fatalf("wake %d failed and counted as awake", i)
		}
	}
	p.ampWake()
	if p.idle.state != ampAwake {
		t.Error("the audio was held for good")
	}
}
