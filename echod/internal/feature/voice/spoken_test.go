package voice

import (
	"strings"
	"testing"
	"time"
)

// How much of a reply has been said goes by its audio once that plays, by the pace of speech for
// audio still arriving, and by the pace alone for a reply with no audio here.
func TestSpokenFollowsTheReplysAudio(t *testing.T) {
	t.Cleanup(func() { spoken.Store(nil) })
	text := strings.Repeat("x", 150) // 10 seconds at spokenPace
	t0 := time.Now()

	replyArrived(t0)
	if got := Spoken(text, t0.Add(5*time.Second)); got != 0.5 {
		t.Errorf("no audio, 5 of 10 seconds: said %.2f, want 0.5", got)
	}

	replyPlaying(t0, 2, false)
	if got := Spoken(text, t0.Add(5*time.Second)); got != 0.5 {
		t.Errorf("2 seconds of audio so far: said %.2f, want the pace's 0.5", got)
	}

	replyPlaying(t0, 20, true)
	if got := Spoken(text, t0.Add(5*time.Second)); got != 0.25 {
		t.Errorf("all 20 seconds of audio: said %.2f, want 0.25", got)
	}
	if got := Spoken(text, t0.Add(time.Minute)); got != 1 {
		t.Errorf("after the audio: said %.2f, want 1", got)
	}

	replyArrived(t0.Add(time.Minute))
	if got := Spoken(text, t0.Add(time.Minute)); got != 0 {
		t.Errorf("the next reply's words: said %.2f, want 0", got)
	}
}
