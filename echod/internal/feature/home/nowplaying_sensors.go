package home

import (
	"context"
	"log/slog"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// What is played to the device, as Home Assistant sensors: where it comes from - AirPlay, Spotify, DLNA
// or Music Assistant - and the song's title, artist and album where it said them. The device's media
// player cannot carry them, as with the radio's (radio_sensors.go). Each is empty while nothing of the
// kind plays: no song name is read from a phone over Bluetooth, a video has its own title, and the
// radio has its own sensors.

// nowPlaying is what the sensors say.
type nowPlaying struct{ source, title, artist, album string }

// playingSnapshot is what the player says at one moment, as much of it as nowPlayingOf needs.
type playingSnapshot struct {
	recvFrom, recvTitle, recvArtist, recvAlbum string
	carried                                    bool
	extTitle, extArtist, extAlbum              string
	held                                       bool
	heldTitle, heldArtist, heldAlbum           string
}

// nowPlayer is as much of media's Player as the sensors read.
type nowPlayer interface {
	ReceivedTrack() (from, title, artist, album string)
	Carried() bool
	Track() (title, artist, album string)
	Held() (title, artist, album string, ok bool)
}

// takeSnapshot reads the player; tests put a room of their own in its place.
var takeSnapshot = func() playingSnapshot { return snapshotOf(media.Get()) }

// snapshotOf is what p says now. Carried rather than ExternalPlaying: Music Assistant left paused,
// still holding the speaker, is not what the room hears while a station of this device's own plays.
func snapshotOf(p nowPlayer) playingSnapshot {
	var s playingSnapshot
	s.recvFrom, s.recvTitle, s.recvArtist, s.recvAlbum = p.ReceivedTrack()
	s.carried = p.Carried()
	s.extTitle, s.extArtist, s.extAlbum = p.Track()
	s.heldTitle, s.heldArtist, s.heldAlbum, s.held = p.Held()
	return s
}

// nowPlayingOf is what the sensors say for s: a receiver's song while its own track plays, then the
// one Music Assistant is carrying, then a Music Assistant track paused or stopped while it played here.
// It is the order the now-playing page is built in, so the two name the same thing - keep it in step
// with carried(). A held track is named as the page shows it, with play on it, for as long as media
// holds it (up to half an hour from the pause), though the media player reads idle all that time.
// Something else played meanwhile hides it only while it plays: once Music Assistant has let go of the
// speaker nothing drops the hold, so the held track is named again when that ends, as on the page.
//
// One difference: Music Assistant clears its track's name with the pause, and lets go of the speaker
// only a couple of seconds later, so for those seconds it carries a track it no longer names. The held
// track is named then too, rather than nothing and then the held track back again, each a row in Home
// Assistant's history.
func nowPlayingOf(s playingSnapshot) nowPlaying {
	if Receiver(s.recvFrom) {
		return nowPlaying{s.recvFrom, s.recvTitle, s.recvArtist, s.recvAlbum}
	}
	if s.carried && (s.extTitle != "" || !s.held) {
		return nowPlaying{"Music Assistant", s.extTitle, s.extArtist, s.extAlbum}
	}
	if s.held {
		return nowPlaying{"Music Assistant", s.heldTitle, s.heldArtist, s.heldAlbum}
	}
	return nowPlaying{}
}

// maxState is the longest state Home Assistant keeps, in characters. A longer one is not cut there:
// it is logged as an error and the sensor reads unknown, and AirPlay hands on fields of up to 1024
// bytes.
const maxState = 255

// cutState is s cut to maxState characters, at a character's edge.
func cutState(s string) string {
	n := 0
	for i := range s {
		if n == maxState {
			return s[:i]
		}
		n++
	}
	return s
}

func (n nowPlaying) cut() nowPlaying {
	return nowPlaying{cutState(n.source), cutState(n.title), cutState(n.artist), cutState(n.album)}
}

func (f *Feature) buildNowPlayingSensors() {
	text := func(id, name, icon string) *esphome.TextSensor {
		return &esphome.TextSensor{Base: esphome.Base{ObjectID: id, Name: name, Icon: icon}}
	}
	f.nowSourceTxt = text("now_playing_source", "Now playing source", "mdi:cast-audio")
	f.nowTitleTxt = text("now_playing_title", "Now playing title", "mdi:music-note")
	f.nowArtistTxt = text("now_playing_artist", "Now playing artist", "mdi:account-music")
	f.nowAlbumTxt = text("now_playing_album", "Now playing album", "mdi:album")
}

// setNowPlaying writes one sensor; tests count the writes.
var setNowPlaying = (*esphome.TextSensor).Set

// showNowPlaying puts next on the sensors and says how many it wrote: only those that differ from last,
// or all of them with no last. The library sends every write to Home Assistant, where each is a row in
// the history and the logbook, so what is the same is left alone. It is decided against what was last
// written rather than what a sensor holds: before its first write a sensor holds "" and Home Assistant
// has nothing at all.
func (f *Feature) showNowPlaying(last *nowPlaying, next nowPlaying) int {
	var was nowPlaying
	if last != nil {
		was = *last
		*last = next
	}
	written := 0
	for _, s := range []struct {
		txt       *esphome.TextSensor
		was, want string
	}{
		{f.nowSourceTxt, was.source, next.source},
		{f.nowTitleTxt, was.title, next.title},
		{f.nowArtistTxt, was.artist, next.artist},
		{f.nowAlbumTxt, was.album, next.album},
	} {
		if last != nil && s.was == s.want {
			continue
		}
		setNowPlaying(s.txt, s.want)
		written++
	}
	return written
}

var (
	// nowSettle is how long the player must go without a change before the sensors are told. AirPlay
	// names a song a field at a time - title, then artist, then album - and without it the history would
	// hold the new title over the last song's artist. An iPhone can send them in separate batches: the
	// album was seen half a second after the title and artist.
	nowSettle = time.Second

	// nowSettleMost is the longest the sensors wait for the changes to stop, so a player that never
	// stops changing is still told.
	nowSettleMost = 3 * time.Second

	// nowEvery is how often the sensors are looked at again with nothing having said to: a Music
	// Assistant track paused from here stops being one by time alone (media's Held).
	nowEvery = 30 * time.Second
)

// pokeNowPlaying asks for the sensors to be looked at again: what plays may have changed. It never
// blocks, since it runs on whatever goroutine changed the player (media's OnNowPlaying).
func (f *Feature) pokeNowPlaying() {
	select {
	case f.nowPoke <- struct{}{}:
	default:
	}
}

// nowPlayingLoop keeps the sensors on what is played to the device. It is the only thing that writes
// them once Restore has emptied them.
func (f *Feature) nowPlayingLoop(ctx context.Context) {
	tick := time.NewTicker(nowEvery)
	defer tick.Stop()
	var last nowPlaying // what Restore left them at
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.nowPoke:
			if !f.settleNowPlaying(ctx) {
				return
			}
		case <-tick.C:
		}
		next := nowPlayingOf(takeSnapshot()).cut()
		if f.showNowPlaying(&last, next) > 0 {
			slog.Info("home: now playing sensors", "source", next.source, "title", next.title, "artist", next.artist, "album", next.album)
		}
	}
}

// settleNowPlaying waits for the pokes to stop: until nowSettle goes by without one, or nowSettleMost
// in all. A poke while it waits is taken here and starts the wait over, and what it was about is in
// what is read next. It reports false once ctx is done.
func (f *Feature) settleNowPlaying(ctx context.Context) bool {
	most := time.NewTimer(nowSettleMost)
	defer most.Stop()
	quiet := time.NewTimer(nowSettle)
	defer quiet.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-most.C:
			return true
		case <-quiet.C:
			return true
		case <-f.nowPoke:
			quiet.Reset(nowSettle)
		}
	}
}
