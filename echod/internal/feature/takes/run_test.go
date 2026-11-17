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
	if r := f.LastRefusal(); !r.Refused {
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

// Start on the device's own screen is the consent: a run starts there with Record takes of a word off,
// and the switch does not end it. Asked for on the setup page, the switch is needed, as for any
// recording a browser asks for.
func TestTheScreensStartNeedsNoSwitchAndThePagesDoes(t *testing.T) {
	d, f, kept := runDevice(t)
	d.on.Store(false)
	if b := f.RunBlocked(); b != RunReady {
		t.Errorf("the screen is blocked %v with the switch off", b)
	}
	if b := f.PageRunBlocked(); b != RunSwitchOff {
		t.Errorf("the page is blocked %v with the switch off", b)
	}
	if err := f.ArmRun(5, 2, "browser"); err == nil || err.Error() != errOff.Error() {
		t.Errorf("the page asked with the switch off: %v", err)
	}
	if err := f.StartRun(5, 2); err != nil {
		t.Fatalf("the screen's Start with the switch off: %v", err)
	}
	m := d.listening(t)
	f.poke <- struct{}{}
	d.capture(t, m, 2, true)
	took(t, kept)
	if st := f.State(""); st.Phase != Recording {
		t.Fatalf("the run ended with the switch off: %v", st.Phase)
	}
	f.DiscardPage("Record takes of a word was switched off") // what the switch going off does
	if st := f.State(""); st.Phase != Recording {
		t.Fatal("the switch going off ended the screen's run")
	}
	f.Stop()
	settled(t, f, Idle)
}

// A device whose clock is not set yet stamps nothing 1970: no run starts until it is.
func TestARunWaitsForTheClock(t *testing.T) {
	d, f, _ := runDevice(t)
	d.mu.Lock()
	d.clock = time.Unix(600, 0)
	d.mu.Unlock()
	if b := f.RunBlocked(); b != RunClockNotSet {
		t.Errorf("blocked %v with the clock at 1970", b)
	}
	if err := f.StartRun(5, 2); err == nil || err.Error() != "The clock isn't set yet" {
		t.Errorf("started with no clock: %v", err)
	}
}

// Takes of a word kept for the setup page to download are not let go of by a run: it waits until they
// are downloaded or thrown away, and says so.
func TestARunLeavesKeptTakesAlone(t *testing.T) {
	d, f, _ := runDevice(t)
	m := d.recording(t, f, "Hey Jarvis")
	d.takesOf(t, m, 2)
	f.Stop()
	settled(t, f, Kept)
	if b := f.RunBlocked(); b != RunTakesKept {
		t.Errorf("blocked %v with takes kept", b)
	}
	if err := f.ArmRun(5, 2, "browser"); err == nil {
		t.Error("a run was asked for over the kept takes")
	}
	if err := f.StartRun(5, 2); err == nil {
		t.Error("a run started over the kept takes")
	}
	if st := f.State("browser"); st.Phase != Kept || st.Takes != 2 {
		t.Errorf("the kept takes are now %+v", st)
	}
}

// A Start refused while a run goes on is kept apart, and says nothing about the run.
func TestAStartRefusedDuringARunLeavesItsResult(t *testing.T) {
	d, f, kept := runDevice(t)
	if err := f.StartRun(5, 2); err != nil {
		t.Fatal(err)
	}
	m := d.listening(t)
	d.capture(t, m, 2, true)
	took(t, kept)
	if err := f.StartRun(5, 2); err == nil {
		t.Fatal("a second run started over the first")
	}
	if r := f.LastRun(); r.Refused || r.Of != 0 {
		t.Errorf("the refusal became the run's result: %+v", r)
	}
	if r := f.LastRefusal(); !r.Refused || r.Why == "" {
		t.Errorf("the refusal was not kept: %+v", r)
	}
	f.Stop()
	settled(t, f, Idle)
	if r := f.LastRun(); r.Kept != 1 || r.Of != 5 || r.Refused {
		t.Errorf("the run ended as %+v", r)
	}
}

// RunBlocked reads the copy of the wake word, taken again only when it changes.
func TestRunBlockedReadsTheCopyOfTheWord(t *testing.T) {
	_, f, _ := runDevice(t)
	calls := 0
	word := "hey_jarvis_v2"
	f.SetWakeWord(func() (string, string) { calls++; return word, "tflm" }, func(Capture) {})
	calls = 0
	for range 10 {
		f.RunBlocked()
	}
	if calls != 0 {
		t.Errorf("RunBlocked asked for the word %d times", calls)
	}
	word = ""
	f.WakeWordChanged()
	if b := f.RunBlocked(); b != RunNoWakeWord {
		t.Errorf("blocked %v once the word went", b)
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

// A word said promptly after the beep is kept, though the speaker goes on saying it is playing for a
// second after the note (the fake's chime is heard for a second): from the beep on, only the beep and
// its echo (runEcho) count as the device's own sound.
func TestAWordSaidPromptlyAfterTheBeepIsKept(t *testing.T) {
	d, f, kept := runDevice(t)
	if err := f.StartRun(5, 2); err != nil {
		t.Fatal(err)
	}
	m := d.listening(t)
	if !d.say(m, room, framesIn(2*time.Second)) {
		t.Fatal("the run stopped in the gap")
	}
	// The beep has sounded; the speaker says it is playing for the next second.
	chimed(t, d, 2) // the start note and the beep
	if !d.audibleNow() {
		t.Fatal("the fake does not hear the beep: this test would prove nothing")
	}
	after := framesIn(300 * time.Millisecond)
	if !d.say(m, room, after) || !d.say(m, word, 25) || !d.say(m, room, framesIn(runListen)-after-25) {
		t.Fatal("the run stopped in the capture")
	}
	c := took(t, kept)
	if n := len(c.PCM) / mic.FrameSamples; c.Index != 1 || n < 25 {
		t.Errorf("took take %d of %d frames, want the word said 0.3 s after the beep", c.Index, n)
	}
	f.Stop()
	settled(t, f, Idle)
}

// Something else played during a capture (an announcement, a reminder, a chime, the music) is the
// device's own sound and is never kept as a take, a second after the beep or any time after it.
func TestOtherPlaybackDuringACaptureIsNotATake(t *testing.T) {
	d, f, kept := runDevice(t)
	if err := f.StartRun(5, 2); err != nil {
		t.Fatal(err)
	}
	m := d.listening(t)
	if !d.say(m, room, framesIn(2*time.Second)) {
		t.Fatal("the run stopped in the gap")
	}
	chimed(t, d, 2)
	second := framesIn(time.Second)
	if !d.say(m, room, second) {
		t.Fatal("the run stopped in the capture")
	}
	d.playing.Store(true) // a reminder, word-length and loud, a second after the beep
	ok := d.say(m, word, 25)
	d.playing.Store(false)
	if !ok || !d.say(m, room, framesIn(runListen)-second-25) {
		t.Fatal("the run stopped in the capture")
	}
	deadline := time.Now().Add(5 * time.Second)
	for c := f.Card(); !c.Missed; c = f.Card() {
		if time.Now().After(deadline) {
			t.Fatalf("the capture with only playback in it was not a miss: %+v", c)
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case c := <-kept:
		t.Errorf("playback was kept as take %d", c.Index)
	default:
	}
	f.Stop()
	settled(t, f, Idle)
}

// A run asked for on the setup page goes on when the page closes by itself while it runs: it was
// started at the device and its takes go to wakesmith. One still waiting for the press goes.
func TestASetupPageRunOutlivesThePage(t *testing.T) {
	d, f, _ := runDevice(t)
	if err := f.ArmRun(5, 2, "browser"); err != nil {
		t.Fatal(err)
	}
	if !f.Begin(0) {
		t.Fatal("the press did not start it")
	}
	m := d.listening(t)
	f.DiscardPage("the setup page closed")
	if st := f.State("browser"); st.Phase != Recording {
		t.Fatalf("the page closing ended its run: %v", st.Phase)
	}
	f.Stop()
	settled(t, f, Idle)
	stopped(t, m)

	if err := f.ArmRun(5, 2, "browser"); err != nil {
		t.Fatal(err)
	}
	f.DiscardPage("the setup page closed")
	if st := settled(t, f, Idle); st.Why != "the setup page closed" {
		t.Errorf("a run waiting for the press ended saying %q", st.Why)
	}
}

// A run ends when its takes would have nowhere to go: Send wake clips to moving off wakesmith, or to
// another address; and when Record takes of a word goes off, however it was started.
func TestARunEndsWhenItsTakesLoseTheirWay(t *testing.T) {
	for _, c := range []struct {
		name, says string
		change     func(d *fake)
	}{
		{"off wakesmith", "no longer goes to that wakesmith", func(d *fake) { d.wakesmith.Store(false) }},
		{"another address", "no longer goes to that wakesmith", func(d *fake) {
			a := "http://other.test:8765"
			d.address.Store(&a)
		}},
		{"the switch off", "switched off", func(d *fake) { d.on.Store(false) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, f, _ := runDevice(t)
			if err := f.StartRun(5, 2); err != nil { // the switch is on: it going off ends even this one
				t.Fatal(err)
			}
			m := d.listening(t)
			c.change(d)
			f.poke <- struct{}{}
			stopped(t, m)
			settled(t, f, Idle)
			if r := f.LastRun(); !strings.Contains(r.Why, c.says) {
				t.Errorf("ended saying %q", r.Why)
			}
		})
	}
}

// A ring ends a run with no stop note over the alarm.
func TestARingEndsARunWithoutTheStopNote(t *testing.T) {
	d, f, _ := runDevice(t)
	if err := f.StartRun(5, 2); err != nil {
		t.Fatal(err)
	}
	m := d.listening(t)
	chimed(t, d, 1) // the start note
	d.ringing.Store(true)
	f.poke <- struct{}{}
	stopped(t, m)
	settled(t, f, Idle)
	if r := f.LastRun(); !strings.Contains(r.Why, "rang") {
		t.Errorf("ended saying %q", r.Why)
	}
	time.Sleep(20 * time.Millisecond)
	if n := d.chimes.Load(); n != 1 {
		t.Errorf("%d notes, want only the start note: the stop note played over the ring", n)
	}
}

// Start in the settings while the setup page's asking waits for its press is refused, and says why: it
// neither takes the asking over nor is taken for the page's.
func TestTheScreensStartWaitsForThePagesAsking(t *testing.T) {
	_, f, _ := runDevice(t)
	if err := f.Arm("Hey Jarvis", Word, "browser"); err != nil {
		t.Fatal(err)
	}
	err := f.StartRun(5, 2)
	if err == nil || !strings.Contains(err.Error(), "setup page is waiting") {
		t.Fatalf("the screen's Start over the page's asking: %v", err)
	}
	if c := f.Card(); c.Phase != Armed || c.Run || c.Word != "Hey Jarvis" {
		t.Errorf("the page's asking became %+v", c)
	}
	if st := f.State("browser"); !st.Mine || st.Run {
		t.Errorf("the page sees %+v", st)
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
	// A word begun as the beep fades is kept from where the beep's sound ends, without any of it.
	if pcm, ok := pickTake(capture(pre, echo, [2]int{pre + echo - 5, 25}), pre); !ok {
		t.Error("a word begun as the beep faded was not taken")
	} else if n := len(pcm) / mic.FrameSamples; n < 20 || n > 20+framesIn(pad)+2 {
		t.Errorf("the take of a word begun as the beep faded is %d frames", n)
	}
	// One that is mostly under the beep leaves too little to be a word.
	if _, ok := pickTake(capture(pre, echo, [2]int{pre, echo + framesIn(shortest)/2}), pre); ok {
		t.Error("a word said under the beep was taken")
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
