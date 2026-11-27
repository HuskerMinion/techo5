package voice

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
)

// countChimes stands in for the wake sound for one test and counts it.
func countChimes(t *testing.T) *int {
	t.Helper()
	was := wakeChime
	t.Cleanup(func() { wakeChime = was })
	var n int
	wakeChime = func(int, bool) { n++ }
	return &n
}

// A turn whose wake word chimed as it was heard opens without a second sound (#727); every other turn
// chimes as it opens, as before.
func TestAnEarlyChimedTurnOpensWithoutASecondOne(t *testing.T) {
	for _, early := range []bool{true, false} {
		t.Run(map[bool]string{true: "chimed at the word", false: "not"}[early], func(t *testing.T) {
			w := newWatcher(t, config.OnHA{})
			chimes := countChimes(t)
			e := event{kind: evStart, slot: 0, woke: true, at: w.at}
			if early {
				e.chimed = w.at
			}
			w.handle(e)
			if w.phase != phaseListening {
				t.Fatalf("the turn did not open: phase %s", w.phase)
			}
			if want := map[bool]int{true: 0, false: 1}[early]; *chimes != want {
				t.Errorf("chimed %d times as the turn opened, want %d", *chimes, want)
			}
		})
	}
}

// One that cannot open, with no pipeline ready, plays no second sound either: the trouble it shows is
// what says so.
func TestAnEarlyChimedTurnThatCannotOpenDoesNotChimeAgain(t *testing.T) {
	w := newWatcher(t, config.OnHA{})
	w.ready = false
	chimes := countChimes(t)
	w.handle(event{kind: evStart, slot: 0, woke: true, at: w.at, chimed: w.at})
	if w.phase != phaseIdle || *chimes != 0 {
		t.Errorf("phase %s, chimed %d; want no turn and no second sound", w.phase, *chimes)
	}
}

// ChimeNow plays the sound and nothing else: no turn, no event, no ducking, nothing on the screen. It
// plays none where the turn would not have chimed as it opened, or chimes only after a ring is silenced.
func TestChimeNowIsTheSoundAlone(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Wake(0).Tone(config.ToneHA)
	chimes := countChimes(t)
	v := wakeCallVoice()
	before := time.Now()
	at := v.ChimeNow(0)
	if at.Before(before) || *chimes != 1 {
		t.Fatalf("chimed %d times at %v, want once now", *chimes, at)
	}
	if len(v.turn.events) != 0 || v.Busy() || v.turn.holding || len(v.turn.out) != 0 {
		t.Errorf("the sound did more: %d events, busy %v, ducking %v, %d sent", len(v.turn.events), v.Busy(),
			v.turn.holding, len(v.turn.out))
	}
}

func TestChimeNowPlaysNoneWhereTheTurnWouldNot(t *testing.T) {
	cases := []struct {
		name string
		set  func(t *testing.T, v *Voice)
	}{
		{"a slot with no sound", func(t *testing.T, v *Voice) { _ = config.Set().Wake(0).Tone(config.ToneNone) }},
		{"over a ring", func(t *testing.T, v *Voice) { sound(t) }},
		{"a turn running", func(t *testing.T, v *Voice) { v.turn.visible.Store(int32(phaseReplying)) }},
		{"a slot that calls a contact", func(t *testing.T, v *Voice) {
			_ = config.Set().Wake(0).ID("test_word")
			standIn(t)
			if err := SetWakeCall(1, "assistant"); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config.Use(filepath.Join(t.TempDir(), "state.json"))
			_ = config.Set().Wake(0).Tone(config.ToneHA)
			chimes := countChimes(t)
			v := wakeCallVoice()
			tc.set(t, v)
			if at := v.ChimeNow(0); !at.IsZero() || *chimes != 0 {
				t.Errorf("chimed %d times, at %v; want none", *chimes, at)
			}
		})
	}
}

// early is a history of a wake sound played at the word, followed by what was said after it. The sound
// takes the first sound windows of it, a window being a tenth of a second, and runs on for the length
// of the real sound (toneEnds) from the start of the history: chimedAgo is how long before the history
// ends the sound began, so it ends where the windows do.
func early(t *testing.T, cancel bool, after ...[]int16) (pre []int16, chimedAgo time.Duration, sound int) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Wake(0).Tone(config.ToneHA)
	_ = config.Set().Microphone().Cancel(cancel)
	tone := toneEnds(0, false)
	windows := int(tone/(100*time.Millisecond)) + 1
	pre = voiceAt(windows, 1500)
	sound = len(pre)
	for _, a := range after {
		pre = append(pre, a...)
	}
	// over = len(pre) - samplesIn(ago) + samplesIn(tone) = sound
	chimedAgo = durationOf(len(pre)-sound) + tone
	return pre, chimedAgo, sound
}

// runChimed is run for a turn whose wake sound played chimed before the end of its history.
func runChimed(t *testing.T, cancel bool, chimed time.Duration, pre []int16, frames ...[]int16) (be *sink, ended bool) {
	t.Helper()
	return runWith(t, config.ToneHA, cancel, chimed, pre, frames...)
}

// Without the canceller, a turn whose sound played at the word sends what was said after the sound,
// from the history, where one that chimes as it opens sends no history (#727).
func TestAnEarlyChimeSendsWhatWasSaidAfterItWithoutTheCanceller(t *testing.T) {
	request := voiceAt(3, 900)
	pre, ago, sound := early(t, false, request)
	live := voiceAt(2, 900)
	be, _ := runChimed(t, false, ago, pre, live)
	if be.samples != len(pre)-sound+len(live) {
		t.Fatalf("sent %d samples, want what followed the sound, %d", be.samples, len(pre)-sound+len(live))
	}
	if int16(be.first[0])|int16(be.first[1])<<8 != request[0] {
		t.Error("the first audio sent is not the start of what was said after the sound")
	}
}

// A sound still playing when the turn opens is waited out: none of the history, and nothing live while
// it lasts.
func TestAnEarlyChimeStillSoundingIsWaitedOutWithoutTheCanceller(t *testing.T) {
	pre, _, _ := early(t, false)
	be, _ := runChimed(t, false, 100*time.Millisecond, pre, voiceAt(2, 900))
	if be.samples != 0 {
		t.Fatalf("sent %d samples while the sound still played", be.samples)
	}
}

// With the canceller everything is sent as before, the sound included, which it takes out.
func TestAnEarlyChimeSendsAllOfTheHistoryWithTheCanceller(t *testing.T) {
	pre, ago, _ := early(t, true, voiceAt(3, 900))
	live := voiceAt(2, 900)
	be, _ := runChimed(t, true, ago, pre, live)
	if be.samples != len(pre)+len(live) {
		t.Fatalf("sent %d samples, want all of it, %d", be.samples, len(pre)+len(live))
	}
}

// With the canceller, end of speech is heard from the end of a sound played at the word, not from a
// sound's length after the turn opens. The word and the sound in the history do not count: someone who
// waited for the sound and paused is not cut off in the pause, and a request said in the history ends
// the turn on the quiet after it.
func TestAnEarlyChimeTimesTheEndOfSpeechFromTheSound(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	quiet := endpointSettings().Quiet
	t.Run("a pause after the sound", func(t *testing.T) {
		stepping(t)
		pre, ago, _ := early(t, true, voiceAt(quiet+2, 20))
		var frames [][]int16
		frames = append(frames, voiceAt(2, 20))
		frames = append(frames, voiceAt(8, 1500))
		if _, ended := runChimed(t, true, ago, pre, frames...); ended {
			t.Fatal("the word and the sound in the history ended the turn in the pause after them")
		}
		frames = append(frames, voiceAt(quiet+1, 20))
		if _, ended := runChimed(t, true, ago, pre, frames...); !ended {
			t.Fatal("the quiet after the request did not end the turn")
		}
	})
	t.Run("a request in the history", func(t *testing.T) {
		stepping(t)
		pre, ago, _ := early(t, true, voiceAt(2, 20), voiceAt(8, 1500))
		frames := [][]int16{voiceAt(quiet+1, 20)}
		if _, ended := runChimed(t, true, ago, pre, frames...); !ended {
			t.Fatal("a request said while the devices decided did not end the turn on the quiet after it")
		}
		// Timed from the turn opening, the same turn is still deaf to that quiet.
		if _, ended := runChimed(t, true, -1, pre, frames...); ended {
			t.Fatal("a turn that chimes as it opens heard the end of speech inside its own sound")
		}
	})
}

// A turn held back on its way to opening still reaches back to a sound played at the word, however long
// it was held, as far as the microphones' history goes: with the window's reach alone, the first words
// after the sound fell out of a turn held past it.
func TestAHeldTurnReachesBackToTheChime(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Wake(0).Tone(config.ToneHA)
	waited, heard := 300*time.Millisecond, 1500*time.Millisecond
	got := sinceChime(historyFor(0, false, false, waited, heard), heard)
	if got < heard {
		t.Errorf("held 1.5 s after the sound: history %v, want at least %v", got, heard)
	}
	if got := sinceChime(historyFor(0, false, false, waited, time.Minute), time.Minute); got != mic.Held {
		t.Errorf("a minute after the sound: history %v, want at most %v", got, mic.Held)
	}
	if got := sinceChime(mic.History+waited, 10*time.Millisecond); got != mic.History+waited {
		t.Errorf("a reach longer than the time since the sound was cut to %v", got)
	}
}

// An alarm that started ringing after the word chimed, while the devices decided, is silenced as the
// turn opens, and the turn chimes again over the quiet.
func TestARingInTheWindowChimesAgainAfterIt(t *testing.T) {
	for _, ringing := range []bool{true, false} {
		t.Run(map[bool]string{true: "ringing", false: "not"}[ringing], func(t *testing.T) {
			config.Use(filepath.Join(t.TempDir(), "state.json"))
			v := wakeCallVoice()
			if ringing {
				sound(t)
			}
			v.WokeChimed(0, time.Now(), 12, time.Now())
			if e := <-v.turn.events; e.kind != evStart || e.chimed.IsZero() == !ringing {
				t.Fatalf("posted chimed %v; want the turn to chime again only after a ring", e.chimed)
			}
		})
	}
}

// A slot after the first whose wake word is not running has no pipeline to reach, and the turn refuses
// it without a sound: so it does not chime at the word either.
func TestNoChimeForASlotWithNoWakeWord(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Wake(1).Tone(config.ToneHA)
	chimes := countChimes(t)
	v := wakeCallVoice()
	v.vs.ActiveWakeWords = []string{"okay_nabu"}
	if at := v.ChimeNow(1); !at.IsZero() || *chimes != 0 {
		t.Errorf("chimed %d times for a slot with no wake word", *chimes)
	}
	v.vs.ActiveWakeWords = []string{"okay_nabu", "hey_jarvis"}
	if at := v.ChimeNow(1); at.IsZero() {
		t.Error("no chime for the second slot with its wake word running")
	}
}

// The sound is placed by when it was heard, not when it was queued: the speaker's latency after it, and
// the capture's before the history ends, both come off; never below zero.
func TestTheChimeIsPlacedWhereTheMicrophonesHeardIt(t *testing.T) {
	if got, want := chimedAgo(500*time.Millisecond, 60*time.Millisecond), 440*time.Millisecond-captureLag; got != want {
		t.Errorf("queued 500 ms ago, 60 ms out: %v, want %v", got, want)
	}
	if got := chimedAgo(10*time.Millisecond, 60*time.Millisecond); got != 0 {
		t.Errorf("not heard yet: %v, want 0", got)
	}
}

// A wake word that chimed as it was heard over a turn still running interrupts it, and the interrupt
// silences the speaker, the early sound with it: the next turn chimes as it opens rather than opening
// silently. A sound long since played out is not played again.
func TestAnEarlyChimeCutByTheInterruptChimesAgain(t *testing.T) {
	for name, ago := range map[string]time.Duration{"just chimed": 0, "played out": time.Minute} {
		t.Run(name, func(t *testing.T) {
			got := told(t)
			c := streamedReply(t, got)
			c.ha.ready = func() bool { return true }
			c.ha.start = func(string) error { return nil }
			chimes := countChimes(t)

			now := time.Now()
			c.handle(event{kind: evStart, slot: 0, woke: true, at: now, chimed: now.Add(-ago)})
			c.handle(event{kind: evRunEnd})
			if c.phase != phaseListening {
				t.Fatalf("the next turn is %s, want listening", c.phase)
			}
			if want := map[bool]int{true: 1, false: 0}[ago == 0]; *chimes != want {
				t.Errorf("chimed %d times as the turn opened, want %d", *chimes, want)
			}
		})
	}
}

// The advertised wake words are read off the conversation's goroutine (ChimeNow, the phrase a turn
// reports) while Home Assistant's selection sets them on the connection's: under -race, a snapshot.
func TestTheWakeWordsAreReadWhileTheyAreSet(t *testing.T) {
	v := wakeCallVoice()
	v.SetActiveWakeWords([]string{"okay_nabu"})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 200 {
			if i%2 == 0 {
				v.SetActiveWakeWords([]string{"okay_nabu", "hey_jarvis"})
			} else {
				v.SetActiveWakeWords([]string{"okay_nabu"})
			}
		}
	}()
	for range 200 {
		if _, ok := v.turn.phraseFor(0); !ok {
			t.Fatal("the first slot's word went missing while the words were set")
		}
		_ = v.ActiveWakeWords()
	}
	<-done
}
