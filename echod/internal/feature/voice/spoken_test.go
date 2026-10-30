package voice

import (
	"math"
	"strings"
	"testing"
	"time"
)

// How much of a reply has been said goes by its audio once that plays, spokenLag behind; by a slow
// voice's pace for audio still arriving (#333); and by the pace alone for a reply with no audio here.
func TestSpokenFollowsTheReplysAudio(t *testing.T) {
	t.Cleanup(func() { spoken.Store(nil) })
	text := strings.Repeat("x", 165) // 11 seconds at spokenPace, 15 at arrivingPace
	t0 := time.Now()
	near := func(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

	replyArrived(t0, false)
	if got := Spoken(text, t0.Add(5500*time.Millisecond)); !near(got, 0.5) {
		t.Errorf("no audio, 5.5 of 11 seconds: said %.3f, want 0.5", got)
	}

	replyPlaying(t0, 2, false)
	if got := Spoken(text, t0.Add(7*time.Second)); !near(got, 6.0/15) {
		t.Errorf("2 seconds of audio so far: said %.3f, want a slow voice's 6 of 15 seconds", got)
	}

	replyPlaying(t0, 20, true)
	if got := Spoken(text, t0.Add(6*time.Second)); !near(got, 0.25) {
		t.Errorf("all 20 seconds of audio: said %.3f, want 5 of 20 seconds", got)
	}
	if got := Spoken(text, t0.Add(time.Second)); got != 0 {
		t.Errorf("within the lag: said %.3f, want 0", got)
	}
	if got := Spoken(text, t0.Add(time.Minute)); got != 1 {
		t.Errorf("after the audio: said %.3f, want 1", got)
	}

	replyArrived(t0.Add(time.Minute), false)
	if got := Spoken(text, t0.Add(time.Minute)); got != 0 {
		t.Errorf("the next reply's words: said %.2f, want 0", got)
	}

	// Realtime voice has the words only once it has said them.
	replyArrived(t0, true)
	if got := Spoken(text, t0); got != 1 {
		t.Errorf("words already said: said %.2f, want 1", got)
	}
}
