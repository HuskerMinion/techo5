//go:build !dot && !spot

package display

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// swipedAway is what a swipe right on the now-playing page leaves behind, and what endMusic leaves
// behind after it has stopped the music.
func swipedAway(d *Display, rd home.Radio, playing bool) {
	d.mu.Lock()
	d.away, d.awayTrack, d.awayStation, d.awayPlaying = true, rd.Title, rd.Now, playing
	d.mu.Unlock()
}

// A dismissal is about the track it was made on, not about every later start of it.
//
// The page stayed away for as long as the track's name was the same, and restarting a station on the
// song that was playing is the same track — so the one deliberate press that should never leave you
// looking at the clock was the one that did. The quiet in between is what tells the two apart: while
// the track is stopped the page stays away, and starting it brings the page back.
func TestAPageSwipedAwayComesBackWhenItsTrackIsStartedAgain(t *testing.T) {
	song := home.Radio{Title: "Africa", Now: "KXYZ 101.1"}

	var d Display
	swipedAway(&d, song, true)

	if !d.putAway(song, true, true) {
		t.Fatal("the page came back while the track it was put away on was still playing")
	}
	if !d.putAway(song, true, false) {
		t.Fatal("the page came back the moment the track was paused")
	}
	if d.putAway(song, true, true) {
		t.Error("the page stayed away through the track being started again")
	}
	if d.away {
		t.Error("the start left the away state behind")
	}
}

// The same, from a swipe made while the music was already quiet: the track was not playing when it went,
// so it does not have to stop first for the next start to count as one.
func TestAPageSwipedAwayQuietComesBackOnTheNextStart(t *testing.T) {
	song := home.Radio{Title: "Africa", Now: "KXYZ 101.1"}

	var d Display
	swipedAway(&d, song, false)

	if !d.putAway(song, true, false) {
		t.Fatal("the page came back on its own while nothing was playing")
	}
	if d.putAway(song, true, true) {
		t.Error("the page stayed away through the track being started")
	}
}

// What was already true: the page comes back for the next song, and nothing playing at all clears the
// dismissal so what comes next is not hidden by a gesture made about something else.
func TestThePageComesBackForAnotherTrack(t *testing.T) {
	song := home.Radio{Title: "Africa", Now: "KXYZ 101.1"}
	next := home.Radio{Title: "Rosanna", Now: "KXYZ 101.1"}

	var d Display
	swipedAway(&d, song, true)

	if d.putAway(next, true, true) {
		t.Error("the page stayed away into the next song")
	}
	if d.putAway(song, true, true) {
		t.Error("the dismissal outlived the track it was made on")
	}
}

// A track somebody else is playing that has stopped is still a page, and a dismissal made on it is a
// dismissal of that name: with nothing wanted, there is nothing to keep away.
func TestNothingPlayingClearsTheDismissal(t *testing.T) {
	song := home.Radio{Title: "Africa", Now: "Music Assistant"}

	var d Display
	swipedAway(&d, song, true)

	if d.putAway(song, false, false) {
		t.Error("the page stayed away with nothing playing to keep it away")
	}
	if d.awayPlaying {
		t.Error("the away state was cleared but kept the track as playing")
	}
}
