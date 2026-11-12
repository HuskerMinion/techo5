//go:build !dot && !spot

package display

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/musicassistant"
)

// Where an audiobook or a podcast episode was left, and playing one from a place of its own.
//
// Music Assistant keeps the place for each of its users (the device's token's user is whose it is), on
// every device and in its own web page alike, and a play of a book or an episode goes on from there by
// itself: Play is a play as any other. The pages only show the place (Resume at), from the item as the
// server sends it, and offer to start over or to play from a chapter, which Music Assistant is asked to
// do itself. The device kept places of its own until #524; config.BookPlaces is no longer read.

// libPlayerState is a player's state and attributes from Home Assistant; a variable for the tests.
var libPlayerState = func(entity string) (hass.State, error) { return hass.Get().State(entity) }

// The move to a spot through Home Assistant, when Music Assistant would not start there itself: it
// waits a moment, so a player still playing the last thing is not taken for this one, looks for the
// player to say it is playing for a few seconds, and moves either way. Variables for the tests.
var (
	libSeekFirst = time.Second
	libSeekPoll  = 250 * time.Millisecond
	libSeekTries = 24 // about six seconds
)

// libResumeOffer is where an audiobook's or an episode's page offers to go on from, in seconds, if it
// was left part way.
func libResumeOffer(p *libPage) (int, bool) {
	if p == nil || (p.what != libAudiobook && p.what != libEpisode) || p.entry.URI == "" || p.entry.FullyPlayed {
		return 0, false
	}
	s := p.entry.ResumeMS / 1000
	return s, s > 0
}

// libStartOver plays the book or episode of page p (of media type kind) from its beginning: a book from
// its first chapter (the one its page lists first, or else the server's chapter 0, which is the first
// when it numbers them from 0; a book without one is refused and so played and moved to its start), an
// episode from its start.
func (d *Display) libStartOver(l *librarySheet, p *libPage, kind string, lit int) {
	chapter := -1
	if kind == "audiobook" {
		chapter = 0
		for _, s := range p.sections {
			if s.kind == "chapter" && len(s.entries) > 0 && s.entries[0].TrackNumber > 0 {
				chapter = s.entries[0].TrackNumber - 1
			}
		}
	}
	d.libPlayAt(l, p.entry, kind, chapter, 0, lit)
}

// libPlayAt plays e (an audiobook or a podcast episode, of media type kind) start seconds in, rather
// than from where it was left: from the start of its chapter at position chapter (a book's, from 0), or
// from its beginning. It lights what was tapped (lit) and leaves the library, as a play does.
//
// Music Assistant is asked to start it there itself, on the library's player's queue. When it cannot
// (a book with no chapters to start from, no queue to name, or a token whose user may not use the
// player: play_media asks for the player itself), Home Assistant plays it, which goes on from where it
// was left, and then moves it to start seconds in.
func (d *Display) libPlayAt(l *librarySheet, e musicassistant.Entry, kind string, chapter int, start float64, lit int) {
	if l.target == "" || e.URI == "" {
		return
	}
	target, from := l.target, l.from
	slog.Info("dashboard music library play at", "type", kind, "chapter", chapter, "at", int(start))
	putOn.Store(target, e.URI)
	go func() {
		if c, set := libClient(); set && (chapter >= 0 || kind == "podcast_episode") {
			if q := libQueueOf(target); q != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				err := c.PlayFrom(ctx, q, e.URI, chapter, chapter < 0)
				cancel()
				if err == nil {
					return
				}
				slog.Warn("dashboard music library play at: through Home Assistant instead", "forbidden", musicassistant.IsForbidden(err), "err", err)
			}
		}
		d.libPlay(target, e, kind, false, libEnqueueReplace)
		libSeekTo(target, start)
	}()
	d.editLibrary(func(n *librarySheet) { n.lit, n.litUntil = lit, time.Now().Add(tapLit) })
	libLeaveAfter(tapLit, func() { d.libLeave(target, from) })
}

// libSeekTo moves what target has just been asked to play to seconds in, through Home Assistant's seek,
// once the player says it plays it (or after a few seconds either way).
func libSeekTo(target string, seconds float64) {
	time.Sleep(libSeekFirst)
	for range libSeekTries {
		if st, err := libPlayerState(target); err == nil && st.State == "playing" {
			break
		}
		time.Sleep(libSeekPoll)
	}
	libTap(dashboard.Action{Entity: target, Service: "media_player.media_seek", Data: map[string]any{"seek_position": math.Floor(seconds)}})
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

// libClock is seconds as a time into a book: 1:02:03, or 4:05 under an hour.
func libClock(seconds int) string {
	h, m, s := seconds/3600, seconds/60%60, seconds%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// libProgress is how far into e this token's user is, from 0 to 1, for the bar on its cover, and
// whether they finished it. Nothing started, or no length to measure it by, is 0.
func libProgress(e musicassistant.Entry) (float64, bool) {
	if e.FullyPlayed {
		return 1, true
	}
	if e.ResumeMS <= 0 || e.Duration <= 0 {
		return 0, false
	}
	return min(float64(e.ResumeMS)/1000/float64(e.Duration), 1), false
}
