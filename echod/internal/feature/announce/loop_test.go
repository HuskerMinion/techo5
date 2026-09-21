package announce

import (
	"testing"
	"time"
)

// A device does not answer its own playback.
//
// Three devices on one desk hear each other perfectly well, and an announcement is speech: the wake
// word fired on the announcement coming out of the speaker beside it, announced that, and the house
// talked to itself until somebody held a mute button. The echo canceller cannot help, because what
// it knows is this device's own output, not the one a foot away.
func TestNotWhileAnAnnouncementIsPlaying(t *testing.T) {
	f := &Feature{}

	if f.JustPlayed() {
		t.Error("quiet device says it just played something")
	}

	f.mu.Lock()
	f.quietUntil = time.Now().Add(afterPlaying)
	f.mu.Unlock()

	if !f.JustPlayed() {
		t.Error("not holding off straight after playing one")
	}
}

// The hold expires, or the word would work once and never again.
func TestTheHoldOffEndsOnItsOwn(t *testing.T) {
	f := &Feature{}

	f.mu.Lock()
	f.quietUntil = time.Now().Add(-time.Second)
	f.mu.Unlock()

	if f.JustPlayed() {
		t.Error("still holding off after the time has passed")
	}
}

// Long enough to outlast the tail of a clip and the room's reply to it, short enough that somebody
// answering an announcement out loud is not ignored.
func TestHowLongTheHoldOffIs(t *testing.T) {
	if afterPlaying < time.Second || afterPlaying > 10*time.Second {
		t.Errorf("afterPlaying is %v, which is not a length anybody meant", afterPlaying)
	}
}
