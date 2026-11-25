//go:build !dot && !spot

package display

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// What this player plays itself - AirPlay, a phone over Bluetooth - brings Now Playing up on a device
// with no station list as on one with one: asked only once there was a list, it left the clock up
// under AirPlay with only the footer's word for it (#732).
func TestOwnStreamBringsNowPlayingUpWithoutStations(t *testing.T) {
	d := &Display{}
	if d.nowPlaying() {
		t.Skip("something is playing in the test process")
	}
	if home.Get().Radio().Configured {
		t.Skip("the test process has a station list")
	}
	was := ownPlaying
	t.Cleanup(func() { ownPlaying = was })
	for _, c := range []struct{ playing, paused bool }{{true, false}, {false, true}} {
		ownPlaying = func() (bool, bool) { return c.playing, c.paused }
		if !d.nowPlaying() {
			t.Errorf("playing %v, paused %v: the clock stays up", c.playing, c.paused)
		}
	}
	ownPlaying = func() (bool, bool) { return false, false }
	if d.nowPlaying() {
		t.Error("nothing playing brings the page up")
	}
}
