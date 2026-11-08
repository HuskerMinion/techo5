package voice

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/lib/endpoint"
)

// sink is a backend that keeps the audio it is sent.
type sink struct {
	samples int
	first   []byte
}

func (s *sink) Ready() bool        { return true }
func (s *sink) Start(string) error { return nil }
func (s *sink) End() error         { return nil }
func (s *sink) Stop() error        { return nil }
func (s *sink) Name() string       { return "sink" }
func (s *sink) Audio(f []byte) error {
	if s.first == nil {
		s.first = f
	}
	s.samples += len(f) / 2
	return nil
}

// run pumps pre and then frames through a turn on slot 0 and reports what the backend was sent and
// whether the device decided the speaker had finished.
func run(t *testing.T, tone config.Tone, cancel bool, pre []int16, frames ...[]int16) (be *sink, ended bool) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Wake(0).Tone(tone); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Microphone().Cancel(cancel); err != nil {
		t.Fatal(err)
	}
	c := &conversation{source: mic.New(), out: make(chan func() error, 1024), events: make(chan event, 64)}
	be = &sink{}
	ch := make(chan []int16, len(frames))
	for _, f := range frames {
		ch <- f
	}
	close(ch)
	c.pump(context.Background(), be, 0, false, false, holdsForTone(0, false), "turn", pre, ch)
	for len(c.out) > 0 {
		_ = (<-c.out)()
	}
	for len(c.events) > 0 {
		if (<-c.events).kind == evSpokeEnd {
			ended = true
		}
	}
	return be, ended
}

// With a wake sound the history reaches back only to the moment the word was heard: the 250 ms before
// it are the end of the wake word, which speech-to-text took for the start of the request (#445).
func TestHistoryLeavesTheWakeWordOutWhenASoundPlays(t *testing.T) {
	waited := 400 * time.Millisecond
	for _, l := range []struct {
		tone config.Tone
		want time.Duration
	}{
		{config.ToneHA, waited},
		{config.ToneNone, mic.History + waited},
	} {
		config.Use(filepath.Join(t.TempDir(), "state.json"))
		_ = config.Set().Wake(0).Tone(l.tone)
		if got := historyFor(0, false, false, waited, waited); got != l.want {
			t.Errorf("tone %v: history %v, want %v", l.tone, got, l.want)
		}
	}
	// The moment between the word being heard and the turn opening is kept, never more than the reach
	// without a sound.
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Wake(0).Tone(config.ToneHA)
	if got := historyFor(0, false, false, 0, 30*time.Millisecond); got != 30*time.Millisecond {
		t.Errorf("heard 30 ms ago: history %v, want 30ms", got)
	}
	if got := historyFor(0, false, false, 0, time.Minute); got != mic.History {
		t.Errorf("heard a minute ago: history %v, want at most %v", got, mic.History)
	}
}

// A wake Home Assistant confirmed reaches back to when the device scored the word, with or without a
// wake sound, as far as the microphones' history goes (#477).
// A turn on a run Home Assistant heard the word in sends no history, its run has had it; one it
// confirmed does, since that audio went to its wake word stage (#477).
func TestAConfirmedTurnSendsItsHistory(t *testing.T) {
	if hadAudio(nextTurn{continued: true, confirmed: true}) {
		t.Error("a confirmed turn counted as having had its audio")
	}
	if !hadAudio(nextTurn{continued: true}) {
		t.Error("a turn Home Assistant heard the word in did not count as having had its audio")
	}
	if hadAudio(nextTurn{}) {
		t.Error("a turn of the device's own counted as having had its audio")
	}
}

func TestAConfirmedWakeReachesBackToTheScore(t *testing.T) {
	for _, tone := range []config.Tone{config.ToneNone, config.ToneHA} {
		config.Use(filepath.Join(t.TempDir(), "state.json"))
		_ = config.Set().Wake(0).Tone(tone)
		if got, want := historyFor(0, false, true, 0, 900*time.Millisecond), 900*time.Millisecond+mic.History; got != want {
			t.Errorf("tone %v, scored 900 ms ago: history %v, want %v", tone, got, want)
		}
		if got := historyFor(0, false, true, 0, time.Minute); got != mic.Confirming {
			t.Errorf("tone %v, scored a minute ago: history %v, want at most %v", tone, got, mic.Confirming)
		}
	}
}

func TestHoldsForToneOnlyWithoutTheCanceller(t *testing.T) {
	for _, l := range []struct {
		tone   config.Tone
		cancel bool
		want   bool
	}{
		{config.ToneHA, true, false},
		{config.ToneHA, false, true},
		{config.ToneNone, false, false},
		{config.ToneNone, true, false},
	} {
		config.Use(filepath.Join(t.TempDir(), "state.json"))
		_ = config.Set().Wake(0).Tone(l.tone)
		_ = config.Set().Microphone().Cancel(l.cancel)
		if got := holdsForTone(0, false); got != l.want {
			t.Errorf("tone %s, canceller %v: holds %v, want %v", l.tone, l.cancel, got, l.want)
		}
	}
}

// With the canceller on, the wake sound does not gate the microphones: the history and the first live
// frame go out at once, in that order, while the sound is still playing.
func TestWakeSoundDoesNotGateWithTheCanceller(t *testing.T) {
	pre := voiceAt(3, 1500)
	live := voiceAt(2, 1500)
	be, _ := run(t, config.ToneHA, true, pre, live)
	if be.samples != len(pre)+len(live) {
		t.Fatalf("sent %d samples, want the history and the live frame, %d", be.samples, len(pre)+len(live))
	}
	if int16(be.first[0])|int16(be.first[1])<<8 != pre[0] {
		t.Error("the first audio sent is not the start of the history")
	}
}

// Without it, today's behavior: no history, and nothing while the sound plays.
func TestWakeSoundGatesWithoutTheCanceller(t *testing.T) {
	be, _ := run(t, config.ToneHA, false, voiceAt(3, 1500), voiceAt(2, 1500))
	if be.samples != 0 {
		t.Fatalf("sent %d samples through the wake sound with no canceller", be.samples)
	}
}

// A slot with no sound sends the history either way.
func TestSilentSlotSendsHistory(t *testing.T) {
	for _, cancel := range []bool{true, false} {
		pre := voiceAt(3, 1500)
		be, _ := run(t, config.ToneNone, cancel, pre)
		if be.samples != len(pre) {
			t.Errorf("canceller %v: sent %d, want %d", cancel, be.samples, len(pre))
		}
	}
}

// A wake word with nothing after it ends cleanly where nothing plays, and the history that holds the
// wake word cannot end a turn whose wake sound is still playing, whose person is waiting for it.
func TestHistoryDoesNotEndATurnEarly(t *testing.T) {
	word := voiceAt(5, 1500)
	quiet := voiceAt(10, 20)
	if _, ended := run(t, config.ToneNone, true, word, quiet); !ended {
		t.Error("a wake word with nothing after it did not end the turn")
	}
	if _, ended := run(t, config.ToneHA, true, word, quiet); ended {
		t.Error("the wake word and the quiet while the wake sound plays ended the turn")
	}
	if got := endpoint.Window; got == 0 {
		t.Fatal("window")
	}
}

// stepping makes the turn's clock move a tenth of a second at each look, which is one frame of
// voiceAt's, so a test needs no real waiting. It returns the number of frames the wake sound lasts.
func stepping(t *testing.T) int {
	t.Helper()
	now := time.Unix(1000, 0)
	clock = func() time.Time { now = now.Add(100 * time.Millisecond); return now }
	t.Cleanup(func() { clock = time.Now })
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Wake(0).Tone(config.ToneHA)
	_ = config.Set().Microphone().Cancel(true)
	return int(toneEnds(0, false)/(100*time.Millisecond)) + 1
}

// A request said in one breath after the wake word fits inside the wake sound. The turn ends on the
// quiet after the sound, not at the listening limit.
func TestRequestInsideTheWakeSoundEndsAfterIt(t *testing.T) {
	n := stepping(t)
	var frames [][]int16
	frames = append(frames, voiceAt(1, 1500), voiceAt(1, 1500), voiceAt(1, 1500), voiceAt(1, 1500))
	for len(frames) < n {
		frames = append(frames, voiceAt(1, 20))
	}
	if _, ended := run(t, config.ToneHA, true, nil, frames...); ended {
		t.Fatal("the turn ended inside the wake sound")
	}
	// the same, with quiet after the request that is still short of saidQuiet
	for len(frames) < 4+saidQuiet-1 {
		frames = append(frames, voiceAt(1, 20))
	}
	if _, ended := run(t, config.ToneHA, true, nil, frames...); ended {
		t.Fatal("the turn ended before saidQuiet of quiet after the request")
	}
	// and with all of it
	frames = append(frames, voiceAt(1, 20))
	if _, ended := run(t, config.ToneHA, true, nil, frames...); !ended {
		t.Fatal("a request spoken inside the wake sound did not end the turn on the quiet after it")
	}
}

// The end of the wake word and the sound itself count as speech inside the sound. Someone who waits for
// the sound and a second more before asking is not cut off in that pause, and the turn ends on the
// quiet after what they asked.
func TestWaitingAfterTheWakeSoundIsNotCutOff(t *testing.T) {
	n := stepping(t)
	var frames [][]int16
	frames = append(frames, voiceAt(1, 1100), voiceAt(1, 1100), voiceAt(1, 1100))
	for len(frames) < n+10 {
		frames = append(frames, voiceAt(1, 20))
	}
	if _, ended := run(t, config.ToneHA, true, nil, frames...); ended {
		t.Fatal("the wake word and the sound ended the turn in the pause before the request")
	}
	for range 10 {
		frames = append(frames, voiceAt(1, 1500))
	}
	if _, ended := run(t, config.ToneHA, true, nil, frames...); ended {
		t.Fatal("the turn ended while the request was being said")
	}
	for range 8 {
		frames = append(frames, voiceAt(1, 20))
	}
	if _, ended := run(t, config.ToneHA, true, nil, frames...); !ended {
		t.Fatal("the quiet after the request did not end the turn")
	}
}

// Someone who says the wake word, waits for the sound and then speaks is not cut off, and the wake
// word in the history does not count as speech.
func TestSpeakingAfterTheWakeSoundIsNotCutOff(t *testing.T) {
	n := stepping(t)
	var frames [][]int16
	for range n + 3 {
		frames = append(frames, voiceAt(1, 20))
	}
	frames = append(frames, voiceAt(5, 1500))
	if _, ended := run(t, config.ToneHA, true, nil, frames...); ended {
		t.Fatal("quiet after the wake word ended the turn before the request")
	}
}

// Nothing said still ends the turn only as before: never from the detector, since there was no speech.
func TestSilenceAfterTheWakeSoundEndsAsBefore(t *testing.T) {
	n := stepping(t)
	var frames [][]int16
	for range n + 15 {
		frames = append(frames, voiceAt(1, 0))
	}
	if _, ended := run(t, config.ToneHA, true, nil, frames...); ended {
		t.Fatal("silence ended the turn")
	}
}

// A request from across the room, at a quarter of the wake word's level, after the sound and a
// pause, is not cut off by the check for a request said inside the sound.
func TestASoftRequestAfterTheWakeSoundIsNotCutOff(t *testing.T) {
	n := stepping(t)
	var frames [][]int16
	frames = append(frames, voiceAt(1, 1100), voiceAt(1, 1100), voiceAt(1, 1100))
	for len(frames) < n+20 {
		frames = append(frames, voiceAt(1, 20))
	}
	for range 30 {
		frames = append(frames, voiceAt(1, 275))
	}
	if _, ended := run(t, config.ToneHA, true, nil, frames...); ended {
		t.Fatal("a soft request after the wake sound was cut off")
	}
}
