//go:build !dot && !spot

package display

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// The Music Assistant tile opens the library, except while this device plays and the tile is for its
// own player: then it opens Now Playing, which has the library's button.
func TestTheMusicTileOpensNowPlayingWhilePlaying(t *testing.T) {
	noLibrary(t)
	prev := tileMusicIsOwn
	t.Cleanup(func() { tileMusicIsOwn = prev })
	tile := &dashTile{action: &dashboard.Action{View: dashboard.LibraryPath, Entity: "media_player.ma_echo_show"}}
	for _, c := range []struct {
		name           string
		playing, own   bool
		wantNowPlaying bool
	}{
		{"nothing playing", false, true, false},
		{"playing here", true, true, true},
		{"the tile is for another speaker", true, false, false},
	} {
		tileMusicIsOwn = func(string) bool { return c.own }
		d := &Display{poke: make(chan struct{}, 1)}
		d.dash, d.away = true, true
		if c.playing {
			d.musicPendingStart(false)
		}
		d.tileAction(tile)
		if c.wantNowPlaying {
			waitUntil(t, c.name+": the dashboard put away", func() bool {
				d.mu.Lock()
				defer d.mu.Unlock()
				return !d.dash && !d.away
			})
			if d.libraryViewNow() != nil {
				t.Errorf("%s: the library opened", c.name)
			}
			continue
		}
		waitUntil(t, c.name+": the library opened", func() bool { return d.libraryViewNow() != nil })
	}
}
