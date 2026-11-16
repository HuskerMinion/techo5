//go:build !dot && !spot

package display

import (
	"testing"
	"time"
)

// Play on a paused page holds the page up for the stream coming back; pause does not.
func TestResumeHoldsNowPlayingUp(t *testing.T) {
	d := &Display{}
	d.showingPaused = false
	d.toggleMusic()
	if pending, _ := d.musicPending(); pending {
		t.Error("a pause holds the page up")
	}
	d.showingPaused = true
	d.toggleMusic()
	if pending, fresh := d.musicPending(); !pending || fresh {
		t.Errorf("a resume: pending %v, fresh %v; want held and not fresh", pending, fresh)
	}
}

// Music asked for here keeps Now Playing up while the stream is on its way, for a while and no longer,
// and ending the music lets go at once.
func TestNowPlayingIsHeldForMusicOnItsWay(t *testing.T) {
	d := &Display{}
	if d.nowPlaying() {
		t.Skip("something is playing in the test process")
	}
	d.musicPendingStart(true)
	if !d.nowPlaying() {
		t.Error("the clock shows while the music the library started is on its way")
	}
	if pending, fresh := d.musicPending(); !pending || !fresh {
		t.Errorf("pending %v, fresh %v", pending, fresh)
	}
	d.musicPendingAt = time.Now().Add(-musicPendingFor - time.Second)
	if d.nowPlaying() {
		t.Error("a start that never came holds the page up for good")
	}
	d.musicPendingStart(false)
	d.musicPendingEnd()
	if d.nowPlaying() {
		t.Error("ending the music leaves the page up")
	}
}
