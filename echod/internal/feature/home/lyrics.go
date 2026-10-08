package home

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/lyrics"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Lyrics: the words of the song on Now Playing, the line being sung and the one after it, in time with
// the music. Off until it is turned on (the "Lyrics" switch, or the row under Display), because it is the
// one part of Now Playing that asks outside the house: the song's title and artist go to LRCLIB
// (lib/lyrics). Words can only be kept in time when the source says how far into the song it is, which
// Music Assistant and Home Assistant's other players do and a station, AirPlay and Spotify do not; for
// those nothing is shown.

// lyricsKept is how many songs' words (or the lack of them) are remembered, so a song played again, or
// the page coming back to one, does not ask again.
const lyricsKept = 16

// lyricsRetry is how long a failed lookup (the network, not "no words") waits before it is tried again.
const lyricsRetry = 2 * time.Minute

type lyricsEntry struct {
	lines  []lyrics.Line // nil: none for this song
	failed time.Time     // the lookup failed rather than found nothing; zero otherwise
}

var lyricsCache struct {
	mu    sync.Mutex
	songs map[string]lyricsEntry
	order []string
	busy  map[string]bool
}

func (f *Feature) buildLyricsSwitch() {
	f.lyricsSw = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "lyrics",
			Name:     "Lyrics",
			Icon:     "mdi:script-text-outline",
			Category: esphome.CategoryConfig,
		},
		OnCommand: func(on bool) { f.SetLyricsOn(on) },
	}
}

// LyricsOn is whether the words are shown.
func LyricsOn() bool { return config.Get().Home.Lyrics }

// SetLyricsOn saves the choice and shows it in Home Assistant.
func (f *Feature) SetLyricsOn(on bool) {
	if err := config.Set().Home().Lyrics(on); err != nil {
		slog.Error("saving the lyrics switch failed", "err", err)
		return
	}
	f.lyricsSw.Set(on)
	f.Changed.Emit(struct{}{})
}

// Lyric is what the page shows of the words: the line being sung (empty before the first, or in a
// gap), the next, and how long until the next starts (0 when nothing more is due, so nothing needs
// redrawing for the words).
type Lyric struct {
	Line, Next string
	In         time.Duration
}

// LyricNow is the words for the song on the page at now, and false when there are none to show: the
// switch is off, the source does not say where the song is, or the song has no timed words. A song not
// yet looked up is looked up, and shows once the answer is in.
func (f *Feature) LyricNow(r Radio, now time.Time) (Lyric, bool) {
	if !LyricsOn() || r.Title == "" || r.Artist == "" {
		return Lyric{}, false
	}
	pos, ok := trackPosition(r)
	if !ok {
		return Lyric{}, false
	}
	key := r.Title + "\x00" + r.Artist
	lines, known := cachedLyrics(key)
	if !known {
		f.lookUpLyrics(key, r.Title, r.Artist, r.Album, pos.Dur)
		return Lyric{}, false
	}
	if len(lines) == 0 {
		return Lyric{}, false
	}
	at := pos.Now(now)
	cur, next, nextAt := lyrics.At(lines, at)
	l := Lyric{Line: cur, Next: next}
	if next != "" && pos.Rate > 0 {
		l.In = time.Duration(float64(nextAt-at) / pos.Rate)
	}
	return l, true
}

// trackPosition is where the song on the page is, from whoever is playing it.
func trackPosition(r Radio) (media.Position, bool) {
	if r.Followed {
		followed.mu.Lock()
		defer followed.mu.Unlock()
		p := followed.pos
		if !followed.posSet || p.Title != r.Title {
			return media.Position{}, false
		}
		return p, true
	}
	return media.TrackPosition(r.Title)
}

// cachedLyrics is the words remembered for a song, and whether there is an answer to remember: a failed
// lookup counts as none until it is due to be tried again.
func cachedLyrics(key string) ([]lyrics.Line, bool) {
	lyricsCache.mu.Lock()
	defer lyricsCache.mu.Unlock()
	e, ok := lyricsCache.songs[key]
	if !ok {
		return nil, false
	}
	if !e.failed.IsZero() && time.Since(e.failed) > lyricsRetry {
		return nil, false
	}
	return e.lines, true
}

func (f *Feature) lookUpLyrics(key, title, artist, album string, dur time.Duration) {
	lyricsCache.mu.Lock()
	if lyricsCache.busy == nil {
		lyricsCache.busy = map[string]bool{}
	}
	if lyricsCache.busy[key] {
		lyricsCache.mu.Unlock()
		return
	}
	lyricsCache.busy[key] = true
	lyricsCache.mu.Unlock()
	safe.Go("lyrics", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		lines, err := lyrics.Find(ctx, title, artist, album, dur)
		e := lyricsEntry{lines: lines}
		switch {
		case err == nil:
		case errors.Is(err, lyrics.ErrNone):
			e.lines = nil
		default:
			slog.Info("lyrics: looking up the song failed", "err", err)
			e.failed = time.Now()
		}
		rememberLyrics(key, e)
		f.Changed.Emit(struct{}{})
	})
}

func rememberLyrics(key string, e lyricsEntry) {
	lyricsCache.mu.Lock()
	defer lyricsCache.mu.Unlock()
	delete(lyricsCache.busy, key)
	if lyricsCache.songs == nil {
		lyricsCache.songs = map[string]lyricsEntry{}
	}
	if _, ok := lyricsCache.songs[key]; !ok {
		lyricsCache.order = append(lyricsCache.order, key)
	}
	lyricsCache.songs[key] = e
	for len(lyricsCache.order) > lyricsKept {
		delete(lyricsCache.songs, lyricsCache.order[0])
		lyricsCache.order = lyricsCache.order[1:]
	}
}
