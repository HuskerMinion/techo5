package voice

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
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
