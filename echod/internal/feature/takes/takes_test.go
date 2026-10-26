package takes

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// With the switch off nothing can be asked for, whoever asks.
func TestARecordingNeedsTheSwitch(t *testing.T) {
	d, f := newDevice(t)
	d.on.Store(false)
	if err := f.Arm("Hey Jarvis", Word, "browser"); !errors.Is(err, errOff) {
		t.Errorf("asked with the switch off: %v", err)
	}
	if f.Card().Up() {
		t.Error("the screen was asked to start a recording with the switch off")
	}
}

// Every reason the microphones are not free refuses the asking, and the asking says which.
func TestARecordingCannotStartWhileTheMicrophonesAreBusy(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(d *fake)
		says string
	}{
		{"muted", func(d *fake) { d.muted.Store(true) }, "muted"},
		{"a mute that cannot be read", func(d *fake) { d.muteUnread.Store(true) }, "muted"},
		{"a call", func(d *fake) { d.call.Store(true) }, "call"},
		{"a talk through a camera", func(d *fake) { d.talk.Store(true) }, "camera"},
		{"a silent panel", func(d *fake) { d.silent.Store(true) }, "silent"},
		{"an announcement", func(d *fake) { d.announcing.Store(true) }, "announcement"},
		{"a ring", func(d *fake) { d.ringing.Store(true) }, "ringing"},
		{"something playing", func(d *fake) { d.audible.Store(true) }, "playing"},
		{"a turn", func(d *fake) { d.turn.Store(true) }, "request"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, f := newDevice(t)
			c.set(d)
			err := f.Arm("Hey Jarvis", Word, "browser")
			if err == nil || !strings.Contains(err.Error(), c.says) {
				t.Errorf("asked during %s: %v", c.name, err)
			}
			if f.Card().Up() || f.Holding() {
				t.Error("the recorder went on anyway")
			}
		})
	}
	_, f := newDevice(t)
	if err := f.Arm("  \n ", Word, "browser"); err == nil {
		t.Error("a recording of no word was asked for")
	}
}

// The setup page can ask, but only the press starts it: an asking left alone opens no microphone,
// and gives up after the minute.
func TestTheBrowserAloneCannotStartARecording(t *testing.T) {
	d, f := newDevice(t)
	if err := f.Arm("Hey Jarvis", Word, "browser"); err != nil {
		t.Fatal(err)
	}
	if c := f.Card(); c.Phase != Armed || c.Word != "Hey Jarvis" {
		t.Errorf("the screen was asked %+v", c)
	}
	if f.Holding() {
		t.Error("the microphones were taken before the press")
	}
	select {
	case <-d.mics:
		t.Fatal("the microphones were listened to before the press")
	default:
	}
	d.wait(armFor + time.Second)
	f.poke <- struct{}{}
	st := settled(t, f, Idle)
	if !strings.Contains(st.Why, "nobody pressed") {
		t.Errorf("gave up saying %q", st.Why)
	}
	if f.Begin(0) {
		t.Error("a press after the minute started it")
	}
}

// Something that takes the microphones while a recording waits for the press calls the wait off.
func TestAWaitForThePressGivesUpOnACall(t *testing.T) {
	d, f := newDevice(t)
	if err := f.Arm("Hey Jarvis", Word, "browser"); err != nil {
		t.Fatal(err)
	}
	d.call.Store(true)
	f.poke <- struct{}{}
	if st := settled(t, f, Idle); !strings.Contains(st.Why, "call") {
		t.Errorf("gave up saying %q", st.Why)
	}
}

// The press starts it with the start chime, and Stop keeps what it was cut into, with the stop chime.
// The microphones stay the takes' for the tail, so a word still being said opens no turn.
func TestStoppingKeepsTheTakes(t *testing.T) {
	d, f := newDevice(t)
	m := d.recording(t, f, "Hey Jarvis")
	if !f.Holding() {
		t.Error("the microphones were not taken while recording")
	}
	if c := f.Card(); c.Phase != Recording {
		t.Errorf("the screen shows %v while recording", c.Phase)
	}
	d.takesOf(t, m, 4)
	f.Stop()
	st := settled(t, f, Kept)
	stopped(t, m)
	if st.Takes != 4 || st.Why != "stopped" || st.Lost {
		t.Errorf("kept %d takes (why %q, lost %v), want 4", st.Takes, st.Why, st.Lost)
	}
	// What was said, without the lead-in the start chime is still in, give or take the frame the clock
	// moves on while the recording notes when a frame came.
	if said := frames(int(leadIn/frameTime) + 50 + 4*75); st.Elapsed > said-leadIn+2*frameTime {
		t.Errorf("kept %v of the %v said: the lead-in was kept", st.Elapsed, said)
	}
	if n := d.chimes.Load(); n != 3 {
		t.Errorf("%d chimes, want one when asked, one to start and one to stop", n)
	}
	if !f.Holding() {
		t.Error("the microphones were let go with no tail")
	}
	d.wait(tail + time.Millisecond)
	if f.Holding() {
		t.Error("the microphones were still taken after the tail")
	}
}

// Throwing away keeps nothing, while recording or after.
func TestThrowingAwayKeepsNothing(t *testing.T) {
	d, f := newDevice(t)
	m := d.recording(t, f, "Hey Jarvis")
	d.takesOf(t, m, 2)
	f.Discard("thrown away on the setup page")
	stopped(t, m)
	st := settled(t, f, Idle)
	if st.Takes != 0 || st.Why != "thrown away on the setup page" {
		t.Errorf("after throwing away: %+v", st)
	}
	if _, _, ok := f.Take(1, "browser"); ok {
		t.Error("a take was still there to play")
	}

	m = d.recording(t, f, "Hey Jarvis")
	d.takesOf(t, m, 2)
	f.Stop()
	settled(t, f, Kept)
	f.Discard("the setup page closed")
	if _, _, ok := f.Zip("browser"); ok {
		t.Error("a zip was given after the takes were thrown away")
	}
}

// Whatever takes the microphones away throws the recording away; a ring ends it and keeps what came
// before, since the ring has nothing to do with the takes and has to stay stoppable.
func TestWhatEndsARecording(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(d *fake)
		keep bool
		says string
	}{
		{"a mute", func(d *fake) { d.muted.Store(true) }, false, "muted"},
		{"a mute that cannot be read", func(d *fake) { d.muteUnread.Store(true) }, false, "muted"},
		{"a call", func(d *fake) { d.call.Store(true) }, false, "call"},
		{"a talk through a camera", func(d *fake) { d.talk.Store(true) }, false, "camera"},
		{"the panel going silent", func(d *fake) { d.silent.Store(true) }, false, "silent"},
		{"an announcement", func(d *fake) { d.announcing.Store(true) }, false, "announcement"},
		{"the switch going off", func(d *fake) { d.on.Store(false) }, false, "switched off"},
		{"a ring", func(d *fake) { d.ringing.Store(true) }, true, "rang"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, f := newDevice(t)
			m := d.recording(t, f, "Hey Jarvis")
			d.takesOf(t, m, 3)
			c.set(d)
			f.poke <- struct{}{}
			stopped(t, m)
			want := Idle
			if c.keep {
				want = Kept
			}
			st := settled(t, f, want)
			if !strings.Contains(st.Why, c.says) {
				t.Errorf("ended by %s saying %q", c.name, st.Why)
			}
			if c.keep && st.Takes != 3 {
				t.Errorf("kept %d takes from before %s, want 3", st.Takes, c.name)
			}
			if !c.keep && st.Takes != 0 {
				t.Errorf("kept %d takes after %s", st.Takes, c.name)
			}
		})
	}
}

// The microphones restarting leaves a hole: the recording ends there and keeps what came before.
func TestAGapInTheFramesEndsTheRecording(t *testing.T) {
	d, f := newDevice(t)
	m := d.recording(t, f, "Hey Jarvis")
	d.takesOf(t, m, 2)
	// The clock goes on with no frames until the check every quarter second sees the gap. On and on,
	// since the recording may note the time of the last frame only after the clock first moved.
	for gone := false; !gone; {
		d.wait(gap + time.Millisecond)
		select {
		case <-m.stopped:
			gone = true
		case <-time.After(100 * time.Millisecond):
		}
	}
	if st := settled(t, f, Kept); st.Takes != 2 || !strings.Contains(st.Why, "paused") {
		t.Errorf("after a gap: %d takes, %q", st.Takes, st.Why)
	}
}

// Two minutes is the most: the recording stops taking frames there, and keeps them.
func TestARecordingStopsAtTheCeiling(t *testing.T) {
	d, f := newDevice(t)
	m := d.recording(t, f, "Hey Jarvis")
	most := int((Longest + leadIn) / frameTime)
	if d.say(m, room, most+50) {
		t.Fatal("the recording took more than two minutes of frames")
	}
	st := settled(t, f, Kept)
	if st.Elapsed > Longest || st.Elapsed < Longest-time.Second {
		t.Errorf("recorded %v, want two minutes", st.Elapsed)
	}
	if !strings.Contains(st.Why, "two minutes") {
		t.Errorf("ended saying %q", st.Why)
	}
}

// A turn that opened between the asking and the press would hear the takes as a question: the press
// ends it.
func TestATurnThatSlippedInIsEnded(t *testing.T) {
	d, f := newDevice(t)
	if err := f.Arm("Hey Jarvis", Word, "browser"); err != nil {
		t.Fatal(err)
	}
	d.turn.Store(true)
	if !f.Begin(0) {
		t.Fatal("the press did not start it")
	}
	if d.canceled.Load() != 1 {
		t.Error("the turn that slipped in was left running")
	}
	<-d.mics
	f.Discard("done")
}

// The press is the recorder's while it waits for one and for a moment after it took one, so voice,
// asking after the recording started, does not take the same press as a stop. Not while a ring sounds:
// that press is the ring's.
func TestThePressIsTheRecordersEvenAfterItStarted(t *testing.T) {
	d, f := newDevice(t)
	if f.TookPress() {
		t.Error("a press with nothing asked for was taken")
	}
	if err := f.Arm("Hey Jarvis", Word, "browser"); err != nil {
		t.Fatal(err)
	}
	if !f.TookPress() {
		t.Error("the press a recording waits for was not taken")
	}
	d.ringing.Store(true)
	if f.TookPress() || f.Pressed() {
		t.Error("the press meant for a ring was taken")
	}
	d.ringing.Store(false)
	d.press()
	if !f.Pressed() {
		t.Fatal("the press did not start it")
	}
	<-d.mics
	// However long starting took: the button reads down until every listener has heard it.
	d.wait(time.Second)
	if !f.TookPress() {
		t.Error("voice, asking after the press started it, would take it as a stop")
	}
	d.lift()
	if f.TookPress() {
		t.Error("the press that started it was still the recorder's after it ended")
	}
	d.wait(time.Second)
	d.press()
	if f.TookPress() {
		t.Error("the next press, to stop it, was taken as the recorder's")
	}
	f.Discard("done")
}

// The screen's Start answers the asking it was shown, not one made since; Not now gives it up.
func TestStartIsForTheAskingItWasShown(t *testing.T) {
	_, f := newDevice(t)
	if err := f.Arm("Hey Jarvis", Word, "browser"); err != nil {
		t.Fatal(err)
	}
	shown := f.Card().ID
	f.Decline(shown)
	if st := settled(t, f, Idle); !strings.Contains(st.Why, "not started") {
		t.Errorf("Not now said %q", st.Why)
	}
	if err := f.Arm("Okay Nabu", Word, "browser"); err != nil {
		t.Fatal(err)
	}
	if f.Begin(shown) {
		t.Error("Start on the old card started the new asking")
	}
	if c := f.Card(); c.Phase != Armed || c.Word != "Okay Nabu" {
		t.Errorf("the new asking is %+v", c)
	}
}

// Asking again while the device waits changes the word and not the minute, and asking over and over
// with nobody pressing stops being allowed for a while: a browser must not be able to keep a Dot's
// every tap a recording's start. Each asking is said at the device. A press clears the run.
func TestAskingCannotKeepTheDeviceWaiting(t *testing.T) {
	d, f := newDevice(t)
	if err := f.Arm("Hey Jarvis", Word, "browser"); err != nil {
		t.Fatal(err)
	}
	if d.chimes.Load() != 1 {
		t.Errorf("%d notes when asked, want one at the device", d.chimes.Load())
	}
	d.wait(armFor - 5*time.Second)
	if err := f.Arm("Okay Nabu", Word, "browser"); err != nil {
		t.Fatal(err)
	}
	if d.chimes.Load() != 1 {
		t.Error("asking again while it waited was said again")
	}
	d.wait(10 * time.Second)
	f.poke <- struct{}{}
	if st := settled(t, f, Idle); !strings.Contains(st.Why, "nobody pressed") {
		t.Errorf("asking again kept it waiting past the minute: %q", st.Why)
	}
	for range tries - 1 {
		if err := f.Arm("Hey Jarvis", Word, "browser"); err != nil {
			t.Fatalf("asking again: %v", err)
		}
		f.Decline(f.Card().ID)
		settled(t, f, Idle)
	}
	if err := f.Arm("Hey Jarvis", Word, "browser"); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Errorf("asked a fourth time with no press: %v", err)
	}
	d.wait(coolOff - time.Second)
	if err := f.Arm("Hey Jarvis", Word, "browser"); err == nil {
		t.Error("asked again inside the cool-off")
	}
	d.wait(2 * time.Second)
	for range tries + 2 {
		if err := f.Arm("Hey Jarvis", Word, "browser"); err != nil {
			t.Fatalf("asking after the cool-off, with a press each time: %v", err)
		}
		if !f.Begin(0) {
			t.Fatal("the press did not start it")
		}
		<-d.mics
		f.Discard("again")
	}
}

// Another browser cannot ask over takes kept for the one that recorded them, and so throw them away.
func TestAnotherBrowsersTakesAreLeftAlone(t *testing.T) {
	d, f := newDevice(t)
	m := d.recording(t, f, "Hey Jarvis")
	d.takesOf(t, m, 2)
	f.Stop()
	settled(t, f, Kept)
	if err := f.Arm("Okay Nabu", Word, "another"); err == nil || !strings.Contains(err.Error(), "another browser's takes") {
		t.Errorf("another browser asked over kept takes: %v", err)
	}
	if st := f.State("browser"); st.Phase != Kept || st.Takes != 2 {
		t.Errorf("the takes after another browser asked: %+v", st)
	}
}

// A recording still winding down after it was thrown away cannot give back the microphones the next
// recording has.
func TestAnOldRecordingCannotLetGoOfTheNext(t *testing.T) {
	d, f := newDevice(t)
	f.running.Store(7)
	f.release(6)
	d.wait(tail + time.Second)
	if !f.Holding() {
		t.Error("an old recording let go of the microphones the next one has")
	}
	f.release(7)
	if !f.Holding() {
		t.Error("the microphones were given back with no tail")
	}
	d.wait(tail + time.Millisecond)
	if f.Holding() {
		t.Error("the microphones were still taken after the tail")
	}
}

// The takes are the browser's that asked: another sees nothing of them and cannot ask over them.
func TestOnlyTheBrowserThatAskedHasTheTakes(t *testing.T) {
	d, f := newDevice(t)
	if err := f.Arm("Hey Jarvis", Word, "browser"); err != nil {
		t.Fatal(err)
	}
	if err := f.Arm("Okay Nabu", Word, "another"); err == nil || f.Card().Word != "Hey Jarvis" {
		t.Errorf("another browser asked over one waiting for the press: %v, now %q", err, f.Card().Word)
	}
	f.Discard("again")
	m := d.recording(t, f, "Hey Jarvis")
	if err := f.Arm("Okay Nabu", Word, "another"); err == nil {
		t.Error("another browser asked over a running recording")
	}
	d.takesOf(t, m, 2)
	f.Stop()
	settled(t, f, Kept)
	if st := f.State("another"); st.Mine || st.Word != "" || st.Takes != 0 {
		t.Errorf("another browser was shown %+v", st)
	}
	if _, _, ok := f.Zip("another"); ok {
		t.Error("another browser was given a zip")
	}
	if _, _, ok := f.Take(1, "another"); ok {
		t.Error("another browser could play a take")
	}
	if _, _, ok := f.Take(1, "browser"); !ok {
		t.Error("the browser that asked could not play its take")
	}
}

// The frames are shared with every other listener (hardware/mic): the recorder keeps a copy of each and
// never writes into the one it was given.
func TestTheRecorderNeverWritesIntoAFrame(t *testing.T) {
	d, f := newDevice(t)
	m := d.recording(t, f, "Hey Jarvis")
	d.takesOf(t, m, 1)
	shared := frame(word, 7)
	was := slices.Clone(shared)
	d.wait(frameTime)
	m.frames <- shared
	d.say(m, room, 60)
	f.Stop()
	settled(t, f, Kept)
	if !slices.Equal(shared, was) {
		t.Error("the recorder wrote into a frame every listener shares")
	}
	f.mu.Lock()
	pcm := f.pcm
	f.mu.Unlock()
	at := len(pcm) - (60+1)*len(shared)
	if !slices.Equal(pcm[at:at+len(shared)], was) {
		t.Error("the frame kept is not the frame as it was heard")
	}
	if &pcm[at] == &shared[0] {
		t.Error("the recorder kept the shared frame itself, not a copy")
	}
}

// A word is kept the way it was meant: no control characters, no runs of spaces, at most MaxWord.
func TestAWordIsCleaned(t *testing.T) {
	for in, want := range map[string]string{
		"  hey\n\tjarvis\x00 ":  "hey jarvis",
		"Okay Nabu":             "Okay Nabu",
		strings.Repeat("a", 60): strings.Repeat("a", MaxWord),
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

// Frames lost on the way are said; a pause after the last frame, the one that ended the recording, is
// not lost audio.
func TestLostFramesAreToldFromTheLastFrame(t *testing.T) {
	kept := 4020 * time.Millisecond
	if short(kept, leadIn+kept) {
		t.Error("every frame kept, and said to be short")
	}
	if !short(kept*9/10, leadIn+kept) {
		t.Error("a tenth of the frames missing, and not said")
	}
}

// The note that says the device is asked for a recording is the device's own sound, not something
// playing: the asking waits for the press through it, and a press while it is still heard starts the
// recording. Something that starts playing after it still ends the wait.
func TestTheAskNoteIsNotSomethingPlaying(t *testing.T) {
	d, f := newDevice(t)
	if err := f.Arm("Hey Jarvis", Word, "browser"); err != nil {
		t.Fatal(err)
	}
	if !f.dev.audible() {
		t.Fatal("the fake note was not heard")
	}
	f.poke <- struct{}{}
	time.Sleep(50 * time.Millisecond) // the wait looks at the poke
	d.wait(200 * time.Millisecond)
	if c := f.Card(); c.Phase != Armed {
		t.Fatalf("the asking gave up on its own note: %q", f.State("browser").Why)
	}
	if !f.Begin(0) {
		t.Fatal("a press while the note was heard did not start it")
	}
	<-d.mics
	f.Discard("again")

	d.wait(2 * time.Second)
	if err := f.Arm("Hey Jarvis", Word, "browser"); err != nil {
		t.Fatal(err)
	}
	d.wait(ownHeard)
	d.audible.Store(true)
	f.poke <- struct{}{}
	if st := settled(t, f, Idle); !strings.Contains(st.Why, "playing") {
		t.Errorf("music starting during the wait ended it saying %q", st.Why)
	}
}

// Another word asked for while the device waits is another card: Start on the one with the old word
// starts nothing, and the new one is waited for as long as the first was.
func TestAnotherWordIsAnotherCard(t *testing.T) {
	d, f := newDevice(t)
	if err := f.Arm("Hey Jarvis", Word, "browser"); err != nil {
		t.Fatal(err)
	}
	shown := f.Card().ID
	if err := f.Arm("Okay Nabu", Word, "browser"); err != nil {
		t.Fatal(err)
	}
	if f.Begin(shown) {
		t.Error("Start on the card with the old word started the new one")
	}
	if c := f.Card(); c.Word != "Okay Nabu" || c.ID == shown {
		t.Errorf("the card after another word: %+v", c)
	}
	d.wait(armFor + time.Second)
	f.poke <- struct{}{}
	if st := settled(t, f, Idle); !strings.Contains(st.Why, "nobody pressed") {
		t.Errorf("the new word was waited for past the first minute: %q", st.Why)
	}
}

// Frames that never came, while the clock went on, are said: the takes may have holes in them.
func TestFramesLostOnTheWayAreSaid(t *testing.T) {
	d, f := newDevice(t)
	m := d.recording(t, f, "Hey Jarvis")
	d.takesOf(t, m, 1)
	d.wait(frames(20)) // 20 frames that never arrive
	d.takesOf(t, m, 1)
	f.Stop()
	if st := settled(t, f, Kept); !st.Lost {
		t.Error("frames went missing and the takes did not say so")
	}
}
