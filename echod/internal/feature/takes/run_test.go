package takes

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
)

// runDevice is a device set to send to wakesmith, with a wake word loaded, and the takes it is handed.
func runDevice(t *testing.T) (*fake, *Feature, chan Capture) {
	t.Helper()
	d, f := newDevice(t)
	d.wakesmith.Store(true)
	kept := make(chan Capture, 100)
	f.SetWakeWord(func() (string, string) { return "hey_jarvis_v2", "zserge" }, func(c Capture) { kept <- c })
	return d, f, kept
}

// listening waits for the run to have taken the microphone, and hands it back.
func (d *fake) listening(t *testing.T) *fakeMic {
	t.Helper()
	select {
	case m := <-d.mics:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("the run never listened")
	}
	return nil
}

// capture is one turn of a run gap seconds apart: the gap, at whose end the beep sounds, then a capture
// with the word said after the beep's echo is over (the fake hears a chime for a second), or not at all.
func (d *fake) capture(t *testing.T, m *fakeMic, gap int, said bool) {
	t.Helper()
	gapN := max(framesIn(time.Duration(gap)*time.Second), framesIn(ownHeard))
	if !d.say(m, room, gapN) {
		t.Fatal("the run stopped in the gap")
	}
	listenN := framesIn(runListen)
	if !said {
		if !d.say(m, room, listenN) {
			t.Fatal("the run stopped in a capture")
		}
		return
	}
	if !d.say(m, room, 70) || !d.say(m, word, 25) || !d.say(m, room, listenN-95) {
		t.Fatal("the run stopped in a capture")
	}
}

// took waits for a take handed on.
func took(t *testing.T, kept chan Capture) Capture {
	t.Helper()
	select {
	case c := <-kept:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("no take was handed on")
	}
	return Capture{}
}

// A run is asked for only with the switch on, Send wake clips to set to wakesmith, and a wake word
// loaded, and says plainly which is missing.
func TestARunNeedsTheSwitchWakesmithAndAWakeWord(t *testing.T) {
	d, f := newDevice(t)
	if b := f.RunBlocked(); b != RunNotWakesmith {
		t.Errorf("blocked %v, want not wakesmith", b)
	}
	if err := f.StartRun(20, 3); err == nil || err.Error() != "Set Send wake clips to wakesmith first" {
		t.Errorf("started with no wakesmith: %v", err)
	}
	if r := f.LastRun(); !r.Refused {
		t.Error("the refusal was not kept for the screen to say")
	}
	d.wakesmith.Store(true)
	if b := f.RunBlocked(); b != RunNoWakeWord {
		t.Errorf("blocked %v with no wake word, want none loaded", b)
	}
	f.SetWakeWord(func() (string, string) { return "", "" }, func(Capture) {})
	if b := f.RunBlocked(); b != RunNoWakeWord {
		t.Errorf("blocked %v with an empty wake word", b)
	}
	f.SetWakeWord(func() (string, string) { return "okay_nabu", "tflm" }, func(Capture) {})
	if b := f.RunBlocked(); b != RunReady {
		t.Errorf("blocked %v with everything set", b)
	}
	d.on.Store(false)
	if err := f.ArmRun(20, 3, "browser"); !errors.Is(err, errOff) && (err == nil || err.Error() != errOff.Error()) {
		t.Errorf("asked with the switch off: %v", err)
	}
	if f.Card().Up() || f.Holding() {
		t.Error("a refused run went on anyway")
	}
}

// Counts and gaps outside what a run takes are brought inside it, and the last asked for is the next
// one's start.
func TestARunsCountAndGapAreKeptInRange(t *testing.T) {
	_, f, _ := runDevice(t)
	if c, g := f.RunPlan(); c != RunCountDefault || g != RunGapDefault {
		t.Errorf("starts from %d, %d", c, g)
	}
	if c, g := ClampRun(1, 99); c != RunCountMin || g != RunGapMax {
		t.Errorf("clamped to %d, %d", c, g)
	}
	f.SetRunPlan(500, 0)
	if c, g := f.RunPlan(); c != RunCountMax || g != RunGapMin {
		t.Errorf("plan %d, %d", c, g)
	}
}

// A run started on the screen beeps before each capture, hands on one take for each, numbered from 1 and
// all of one run, asks again with the same number when it heard none, holds the microphones from start
// to end so the word wakes nothing, and ends by itself when it has them all.
func TestARunKeepsOneTakeAfterEachBeepAndAsksAgainForAMiss(t *testing.T) {
	d, f, kept := runDevice(t)
	if err := f.StartRun(5, 2); err != nil {
		t.Fatal(err)
	}
	m := d.listening(t)
	if c := f.Card(); !c.Up() || !c.Run || c.Take != 1 || c.Of != 5 || c.Phase != Recording {
		t.Errorf("the screen shows %+v", c)
	}
	frame := frameOf(config.Get().Microphone)
	var run string
	for i, said := range []bool{true, false, true, true, true, true} {
		d.capture(t, m, 2, said)
		if !f.Holding() {
			t.Fatalf("capture %d: the microphones were let go during the run", i+1)
		}
		if !said {
			deadline := time.Now().Add(5 * time.Second)
			for c := f.Card(); !c.Missed || c.Take != 2; c = f.Card() {
				if time.Now().After(deadline) {
					t.Fatalf("a capture with nothing said did not ask for take 2 again: %+v", c)
				}
				time.Sleep(5 * time.Millisecond)
			}
			continue
		}
		c := took(t, kept)
		want := 1 + i
		if i > 1 {
			want = i // the miss is asked for again under its own number
		}
		if c.Index != want || c.Word != "hey_jarvis_v2" || c.Front != "zserge" || c.Frame != frame {
			t.Errorf("take %+v, want index %d", Capture{Run: c.Run, Index: c.Index, Word: c.Word, Front: c.Front, Frame: c.Frame}, want)
		}
		if run == "" {
			run = c.Run
		}
		if c.Run != run || len(c.Run) != 12 {
			t.Errorf("take %d is of run %q, the first of %q", c.Index, c.Run, run)
		}
		// The word with up to its padding either side (the smoothing widens it by a frame or two), and
		// never the beep.
		frames := len(c.PCM) / mic.FrameSamples
		if frames < 25 || frames > 25+2*framesIn(pad)+4 {
			t.Errorf("take %d is %d frames long", c.Index, frames)
		}
	}
	st := settled(t, f, Idle)
	if r := f.LastRun(); r.Kept != 5 || r.Of != 5 || r.Refused {
		t.Errorf("the run ended as %+v (why %q)", r, st.Why)
	}
	stopped(t, m)
	select {
	case c := <-kept:
		t.Errorf("a take too many: %d", c.Index)
	default:
	}
	// The start, a beep for each of the six captures, and the stop.
	chimed(t, d, 8)
	if !f.Holding() {
		t.Error("the microphones were let go with no tail")
	}
}

// A capture with nothing said in it is never handed on, and three in a row end the run.
func TestARunThatHearsNothingThreeTimesEnds(t *testing.T) {
	d, f, kept := runDevice(t)
	if err := f.StartRun(5, 2); err != nil {
		t.Fatal(err)
	}
	m := d.listening(t)
	for range runMisses {
		d.capture(t, m, 2, false)
	}
	settled(t, f, Idle)
	stopped(t, m)
	if r := f.LastRun(); r.Kept != 0 || !strings.Contains(r.Why, "wasn't heard 3 times") {
		t.Errorf("ended as %+v", r)
	}
	select {
	case c := <-kept:
		t.Errorf("a silent capture was handed on: %+v", c.Index)
	default:
	}
}

// Stop ends a run where it is, with the takes so far on their way.
func TestStopEndsARun(t *testing.T) {
	d, f, kept := runDevice(t)
	if err := f.StartRun(10, 3); err != nil {
		t.Fatal(err)
	}
	m := d.listening(t)
	d.capture(t, m, 3, true)
	took(t, kept)
	f.Stop()
	settled(t, f, Idle)
	stopped(t, m)
	if r := f.LastRun(); r.Kept != 1 || r.Of != 10 || !strings.Contains(r.Why, "stopped after 1 of 10") {
		t.Errorf("ended as %+v", r)
	}
}

// Asked for on the setup page, a run waits for the press like any recording, with a Start card that
// says it is a run; the press starts it.
func TestASetupPageRunWaitsForThePress(t *testing.T) {
	d, f, _ := runDevice(t)
	if err := f.ArmRun(20, 3, "browser"); err != nil {
		t.Fatal(err)
	}
	if c := f.Card(); c.Phase != Armed || !c.Run || c.Of != 20 || c.Gap != 3*time.Second {
		t.Errorf("the screen was asked %+v", c)
	}
	if f.Holding() {
		t.Error("the microphones were taken before the press")
	}
	if st := f.State("browser"); !st.Run || !st.Mine || st.Of != 20 {
		t.Errorf("the page sees %+v", st)
	}
	if !f.Begin(0) {
		t.Fatal("the press did not start it")
	}
	d.listening(t)
	if c := f.Card(); c.Phase != Recording || !c.Run || c.Take != 1 {
		t.Errorf("the screen shows %+v", c)
	}
	f.Discard("thrown away on the setup page")
	settled(t, f, Idle)
}

// The setup page closing throws away what it asked for, but not a run started on the screen.
func TestTheSetupPageClosingLeavesAScreenRun(t *testing.T) {
	d, f, _ := runDevice(t)
	if err := f.StartRun(5, 2); err != nil {
		t.Fatal(err)
	}
	m := d.listening(t)
	f.DiscardPage("the setup page closed")
	if st := f.State(""); st.Phase != Recording {
		t.Fatalf("the page closing ended the screen's run: %v", st.Phase)
	}
	if st := f.State("browser"); st.Mine || !st.Run {
		t.Errorf("a browser sees the screen's run as %+v", st)
	}
	f.Stop()
	settled(t, f, Idle)
	stopped(t, m)

	if err := f.ArmRun(5, 2, "browser"); err != nil {
		t.Fatal(err)
	}
	f.DiscardPage("the setup page closed")
	if st := settled(t, f, Idle); st.Why != "the setup page closed" {
		t.Errorf("the page's own run ended saying %q", st.Why)
	}
}

// A take is the word said after the beep: not the beep, not a word said over its echo, and not talk
// before it.
func TestATakeIsTheWordAfterTheBeep(t *testing.T) {
	capture := func(beepAt, echo int, words ...[2]int) []heard {
		c := make([]heard, beepAt+framesIn(runListen))
		for i := range c {
			c[i] = heard{pcm: frame(room, i), level: room, over: i >= beepAt && i < beepAt+echo}
		}
		for _, w := range words {
			for i := w[0]; i < w[0]+w[1]; i++ {
				c[i] = heard{pcm: frame(word, i), level: word, over: c[i].over}
			}
		}
		return c
	}
	pre, echo := framesIn(runPreRoll), framesIn(runEcho)
	if _, ok := pickTake(capture(pre, echo), pre); ok {
		t.Error("a take in a capture with nothing said")
	}
	if pcm, ok := pickTake(capture(pre, echo, [2]int{pre + echo + 15, 25}), pre); !ok {
		t.Error("no take of a word said just after the beep")
	} else if n := len(pcm) / mic.FrameSamples; n < 25 || n > 25+2*framesIn(pad)+4 {
		t.Errorf("the take is %d frames", n)
	}
	if _, ok := pickTake(capture(pre, echo, [2]int{pre + echo - 5, 25}), pre); ok {
		t.Error("a word said over the beep's echo was taken")
	}
	if _, ok := pickTake(capture(pre, echo, [2]int{15, 25}), pre); ok {
		t.Error("talk before the beep was taken")
	}
	// Talk before the beep and the word after: the word.
	if pcm, ok := pickTake(capture(pre, echo, [2]int{15, 25}, [2]int{pre + echo + 20, 30}), pre); !ok {
		t.Error("the word after talk before the beep was not taken")
	} else if n := len(pcm) / mic.FrameSamples; n < 30 {
		t.Errorf("took %d frames, not the word after the beep", n)
	}
}
