//go:build !dot && !spot

package display

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// musicPendingFor is how long Now Playing is held up for music that was asked for and has not shown yet:
// a play from the library, or a resume. Past it with nothing playing the clock comes back, so a start
// that never comes does not leave the page up for good.
const musicPendingFor = 8 * time.Second

// musicPendingStart holds Now Playing up for music about to play. fresh is for music that was just
// chosen (the page then says it is starting rather than showing the track that was playing before); a
// resume keeps the page as it is.
func (d *Display) musicPendingStart(fresh bool) {
	d.mu.Lock()
	d.musicPendingAt, d.musicPendingFresh = time.Now(), fresh
	d.musicPendingStops = media.Get().StopCount()
	d.mu.Unlock()
}

// musicPendingEnd lets go of the hold, when the music is ended on purpose.
func (d *Display) musicPendingEnd() {
	d.mu.Lock()
	d.musicPendingAt, d.musicPendingFresh = time.Time{}, false
	d.mu.Unlock()
}

// musicPending is whether Now Playing is being held up for music on its way, and whether that music was
// freshly chosen.
func (d *Display) musicPending() (pending, fresh bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.musicPendingAt.IsZero() || time.Since(d.musicPendingAt) >= musicPendingFor {
		return false, false
	}
	// A stop since the hold began (the Stop buttons, Home Assistant, a server that stayed stopped)
	// lets go of it. Playing, or a gap in it, does not: a play that replaces a song looks like both.
	if media.Get().StopCount() != d.musicPendingStops {
		d.musicPendingAt, d.musicPendingFresh = time.Time{}, false
		return false, false
	}
	return true, d.musicPendingFresh
}

// musicAudible is whether anything is playing, paused or carried here yet.
func musicAudible() bool {
	if media.Get().ExternalShowing() || home.Following() {
		return true
	}
	if _, _, _, ok := media.Get().Held(); ok {
		return true
	}
	playing, paused := media.Get().Playing()
	return playing || paused
}

// toggleMusic is the play and pause button. A tap on play while paused holds the page up until the
// stream is back, which takes a second or two: the clock never shows in between.
func (d *Display) toggleMusic() {
	d.mu.Lock()
	paused := d.showingPaused
	d.mu.Unlock()
	if paused {
		d.musicPendingStart(false)
	}
	transport(media.TransportToggle)
}

// showMusicPage puts away what is over the idle page (the dashboard, the library over it) and brings
// Now Playing back, even one put away for the track: what the library's Now Playing button and the
// Music Assistant tile do while something plays.
func (d *Display) showMusicPage() {
	d.mu.Lock()
	d.dashLibrary, d.dashMedia = nil, nil
	d.away, d.awayTrack, d.awayStation, d.awayPlaying = false, "", "", false
	d.musicQuiet.reopened(time.Now())
	d.mu.Unlock()
	d.closeDashboard()
}
