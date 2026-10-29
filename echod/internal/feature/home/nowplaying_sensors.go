package home

import (
	"context"
	"log/slog"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// What is played to the device, as Home Assistant sensors: where it comes from - AirPlay, Spotify, DLNA,
// Music Assistant, a phone over Bluetooth or the device's own radio - the song's title, artist and album
// where it said them, and whether it is playing or paused. The device's media player cannot carry them,
// as with the radio's (radio_sensors.go). Each is empty, and the state idle, while nothing of the kind
// plays; a video has its own title.

// nowPlaying is what the sensors say.
type nowPlaying struct{ source, title, artist, album, state string }

// The states the state sensor reads. Idle is also what it reads while nothing is named.
const (
	nowStatePlaying = "playing"
	nowStatePaused  = "paused"
	nowStateIdle    = "idle"
)

// nothingPlaying is what the sensors say while nothing plays.
var nothingPlaying = nowPlaying{state: nowStateIdle}

// playingSnapshot is what the player, and the radio, say at one moment, as much of it as nowPlayingOf
// and carried need.
type playingSnapshot struct {
	recvFrom, recvTitle, recvArtist, recvAlbum string
	carried                                    bool
	extTitle, extArtist, extAlbum              string
	held                                       bool
	heldTitle, heldArtist, heldAlbum           string

	// ownPlaying and ownPaused are this player's own track (Player.Playing); screenPlaying and
	// screenPaused are the room's music as a screen shows it (Player.ScreenState).
	ownPlaying, ownPaused       bool
	screenPlaying, screenPaused bool

	// The station the radio sensors name, and what they say is on (meta.go).
	radioStation, radioTitle, radioArtist, radioAlbum string

	// titled is this player's own track named by Home Assistant (media_title), as own.
	titled bool
	own    media.Title
}

// nowPlayer is as much of media's Player as the sensors read.
type nowPlayer interface {
	ReceivedTrack() (from, title, artist, album string)
	Carried() bool
	Track() (title, artist, album string)
	Held() (title, artist, album string, ok bool)
	Playing() (playing, paused bool)
	ScreenState() (playing, paused bool)
	OwnTitle() *media.Title
}

// takeSnapshot reads the player and f's radio; tests put a room of their own in its place.
var takeSnapshot = func(f *Feature) playingSnapshot {
	s := snapshotOf(media.Get())
	f.mu.Lock()
	s.radioStation, s.radioTitle, s.radioArtist, s.radioAlbum = f.meta.station, f.meta.now.Title, f.meta.now.Artist, f.meta.now.Album
	f.mu.Unlock()
	return s
}

// snapshotOf is what p says now. Carried rather than ExternalPlaying: Music Assistant left paused,
// still holding the speaker, is not what the room hears while a station of this device's own plays.
func snapshotOf(p nowPlayer) playingSnapshot {
	var s playingSnapshot
	s.recvFrom, s.recvTitle, s.recvArtist, s.recvAlbum = p.ReceivedTrack()
	s.carried = p.Carried()
	s.extTitle, s.extArtist, s.extAlbum = p.Track()
	s.heldTitle, s.heldArtist, s.heldAlbum, s.held = p.Held()
	s.ownPlaying, s.ownPaused = p.Playing()
	s.screenPlaying, s.screenPaused = p.ScreenState()
	if t := p.OwnTitle(); t != nil {
		s.titled, s.own = true, *t
	}
	return s
}

// playingWhose is whose song the room is playing.
type playingWhose int

const (
	// playingOwn is this device's own track, whatever it is - a station, a phone over Bluetooth, a
	// video - or nothing at all.
	playingOwn playingWhose = iota
	// playingReceived is a receiver's track (AirPlay, Spotify Connect, DLNA: Receiver).
	playingReceived
	// playingCarried is Music Assistant's, carried here.
	playingCarried
	// playingHeld is a Music Assistant track paused or stopped while it played here, which media holds
	// (up to half an hour from the pause) with play on it after Music Assistant has let go of it.
	playingHeld
)

// whosePlaying is whose song the room is playing in s, in the one order both the now-playing page
// (carried) and the sensors (nowPlayingOf) go by: a receiver's while its own track plays, then the one
// Music Assistant carries, then a held one, then this device's own.
//
// heldOverUnnamed is where the two part. Music Assistant clears its track's name with the pause, and
// lets go of the speaker only a couple of seconds later, so for those seconds it carries a track it no
// longer names. With heldOverUnnamed the held track is the room's then, as it is a moment later: the
// sensors ask for that, so as not to write nothing and then the held track back again, each a row in
// Home Assistant's history. The page leaves it Music Assistant's, which it is.
func whosePlaying(s playingSnapshot, heldOverUnnamed bool) playingWhose {
	switch {
	case Receiver(s.recvFrom):
		return playingReceived
	case s.carried && !(heldOverUnnamed && s.held && s.extTitle == ""):
		return playingCarried
	case s.held:
		return playingHeld
	}
	return playingOwn
}

// PlaybackName is what the sensors call a track Home Assistant named without saying where it comes
// from. The screen says it in its own language.
const PlaybackName = "Playback"

// titledSource is where a named track comes from: what it was named as coming from, else fallback.
func titledSource(t media.Title, fallback string) string {
	if t.Source != "" {
		return t.Source
	}
	return fallback
}

// bluetoothFrom is whether a received track's name is a phone's over Bluetooth (feature/btaudio).
func bluetoothFrom(from string) bool { return strings.HasPrefix(from, media.BluetoothPrefix) }

// nowPlayingOf is what the sensors say for s, by whosePlaying. A held track is named as the page shows
// it, with play on it and the state paused, for as long as media holds it, though the media player reads
// idle all that time. Something else played meanwhile hides it only while it plays: once Music Assistant
// has let go of the speaker nothing drops the hold, so the held track is named again when that ends, as
// on the page.
//
// This device's own track is named when Home Assistant named it (media_title), when it is a phone over Bluetooth (with a song only if the device
// was told one; nothing reads one from a phone yet) or a station the radio sensors name, with what they
// say is on.
func nowPlayingOf(s playingSnapshot) nowPlaying {
	var n nowPlaying
	switch whosePlaying(s, true) {
	case playingReceived:
		n = nowPlaying{source: s.recvFrom, title: s.recvTitle, artist: s.recvArtist, album: s.recvAlbum}
	case playingCarried:
		n = nowPlaying{source: "Music Assistant", title: s.extTitle, artist: s.extArtist, album: s.extAlbum}
	case playingHeld:
		n = nowPlaying{source: "Music Assistant", title: s.heldTitle, artist: s.heldArtist, album: s.heldAlbum}
	case playingOwn:
		switch {
		case s.titled:
			// Named by Home Assistant before it was played: what it was named, before anything a station
			// or a phone left behind.
			n = nowPlaying{source: titledSource(s.own, PlaybackName), title: s.own.Title, artist: s.own.Subtitle}
		case bluetoothFrom(s.recvFrom):
			n = nowPlaying{source: "Bluetooth", title: s.recvTitle, artist: s.recvArtist, album: s.recvAlbum}
		case s.recvFrom == "" && (s.ownPlaying || s.ownPaused) && s.radioStation != "":
			// The radio sensors are kept by their own poller, which can lag the stream by a poll: only
			// while this player's own track is on are they what plays.
			n = nowPlaying{source: "Radio", title: s.radioTitle, artist: s.radioArtist, album: s.radioAlbum}
		default:
			return nothingPlaying
		}
	}
	// As a screen shows it: a held track, and Music Assistant letting go of one, read paused.
	switch {
	case s.screenPlaying:
		n.state = nowStatePlaying
	case s.screenPaused:
		n.state = nowStatePaused
	default:
		n.state = nowStateIdle
	}
	return n
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
	return nowPlaying{cutState(n.source), cutState(n.title), cutState(n.artist), cutState(n.album), n.state}
}

func (f *Feature) buildNowPlayingSensors() {
	text := func(id, name, icon string) *esphome.TextSensor {
		return &esphome.TextSensor{Base: esphome.Base{ObjectID: id, Name: name, Icon: icon}}
	}
	f.nowSourceTxt = text("now_playing_source", "Now playing source", "mdi:cast-audio")
	f.nowTitleTxt = text("now_playing_title", "Now playing title", "mdi:music-note")
	f.nowArtistTxt = text("now_playing_artist", "Now playing artist", "mdi:account-music")
	f.nowAlbumTxt = text("now_playing_album", "Now playing album", "mdi:album")
	f.nowStateTxt = text("now_playing_state", "Now playing state", "mdi:play-pause")
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
		{f.nowStateTxt, was.state, next.state},
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

	// nowHeldEvery is how often the sensors are looked at again, with nothing having said to, while they
	// name a held track: it stops being one by time alone (media's Held), which nothing marks. With
	// anything else named, or nothing, only a change wakes the loop.
	nowHeldEvery = 30 * time.Second
)

// pokeNowPlaying asks for the sensors to be looked at again: what plays may have changed. It never
// blocks, since it runs on whatever goroutine changed the player (media's OnNowPlaying) or the radio's
// sensors (showRadio).
func (f *Feature) pokeNowPlaying() {
	select {
	case f.nowPoke <- struct{}{}:
	default:
	}
}

// nowPlayingLoop keeps the sensors on what is played to the device. It is the only thing that writes
// them once Restore has written them idle.
func (f *Feature) nowPlayingLoop(ctx context.Context) {
	// recheck is armed only while a held track is named.
	var recheck *time.Timer
	defer func() {
		if recheck != nil {
			recheck.Stop()
		}
	}()
	last := nothingPlaying // what Restore left them at
	for {
		var rechecked <-chan time.Time
		if recheck != nil {
			rechecked = recheck.C
		}
		select {
		case <-ctx.Done():
			return
		case <-f.nowPoke:
			if !f.settleNowPlaying(ctx) {
				return
			}
		case <-rechecked:
		}
		s := takeSnapshot(f)
		next := nowPlayingOf(s).cut()
		if f.showNowPlaying(&last, next) > 0 {
			// What is playing only at Debug: a log pasted into an issue should not be a listening history.
			slog.Info("home: now playing sensors", "source", next.source, "state", next.state)
			slog.Debug("home: now playing", "title", next.title, "artist", next.artist, "album", next.album)
		}
		if recheck != nil {
			recheck.Stop()
			recheck = nil
		}
		if whosePlaying(s, true) == playingHeld {
			recheck = time.NewTimer(nowHeldEvery)
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
