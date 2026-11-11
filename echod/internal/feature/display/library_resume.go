//go:build !dot && !spot

package display

import (
	"fmt"
	"log/slog"
	"math"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/musicassistant"
)

// Where an audiobook was left, so the library can offer to go on from there.
//
// Music Assistant keeps no place for a book played through Home Assistant, so the device keeps its own:
// while a book the library started plays, its player is asked now and then where it is, and the place
// is saved when it pauses, stops, gives way to something else, and every few minutes in between (a
// power cut loses no more than that). The saved places live in the device's settings, which are
// written whole, so they are not saved on every look. A book played to within a couple of minutes of
// its end is done, and its place goes.

// libPlayerState is a player's state and attributes from Home Assistant; a variable for the tests.
var libPlayerState = func(entity string) (hass.State, error) { return hass.Get().State(entity) }

// libResumeSpot is where the book uri was left, if it was; a variable for the tests.
var libResumeSpot = func(uri string) (int, bool) { return config.Get().BookPlaces.Spot(uri) }

// libSaveSpot and libForgetSpot keep and let go of a book's place; variables for the tests.
var (
	libSaveSpot = func(uri string, seconds int) {
		if err := config.Set().BookPlaces().Save(uri, seconds, time.Now()); err != nil {
			slog.Warn("dashboard music library resume save", "err", err)
		}
	}
	libForgetSpot = func(uri string) {
		if err := config.Set().BookPlaces().Forget(uri); err != nil {
			slog.Warn("dashboard music library resume forget", "err", err)
		}
	}
)

const (
	libResumeMin    = 60  // seconds in: less is not worth offering to go on from
	libFinishedNear = 120 // seconds from the end: a book played this far is done
)

// How a playing book is watched; variables for the tests.
var (
	libWatchFirst     = 5 * time.Second
	libWatchEvery     = 15 * time.Second
	libWatchSaveEvery = 5 * time.Minute
	libWatchPausedFor = time.Hour // a book paused this long is let be; its place is already saved
)

// The move to a spot in a book: it waits a moment, so a player still playing the last thing is not
// taken for this one, looks for the player to say it is playing for a few seconds, and moves the book
// either way. Variables for the tests.
var (
	libSeekFirst = time.Second
	libSeekPoll  = 250 * time.Millisecond
	libSeekTries = 24 // about six seconds
)

// libBookWatch is the watch in force: a new one ends the last, which saves its place as it goes.
var libBookWatch atomic.Uint64

// libBookLookNow asks the watch to look at the player now rather than at its next turn: an audiobook's
// page just opened, and a book paused a moment ago should offer to resume at once.
var libBookLookNow = make(chan struct{}, 1)

// libCheckBookNow asks the watch, if there is one, to look now.
func libCheckBookNow() {
	select {
	case libBookLookNow <- struct{}{}:
	default:
	}
}

// libPlayFrom plays the book from its start, then moves it to start seconds in through Home
// Assistant's seek on the same player. Music Assistant's own seek needs a token with more rights than
// the device's, so Home Assistant is asked instead.
func (d *Display) libPlayFrom(l *librarySheet, book musicassistant.Entry, start float64, lit int) {
	if l.target == "" || book.URI == "" {
		return
	}
	target := l.target
	d.libPlayTapped(l, book, "audiobook", false, lit, libEnqueueReplace)
	if start <= 0 {
		return
	}
	go func() {
		time.Sleep(libSeekFirst)
		for range libSeekTries {
			if st, err := libPlayerState(target); err == nil && st.State == "playing" {
				break
			}
			time.Sleep(libSeekPoll)
		}
		libTap(dashboard.Action{Entity: target, Service: "media_player.media_seek", Data: map[string]any{"seek_position": math.Floor(start)}})
	}()
}

// libWatchBook watches the book uri, duration seconds long (0 when not known), play on target, and
// keeps its place as it goes. It ends when another book is started, when the player stops or turns
// to something else, or when it has been paused a long while.
func libWatchBook(target, uri string, duration int) {
	gen := libBookWatch.Add(1)
	go func() {
		time.Sleep(libWatchFirst)
		last, saved := -1.0, -1.0
		lastSave := time.Now()
		var pausedSince time.Time
		quiet := 0
		keep := func() {
			if last >= 0 && last != saved {
				libKeepPlace(uri, last, duration)
				saved, lastSave = last, time.Now()
			}
		}
		for {
			if libBookWatch.Load() != gen {
				keep()
				return
			}
			st, err := libPlayerState(target)
			now := time.Now()
			switch {
			case err != nil:
				quiet++
			case st.State == "playing" || st.State == "paused" || st.State == "buffering":
				if !libSameLength(st.Attributes, duration) {
					keep() // something else is on now: this book's place is where it was left
					return
				}
				if pos, ok := libPositionOf(st.Attributes, st.State, now); ok {
					last = pos
				}
				quiet = 0
				if st.State == "paused" {
					if pausedSince.IsZero() {
						pausedSince = now
					}
					keep()
					if now.Sub(pausedSince) >= libWatchPausedFor {
						return
					}
				} else {
					pausedSince = time.Time{}
					if now.Sub(lastSave) >= libWatchSaveEvery {
						keep()
					}
				}
			default:
				quiet++
			}
			if quiet >= 2 {
				keep()
				return
			}
			select {
			case <-time.After(libWatchEvery):
			case <-libBookLookNow:
			}
		}
	}()
}

// libKeepPlace saves where the book was left, or lets its place go when it was played to the end.
func libKeepPlace(uri string, seconds float64, duration int) {
	if duration > 0 && int(seconds) >= duration-libFinishedNear {
		libForgetSpot(uri)
		return
	}
	if seconds < libResumeMin {
		return // too early to be worth offering; an older place, if there is one, stands
	}
	libSaveSpot(uri, int(seconds))
}

// libPositionOf is where a player is in what it plays, in seconds: Home Assistant's media_position,
// which is as of media_position_updated_at, moved on by the time since while it plays.
func libPositionOf(a map[string]any, state string, now time.Time) (float64, bool) {
	pos, ok := a["media_position"].(float64)
	if !ok {
		return 0, false
	}
	if state == "playing" {
		if s, ok := a["media_position_updated_at"].(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil && now.After(t) {
				pos += now.Sub(t).Seconds()
			}
		}
	}
	return pos, true
}

// libSameLength is whether what the player has on is as long as the book, give or take, when both are
// known: something else put on by voice or another screen is not. Not knowing is taken as the same.
func libSameLength(a map[string]any, duration int) bool {
	d, ok := a["media_duration"].(float64)
	if !ok || d <= 0 || duration <= 0 {
		return true
	}
	return math.Abs(d-float64(duration)) <= 5
}

// libClock is seconds as a time into a book: 1:02:03, or 4:05 under an hour.
func libClock(seconds int) string {
	h, m, s := seconds/3600, seconds/60%60, seconds%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// libResumeOffer is where an audiobook page offers to go on from, if it does.
func libResumeOffer(p *libPage) (int, bool) {
	if p == nil || p.what != libAudiobook || p.entry.URI == "" {
		return 0, false
	}
	s, ok := libResumeSpot(p.entry.URI)
	return s, ok && s >= libResumeMin
}
