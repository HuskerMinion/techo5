package home

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// nowRoom is what the player says, set by a test and read by the sensors' loop in its place, and how
// many times the loop has read it.
type nowRoom struct {
	mu    sync.Mutex
	s     playingSnapshot
	reads int
}

func (r *nowRoom) read() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reads
}

func (r *nowRoom) set(s playingSnapshot) {
	r.mu.Lock()
	r.s = s
	r.mu.Unlock()
}

// nowWrites is every write to the sensors, by object id, in order.
type nowWrites struct {
	mu  sync.Mutex
	got map[string][]string
}

func (w *nowWrites) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, vs := range w.got {
		n += len(vs)
	}
	return n
}

// shown is what the sensors say now: the last write to each.
func (w *nowWrites) shown() nowPlaying {
	w.mu.Lock()
	defer w.mu.Unlock()
	last := func(id string) string {
		if vs := w.got[id]; len(vs) > 0 {
			return vs[len(vs)-1]
		}
		return ""
	}
	return nowPlaying{last("now_playing_source"), last("now_playing_title"), last("now_playing_artist"), last("now_playing_album")}
}

// recordNowPlaying keeps every write to the sensors, of any Feature, until the test is over.
func recordNowPlaying(t *testing.T) *nowWrites {
	t.Helper()
	writes := &nowWrites{got: map[string][]string{}}
	wasSet := setNowPlaying
	setNowPlaying = func(txt *esphome.TextSensor, v string) {
		writes.mu.Lock()
		writes.got[txt.ObjectID] = append(writes.got[txt.ObjectID], v)
		writes.mu.Unlock()
		txt.Set(v)
	}
	t.Cleanup(func() { setNowPlaying = wasSet })
	return writes
}

// nowPlayingFeature is a Feature with only the now-playing sensors, the room they read and their writes.
func nowPlayingFeature(t *testing.T) (*Feature, *nowRoom, *nowWrites) {
	t.Helper()
	f := &Feature{nowPoke: make(chan struct{}, 1)}
	f.buildNowPlayingSensors()
	room, writes := &nowRoom{}, recordNowPlaying(t)
	wasSnapshot, wasSettle, wasMost, wasEvery := takeSnapshot, nowSettle, nowSettleMost, nowEvery
	takeSnapshot = func() playingSnapshot {
		room.mu.Lock()
		defer room.mu.Unlock()
		room.reads++
		return room.s
	}
	t.Cleanup(func() { takeSnapshot, nowSettle, nowSettleMost, nowEvery = wasSnapshot, wasSettle, wasMost, wasEvery })
	return f, room, writes
}

// runNowPlaying runs the sensors' loop until the test is over.
func runNowPlaying(t *testing.T, f *Feature) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.nowPlayingLoop(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// aPlayer says what a test tells it to, each field its own, so a field read into the wrong place shows.
type aPlayer struct {
	recv    [4]string
	carried bool
	track   [3]string
	held    [3]string
	holding bool
}

func (p aPlayer) ReceivedTrack() (from, title, artist, album string) {
	return p.recv[0], p.recv[1], p.recv[2], p.recv[3]
}
func (p aPlayer) Carried() bool                        { return p.carried }
func (p aPlayer) Track() (title, artist, album string) { return p.track[0], p.track[1], p.track[2] }
func (p aPlayer) Held() (title, artist, album string, ok bool) {
	return p.held[0], p.held[1], p.held[2], p.holding
}

// What the sensors read is what the player says, each field where it belongs.
func TestTheSnapshotIsWhatThePlayerSays(t *testing.T) {
	got := snapshotOf(aPlayer{
		recv:    [4]string{DLNAName, "Africa", "Toto", "Toto IV"},
		carried: true,
		track:   [3]string{"Hold the Line", "Toto (band)", "Toto (album)"},
		held:    [3]string{"Rosanna", "David Paich", "Toto IV (2018)"},
		holding: true,
	})
	want := playingSnapshot{
		recvFrom: DLNAName, recvTitle: "Africa", recvArtist: "Toto", recvAlbum: "Toto IV",
		carried:  true,
		extTitle: "Hold the Line", extArtist: "Toto (band)", extAlbum: "Toto (album)",
		held:      true,
		heldTitle: "Rosanna", heldArtist: "David Paich", heldAlbum: "Toto IV (2018)",
	}
	if got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
	if got := snapshotOf(aPlayer{}); got != (playingSnapshot{}) {
		t.Errorf("with nothing said: %+v, want nothing", got)
	}
}

// Each receiver's song is named with the receiver, as the now-playing page names it.
func TestWhatAReceiverPlaysIsNowPlaying(t *testing.T) {
	for _, from := range []string{AirPlayName, SpotifyName, DLNAName} {
		got := nowPlayingOf(playingSnapshot{recvFrom: from, recvTitle: "Africa", recvArtist: "Toto", recvAlbum: "Toto IV"})
		if want := (nowPlaying{from, "Africa", "Toto", "Toto IV"}); got != want {
			t.Errorf("%s: %+v, want %+v", from, got, want)
		}
	}
}

func TestMusicAssistantIsNowPlayingWhileItCarries(t *testing.T) {
	got := nowPlayingOf(playingSnapshot{carried: true, extTitle: "Hold the Line", extArtist: "Toto", extAlbum: "Toto"})
	if want := (nowPlaying{"Music Assistant", "Hold the Line", "Toto", "Toto"}); got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
}

// Music Assistant lets go of a track paused from here, and the page keeps it with play on it; so do
// the sensors.
func TestATrackPausedFromHereStaysNowPlaying(t *testing.T) {
	got := nowPlayingOf(playingSnapshot{held: true, heldTitle: "Rosanna", heldArtist: "Toto", heldAlbum: "Toto IV"})
	if want := (nowPlaying{"Music Assistant", "Rosanna", "Toto", "Toto IV"}); got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
}

func TestAReceiverWinsOverMusicAssistant(t *testing.T) {
	got := nowPlayingOf(playingSnapshot{
		recvFrom: AirPlayName, recvTitle: "Africa", recvArtist: "Toto", recvAlbum: "Toto IV",
		carried: true, extTitle: "Hold the Line", held: true, heldTitle: "Rosanna",
	})
	if want := (nowPlaying{AirPlayName, "Africa", "Toto", "Toto IV"}); got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
}

// What the page shows while Music Assistant carries a track is that track, not an older one paused from
// here before it.
func TestWhatMusicAssistantCarriesWinsOverATrackPausedFromHere(t *testing.T) {
	got := nowPlayingOf(playingSnapshot{
		carried: true, extTitle: "Hold the Line", extArtist: "Toto", extAlbum: "Toto",
		held: true, heldTitle: "Rosanna", heldArtist: "Toto", heldAlbum: "Toto IV",
	})
	if want := (nowPlaying{"Music Assistant", "Hold the Line", "Toto", "Toto"}); got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
}

// Music Assistant clears a track's name with its pause and lets go of the speaker a moment later. In
// between the track paused from here is named, rather than nothing and then the paused track again.
func TestATrackPausedFromHereIsNamedWhileMusicAssistantLetsGo(t *testing.T) {
	got := nowPlayingOf(playingSnapshot{carried: true, held: true, heldTitle: "Rosanna", heldArtist: "Toto", heldAlbum: "Toto IV"})
	if want := (nowPlaying{"Music Assistant", "Rosanna", "Toto", "Toto IV"}); got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
	if got := nowPlayingOf(playingSnapshot{carried: true}); got != (nowPlaying{source: "Music Assistant"}) {
		t.Errorf("with nothing held: %+v, want Music Assistant with no song", got)
	}
}

// A phone over Bluetooth names no song, a video has its own title, and the radio has its own sensors:
// none of them is named here.
func TestBluetoothVideoAndTheRadioAreNotNowPlaying(t *testing.T) {
	for _, from := range []string{"Bluetooth: A phone", "Video", ""} {
		if got := nowPlayingOf(playingSnapshot{recvFrom: from, recvTitle: "Africa"}); got != (nowPlaying{}) {
			t.Errorf("%q: %+v, want nothing", from, got)
		}
	}
}

// Once nothing plays every sensor is emptied; and at a start every one is written, though each already
// holds "", or Home Assistant would have no state for them at all.
func TestNothingPlayingEmptiesEverySensor(t *testing.T) {
	f, _, writes := nowPlayingFeature(t)
	if n := f.showNowPlaying(nil, nowPlaying{}); n != 4 {
		t.Errorf("at a start: %d written, want all 4", n)
	}
	last := nowPlaying{AirPlayName, "Africa", "Toto", "Toto IV"}
	if n := f.showNowPlaying(&last, nowPlaying{}); n != 4 {
		t.Errorf("once nothing plays: %d written, want all 4", n)
	}
	if got := writes.shown(); got != (nowPlaying{}) {
		t.Errorf("the sensors say %+v, want nothing", got)
	}
	if last != (nowPlaying{}) {
		t.Errorf("last = %+v, want nothing", last)
	}
}

// Every write is a row in Home Assistant's history and logbook, so only what changed is written.
func TestOnlyWhatChangedIsToldToHomeAssistant(t *testing.T) {
	f, _, writes := nowPlayingFeature(t)
	var last nowPlaying
	song := nowPlaying{AirPlayName, "Africa", "Toto", "Toto IV"}
	if n := f.showNowPlaying(&last, song); n != 4 {
		t.Errorf("a song: %d written, want 4", n)
	}
	if n := f.showNowPlaying(&last, song); n != 0 {
		t.Errorf("the same song again: %d written, want none", n)
	}
	next := nowPlaying{AirPlayName, "Rosanna", "Toto", "Toto IV"}
	if n := f.showNowPlaying(&last, next); n != 1 {
		t.Errorf("the next song on the album: %d written, want only the title", n)
	}
	if got := writes.shown(); got != next {
		t.Errorf("the sensors say %+v, want %+v", got, next)
	}
}

// Home Assistant reads a state longer than 255 characters as unknown, so a longer one is cut to that,
// whole characters only.
func TestNowPlayingIsCutToWhatHomeAssistantKeeps(t *testing.T) {
	got := cutState(strings.Repeat("è", 300))
	if n := utf8.RuneCountInString(got); n != maxState || !utf8.ValidString(got) {
		t.Errorf("cut to %d characters, valid %v; want %d, valid", n, utf8.ValidString(got), maxState)
	}
	if got := cutState("Africa"); got != "Africa" {
		t.Errorf("a short one: %q, want it as it was", got)
	}
	if long := strings.Repeat("a", maxState); cutState(long) != long {
		t.Error("one of exactly the limit was cut")
	}
}

// AirPlay names the next song a field at a time, each a change of its own, and keeps the last song's
// artist and album until it names the new ones. The sensors wait for the changes to stop and read the
// player once, with the whole song, rather than writing the new title over the last song's artist.
func TestASongToldFieldByFieldIsShownOnce(t *testing.T) {
	f, room, writes := nowPlayingFeature(t)
	nowSettle = 300 * time.Millisecond
	runNowPlaying(t, f)

	room.set(playingSnapshot{recvFrom: AirPlayName, recvTitle: "Billie Jean", recvArtist: "Michael Jackson", recvAlbum: "Thriller"})
	f.pokeNowPlaying()
	if !waitFor(func() bool { return writes.shown().album == "Thriller" }) {
		t.Fatalf("the sensors say %+v, want the first song", writes.shown())
	}
	reads := room.read()

	for _, s := range []playingSnapshot{
		{recvFrom: AirPlayName, recvTitle: "Africa", recvArtist: "Michael Jackson", recvAlbum: "Thriller"},
		{recvFrom: AirPlayName, recvTitle: "Africa", recvArtist: "Toto", recvAlbum: "Thriller"},
		{recvFrom: AirPlayName, recvTitle: "Africa", recvArtist: "Toto", recvAlbum: "Toto IV"},
	} {
		room.set(s)
		f.pokeNowPlaying()
		time.Sleep(nowSettle / 10) // well within nowSettle between two, so a slow machine keeps them one change
	}

	want := nowPlaying{AirPlayName, "Africa", "Toto", "Toto IV"}
	if !waitFor(func() bool { return writes.shown() == want }) {
		t.Fatalf("the sensors say %+v, want %+v", writes.shown(), want)
	}
	time.Sleep(2 * nowSettle) // nothing more comes of the pokes
	if n := room.read() - reads; n != 1 {
		t.Errorf("the player read %d times for the song, want once", n)
	}
	writes.mu.Lock()
	defer writes.mu.Unlock()
	if got := writes.got["now_playing_artist"]; len(got) != 2 || got[1] != "Toto" {
		t.Errorf("the artist written %q, want Michael Jackson then Toto", got)
	}
}

// A player that never stops changing does not keep the sensors waiting on it: they are told after
// nowSettleMost at the latest, while the changes still come.
func TestASongIsShownThoughThePlayerNeverStopsChanging(t *testing.T) {
	f, room, writes := nowPlayingFeature(t)
	nowSettle, nowSettleMost = 50*time.Millisecond, 150*time.Millisecond
	runNowPlaying(t, f)

	room.set(playingSnapshot{recvFrom: AirPlayName, recvTitle: "Africa", recvArtist: "Toto", recvAlbum: "Toto IV"})
	shown := false
	for until := time.Now().Add(2 * time.Second); time.Now().Before(until); time.Sleep(20 * time.Millisecond) {
		f.pokeNowPlaying() // more often than nowSettle
		if writes.shown().title == "Africa" {
			shown = true
			break
		}
	}
	if !shown {
		t.Errorf("the sensors say %+v while the player kept changing, want the song", writes.shown())
	}
}

// AirPlay hands on a field of up to 1024 bytes, and what the loop writes is cut to what Home Assistant
// keeps.
func TestALongTitleIsWrittenCut(t *testing.T) {
	f, room, writes := nowPlayingFeature(t)
	nowSettle = time.Millisecond
	runNowPlaying(t, f)

	room.set(playingSnapshot{recvFrom: AirPlayName, recvTitle: strings.Repeat("è", 300)})
	f.pokeNowPlaying()
	if !waitFor(func() bool { return writes.shown().title != "" }) {
		t.Fatal("the title was never written")
	}
	if n := utf8.RuneCountInString(writes.shown().title); n != maxState {
		t.Errorf("written with %d characters, want %d", n, maxState)
	}
}

// Every change to what the player plays reaches the sensors' loop as a poke, without blocking the player.
func TestTheSensorsHearOfEveryChangeToWhatPlays(t *testing.T) {
	f := Get()
	drain := func() {
		select {
		case <-f.nowPoke:
		default:
		}
	}
	drain()
	t.Cleanup(drain)
	media.Get().OnNowPlaying.Emit(struct{}{})
	if len(f.nowPoke) != 1 {
		t.Fatal("a change to what plays did not poke the sensors")
	}
	media.Get().OnNowPlaying.Emit(struct{}{}) // one already waiting: dropped, not blocked on
}

// At a start every sensor is written, though each already holds "", or Home Assistant would read them
// unknown until the first song.
func TestEverySensorIsWrittenAtAStart(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	writes := recordNowPlaying(t)
	Get().Restore(config.Get())
	for _, id := range []string{"now_playing_source", "now_playing_title", "now_playing_artist", "now_playing_album"} {
		if got := writes.got[id]; len(got) != 1 || got[0] != "" {
			t.Errorf("%s written %q at a start, want empty once", id, got)
		}
	}
}

// A Music Assistant track paused from here stops being one by time alone, with nothing to say so: the
// sensors are looked at again now and then, and let it go.
func TestAPausedTrackThatRunsOutIsLetGo(t *testing.T) {
	f, room, writes := nowPlayingFeature(t)
	nowSettle, nowEvery = time.Millisecond, 20*time.Millisecond
	runNowPlaying(t, f)

	room.set(playingSnapshot{held: true, heldTitle: "Rosanna", heldArtist: "Toto", heldAlbum: "Toto IV"})
	f.pokeNowPlaying()
	if !waitFor(func() bool { return writes.shown().title == "Rosanna" }) {
		t.Fatalf("the sensors say %+v, want the paused track", writes.shown())
	}
	room.set(playingSnapshot{}) // held for stoppedFor, and no more
	if !waitFor(func() bool { return writes.shown() == (nowPlaying{}) }) {
		t.Errorf("the sensors say %+v, want nothing once the track ran out", writes.shown())
	}
}
