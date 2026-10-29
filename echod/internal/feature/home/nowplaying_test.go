package home

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/radiometa"
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
	return nowPlaying{last("now_playing_source"), last("now_playing_title"), last("now_playing_artist"), last("now_playing_album"),
		last("now_playing_state")}
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
	wasSnapshot, wasSettle, wasMost, wasEvery := takeSnapshot, nowSettle, nowSettleMost, nowHeldEvery
	takeSnapshot = func(*Feature) playingSnapshot {
		room.mu.Lock()
		defer room.mu.Unlock()
		room.reads++
		return room.s
	}
	t.Cleanup(func() {
		takeSnapshot, nowSettle, nowSettleMost, nowHeldEvery = wasSnapshot, wasSettle, wasMost, wasEvery
	})
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
	own     [2]bool
	screen  [2]bool
	title   *media.Title
}

func (p aPlayer) ReceivedTrack() (from, title, artist, album string) {
	return p.recv[0], p.recv[1], p.recv[2], p.recv[3]
}
func (p aPlayer) Carried() bool                        { return p.carried }
func (p aPlayer) Track() (title, artist, album string) { return p.track[0], p.track[1], p.track[2] }
func (p aPlayer) Held() (title, artist, album string, ok bool) {
	return p.held[0], p.held[1], p.held[2], p.holding
}
func (p aPlayer) Playing() (playing, paused bool)     { return p.own[0], p.own[1] }
func (p aPlayer) ScreenState() (playing, paused bool) { return p.screen[0], p.screen[1] }
func (p aPlayer) OwnTitle() *media.Title              { return p.title }

// What the sensors read is what the player says, each field where it belongs.
func TestTheSnapshotIsWhatThePlayerSays(t *testing.T) {
	got := snapshotOf(aPlayer{
		recv:    [4]string{DLNAName, "Africa", "Toto", "Toto IV"},
		carried: true,
		track:   [3]string{"Hold the Line", "Toto (band)", "Toto (album)"},
		held:    [3]string{"Rosanna", "David Paich", "Toto IV (2018)"},
		holding: true,
		own:     [2]bool{false, true},
		screen:  [2]bool{true, false},
		title:   &media.Title{Title: "Message from Alex", Subtitle: "today 14:12", Source: "Answering machine"},
	})
	want := playingSnapshot{
		recvFrom: DLNAName, recvTitle: "Africa", recvArtist: "Toto", recvAlbum: "Toto IV",
		carried:  true,
		extTitle: "Hold the Line", extArtist: "Toto (band)", extAlbum: "Toto (album)",
		held:      true,
		heldTitle: "Rosanna", heldArtist: "David Paich", heldAlbum: "Toto IV (2018)",
		ownPaused:     true,
		screenPlaying: true,
		titled:        true,
		own:           media.Title{Title: "Message from Alex", Subtitle: "today 14:12", Source: "Answering machine"},
	}
	if got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
	if got := snapshotOf(aPlayer{}); got != (playingSnapshot{}) {
		t.Errorf("with nothing said: %+v, want nothing", got)
	}
}

// The radio is read from what its own sensors are given.
func TestTheSnapshotReadsTheRadio(t *testing.T) {
	f := &Feature{}
	f.meta = meta{station: "Radio Paradise", now: radiometa.Now{Title: "Africa", Artist: "Toto", Album: "Toto IV"}}
	s := takeSnapshot(f)
	if s.radioStation != "Radio Paradise" || s.radioTitle != "Africa" || s.radioArtist != "Toto" || s.radioAlbum != "Toto IV" {
		t.Errorf("%+v, want the radio's station and song", s)
	}
}

// playing is s with the room's music playing, as the screen shows it.
func playing(s playingSnapshot) playingSnapshot {
	s.screenPlaying = true
	return s
}

// Each receiver's song is named with the receiver, as the now-playing page names it.
func TestWhatAReceiverPlaysIsNowPlaying(t *testing.T) {
	for _, from := range []string{AirPlayName, SpotifyName, DLNAName} {
		got := nowPlayingOf(playing(playingSnapshot{recvFrom: from, recvTitle: "Africa", recvArtist: "Toto", recvAlbum: "Toto IV"}))
		if want := (nowPlaying{from, "Africa", "Toto", "Toto IV", "playing"}); got != want {
			t.Errorf("%s: %+v, want %+v", from, got, want)
		}
	}
}

func TestMusicAssistantIsNowPlayingWhileItCarries(t *testing.T) {
	got := nowPlayingOf(playing(playingSnapshot{carried: true, extTitle: "Hold the Line", extArtist: "Toto", extAlbum: "Toto"}))
	if want := (nowPlaying{"Music Assistant", "Hold the Line", "Toto", "Toto", "playing"}); got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
}

// Music Assistant lets go of a track paused from here, and the page keeps it with play on it; so do
// the sensors, which say it is paused.
func TestATrackPausedFromHereStaysNowPlaying(t *testing.T) {
	got := nowPlayingOf(playingSnapshot{held: true, heldTitle: "Rosanna", heldArtist: "Toto", heldAlbum: "Toto IV", screenPaused: true})
	if want := (nowPlaying{"Music Assistant", "Rosanna", "Toto", "Toto IV", "paused"}); got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
}

func TestAReceiverWinsOverMusicAssistant(t *testing.T) {
	got := nowPlayingOf(playing(playingSnapshot{
		recvFrom: AirPlayName, recvTitle: "Africa", recvArtist: "Toto", recvAlbum: "Toto IV",
		carried: true, extTitle: "Hold the Line", held: true, heldTitle: "Rosanna",
	}))
	if want := (nowPlaying{AirPlayName, "Africa", "Toto", "Toto IV", "playing"}); got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
}

// What the page shows while Music Assistant carries a track is that track, not an older one paused from
// here before it.
func TestWhatMusicAssistantCarriesWinsOverATrackPausedFromHere(t *testing.T) {
	got := nowPlayingOf(playing(playingSnapshot{
		carried: true, extTitle: "Hold the Line", extArtist: "Toto", extAlbum: "Toto",
		held: true, heldTitle: "Rosanna", heldArtist: "Toto", heldAlbum: "Toto IV",
	}))
	if want := (nowPlaying{"Music Assistant", "Hold the Line", "Toto", "Toto", "playing"}); got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
}

// Music Assistant clears a track's name with its pause and lets go of the speaker a moment later. In
// between the track paused from here is named, rather than nothing and then the paused track again.
func TestATrackPausedFromHereIsNamedWhileMusicAssistantLetsGo(t *testing.T) {
	got := nowPlayingOf(playingSnapshot{carried: true, held: true, heldTitle: "Rosanna", heldArtist: "Toto", heldAlbum: "Toto IV", screenPaused: true})
	if want := (nowPlaying{"Music Assistant", "Rosanna", "Toto", "Toto IV", "paused"}); got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
	if got := nowPlayingOf(playing(playingSnapshot{carried: true})); got != (nowPlaying{source: "Music Assistant", state: "playing"}) {
		t.Errorf("with nothing held: %+v, want Music Assistant with no song", got)
	}
}

// The page and the sensors go by one order, and part only while Music Assistant carries a track it no
// longer names with one held: the page leaves it Music Assistant's, the sensors name the held one.
func TestThePageAndTheSensorsGoByOneOrder(t *testing.T) {
	for _, c := range []struct {
		what       string
		s          playingSnapshot
		page, sens playingWhose
	}{
		{"nothing", playingSnapshot{}, playingOwn, playingOwn},
		{"a station", playingSnapshot{ownPlaying: true, radioStation: "Radio Paradise"}, playingOwn, playingOwn},
		{"a phone over Bluetooth", playingSnapshot{recvFrom: "Bluetooth: A phone"}, playingOwn, playingOwn},
		{"a receiver", playingSnapshot{recvFrom: AirPlayName}, playingReceived, playingReceived},
		{"a receiver over all else", playingSnapshot{recvFrom: SpotifyName, carried: true, extTitle: "x", held: true}, playingReceived, playingReceived},
		{"Music Assistant", playingSnapshot{carried: true, extTitle: "x"}, playingCarried, playingCarried},
		{"Music Assistant over a held track", playingSnapshot{carried: true, extTitle: "x", held: true}, playingCarried, playingCarried},
		{"Music Assistant naming nothing", playingSnapshot{carried: true}, playingCarried, playingCarried},
		{"Music Assistant letting go", playingSnapshot{carried: true, held: true}, playingCarried, playingHeld},
		{"a held track", playingSnapshot{held: true}, playingHeld, playingHeld},
	} {
		if got := whosePlaying(c.s, false); got != c.page {
			t.Errorf("%s, for the page: %d, want %d", c.what, got, c.page)
		}
		if got := whosePlaying(c.s, true); got != c.sens {
			t.Errorf("%s, for the sensors: %d, want %d", c.what, got, c.sens)
		}
	}
}

// A phone over Bluetooth is named with no song, since nothing reads one from it; one it was told would
// be named.
func TestAPhoneOverBluetoothIsNowPlaying(t *testing.T) {
	got := nowPlayingOf(playing(playingSnapshot{recvFrom: media.BluetoothPrefix + "A phone", ownPlaying: true, radioStation: "Radio Paradise"}))
	if want := (nowPlaying{source: "Bluetooth", state: "playing"}); got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
	got = nowPlayingOf(playing(playingSnapshot{recvFrom: media.BluetoothPrefix + "A phone", recvTitle: "Africa", recvArtist: "Toto"}))
	if want := (nowPlaying{"Bluetooth", "Africa", "Toto", "", "playing"}); got != want {
		t.Errorf("told a song: %+v, want %+v", got, want)
	}
}

// The radio is named with what its own sensors say is on, while this player's own track plays or is
// paused, and not once it has stopped, though its poller has not caught up yet.
func TestTheRadioIsNowPlaying(t *testing.T) {
	radio := playingSnapshot{radioStation: "Radio Paradise", radioTitle: "Africa", radioArtist: "Toto"}

	on := radio
	on.ownPlaying, on.screenPlaying = true, true
	if got, want := nowPlayingOf(on), (nowPlaying{"Radio", "Africa", "Toto", "", "playing"}); got != want {
		t.Errorf("playing: %+v, want %+v", got, want)
	}
	paused := radio
	paused.ownPaused, paused.screenPaused = true, true
	if got, want := nowPlayingOf(paused), (nowPlaying{"Radio", "Africa", "Toto", "", "paused"}); got != want {
		t.Errorf("paused: %+v, want %+v", got, want)
	}
	if got := nowPlayingOf(radio); got != nothingPlaying {
		t.Errorf("stopped: %+v, want nothing", got)
	}
	unnamed := on
	unnamed.radioStation = ""
	if got := nowPlayingOf(unnamed); got != nothingPlaying {
		t.Errorf("a stream the radio does not name: %+v, want nothing", got)
	}
	// What Music Assistant carries is what the room hears over a station underneath it.
	over := on
	over.carried, over.extTitle = true, "Hold the Line"
	if got := nowPlayingOf(over); got.source != "Music Assistant" {
		t.Errorf("under Music Assistant: %+v, want Music Assistant", got)
	}
}

// A video has its own title, and is not named here.
func TestAVideoIsNotNowPlaying(t *testing.T) {
	for _, from := range []string{"Video", ""} {
		if got := nowPlayingOf(playing(playingSnapshot{recvFrom: from, recvTitle: "Africa", ownPlaying: true})); got != nothingPlaying {
			t.Errorf("%q: %+v, want nothing", from, got)
		}
	}
}

// The state is the room's music as a screen shows it, and idle with nothing named.
func TestTheStateIsWhatTheScreenShows(t *testing.T) {
	recv := playingSnapshot{recvFrom: AirPlayName, recvTitle: "Africa"}
	for _, c := range []struct {
		playing, paused bool
		want            string
	}{
		{true, false, "playing"},
		{false, true, "paused"},
		{false, false, "idle"},
	} {
		s := recv
		s.screenPlaying, s.screenPaused = c.playing, c.paused
		if got := nowPlayingOf(s).state; got != c.want {
			t.Errorf("playing %v, paused %v: %q, want %q", c.playing, c.paused, got, c.want)
		}
	}
	if got := nowPlayingOf(playingSnapshot{screenPlaying: true}); got != (nowPlaying{state: "idle"}) {
		t.Errorf("with nothing named: %+v, want idle and nothing else", got)
	}
}

// Once nothing plays every sensor is emptied and the state idle; and at a start every one is written,
// though the names already hold "", or Home Assistant would have no state for them at all.
func TestNothingPlayingEmptiesEverySensor(t *testing.T) {
	f, _, writes := nowPlayingFeature(t)
	if n := f.showNowPlaying(nil, nothingPlaying); n != 5 {
		t.Errorf("at a start: %d written, want all 5", n)
	}
	last := nowPlaying{AirPlayName, "Africa", "Toto", "Toto IV", "playing"}
	if n := f.showNowPlaying(&last, nothingPlaying); n != 5 {
		t.Errorf("once nothing plays: %d written, want all 5", n)
	}
	if got := writes.shown(); got != nothingPlaying {
		t.Errorf("the sensors say %+v, want nothing", got)
	}
	if last != nothingPlaying {
		t.Errorf("last = %+v, want nothing", last)
	}
}

// Every write is a row in Home Assistant's history and logbook, so only what changed is written.
func TestOnlyWhatChangedIsToldToHomeAssistant(t *testing.T) {
	f, _, writes := nowPlayingFeature(t)
	var last nowPlaying
	song := nowPlaying{AirPlayName, "Africa", "Toto", "Toto IV", "playing"}
	if n := f.showNowPlaying(&last, song); n != 5 {
		t.Errorf("a song: %d written, want 5", n)
	}
	if n := f.showNowPlaying(&last, song); n != 0 {
		t.Errorf("the same song again: %d written, want none", n)
	}
	next := nowPlaying{AirPlayName, "Rosanna", "Toto", "Toto IV", "playing"}
	if n := f.showNowPlaying(&last, next); n != 1 {
		t.Errorf("the next song on the album: %d written, want only the title", n)
	}
	paused := nowPlaying{AirPlayName, "Rosanna", "Toto", "Toto IV", "paused"}
	if n := f.showNowPlaying(&last, paused); n != 1 {
		t.Errorf("the song paused: %d written, want only the state", n)
	}
	if got := writes.shown(); got != paused {
		t.Errorf("the sensors say %+v, want %+v", got, paused)
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
	if got := (nowPlaying{state: "paused"}).cut(); got.state != "paused" {
		t.Errorf("the state cut to %q", got.state)
	}
}

// AirPlay names the next song a field at a time, each a change of its own, and keeps the last song's
// artist and album until it names the new ones. The sensors wait for the changes to stop and read the
// player once, with the whole song, rather than writing the new title over the last song's artist.
func TestASongToldFieldByFieldIsShownOnce(t *testing.T) {
	f, room, writes := nowPlayingFeature(t)
	nowSettle = 300 * time.Millisecond
	runNowPlaying(t, f)

	room.set(playing(playingSnapshot{recvFrom: AirPlayName, recvTitle: "Purple Rain", recvArtist: "Prince", recvAlbum: "Purple Rain"}))
	f.pokeNowPlaying()
	if !waitFor(func() bool { return writes.shown().album == "Purple Rain" }) {
		t.Fatalf("the sensors say %+v, want the first song", writes.shown())
	}
	reads := room.read()

	for _, s := range []playingSnapshot{
		{recvFrom: AirPlayName, recvTitle: "Africa", recvArtist: "Prince", recvAlbum: "Purple Rain"},
		{recvFrom: AirPlayName, recvTitle: "Africa", recvArtist: "Toto", recvAlbum: "Purple Rain"},
		{recvFrom: AirPlayName, recvTitle: "Africa", recvArtist: "Toto", recvAlbum: "Toto IV"},
	} {
		room.set(playing(s))
		f.pokeNowPlaying()
		time.Sleep(nowSettle / 10) // well within nowSettle between two, so a slow machine keeps them one change
	}

	want := nowPlaying{AirPlayName, "Africa", "Toto", "Toto IV", "playing"}
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
		t.Errorf("the artist written %q, want Prince then Toto", got)
	}
}

// A player that never stops changing does not keep the sensors waiting on it: they are told after
// nowSettleMost at the latest, while the changes still come.
func TestASongIsShownThoughThePlayerNeverStopsChanging(t *testing.T) {
	f, room, writes := nowPlayingFeature(t)
	nowSettle, nowSettleMost = 50*time.Millisecond, 150*time.Millisecond
	runNowPlaying(t, f)

	room.set(playing(playingSnapshot{recvFrom: AirPlayName, recvTitle: "Africa", recvArtist: "Toto", recvAlbum: "Toto IV"}))
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

	room.set(playing(playingSnapshot{recvFrom: AirPlayName, recvTitle: strings.Repeat("è", 300)}))
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

// What the radio's sensors are given, the now-playing sensors look at again, since they name the radio
// from it.
func TestTheRadiosSensorsPokeTheNowPlayingOnes(t *testing.T) {
	f := &Feature{nowPoke: make(chan struct{}, 1)}
	f.buildRadioSensors()
	f.showRadio(meta{station: "Radio Paradise"})
	if len(f.nowPoke) != 1 {
		t.Fatal("the radio's sensors did not poke the now-playing ones")
	}
	f.showRadio(meta{}) // one already waiting: dropped, not blocked on
}

// At a start every sensor is written, though the names already hold "", or Home Assistant would read
// them unknown until the first song; the state reads idle.
func TestEverySensorIsWrittenAtAStart(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	writes := recordNowPlaying(t)
	Get().Restore(config.Get())
	for id, want := range map[string]string{
		"now_playing_source": "", "now_playing_title": "", "now_playing_artist": "", "now_playing_album": "",
		"now_playing_state": "idle",
	} {
		if got := writes.got[id]; len(got) != 1 || got[0] != want {
			t.Errorf("%s written %q at a start, want %q once", id, got, want)
		}
	}
}

// A Music Assistant track paused from here stops being one by time alone, with nothing to say so: while
// one is named the sensors are looked at again now and then, and let it go.
func TestAPausedTrackThatRunsOutIsLetGo(t *testing.T) {
	f, room, writes := nowPlayingFeature(t)
	nowSettle, nowHeldEvery = time.Millisecond, 20*time.Millisecond
	runNowPlaying(t, f)

	room.set(playingSnapshot{held: true, heldTitle: "Rosanna", heldArtist: "Toto", heldAlbum: "Toto IV", screenPaused: true})
	f.pokeNowPlaying()
	if !waitFor(func() bool { return writes.shown().title == "Rosanna" }) {
		t.Fatalf("the sensors say %+v, want the paused track", writes.shown())
	}
	if got := writes.shown().state; got != "paused" {
		t.Errorf("the paused track's state %q, want paused", got)
	}
	room.set(playingSnapshot{}) // held for stoppedFor, and no more
	if !waitFor(func() bool { return writes.shown() == nothingPlaying }) {
		t.Errorf("the sensors say %+v, want nothing once the track ran out", writes.shown())
	}
}

// With no held track named nothing wakes the loop but a change: no timer runs while a song plays, or
// while nothing does, once a held track has gone.
func TestNothingButAChangeWakesTheSensorsWithoutAHeldTrack(t *testing.T) {
	f, room, writes := nowPlayingFeature(t)
	nowSettle, nowHeldEvery = time.Millisecond, 5*time.Millisecond
	runNowPlaying(t, f)

	for _, s := range []playingSnapshot{
		playing(playingSnapshot{recvFrom: AirPlayName, recvTitle: "Africa"}),
		{},
	} {
		room.set(s)
		f.pokeNowPlaying()
		want := nowPlayingOf(s)
		if !waitFor(func() bool { return writes.shown() == want }) {
			t.Fatalf("the sensors say %+v, want %+v", writes.shown(), want)
		}
		reads := room.read()
		time.Sleep(20 * nowHeldEvery)
		if n := room.read() - reads; n != 0 {
			t.Errorf("with %+v named: read %d times more with nothing changed, want none", want, n)
		}
	}

	// A held track arms it, and once that track goes it stops again.
	room.set(playingSnapshot{held: true, heldTitle: "Rosanna", screenPaused: true})
	f.pokeNowPlaying()
	reads := room.read()
	if !waitFor(func() bool { return room.read()-reads >= 3 }) {
		t.Fatalf("with a held track named: read %d times, want it looked at again and again", room.read()-reads)
	}
	room.set(playingSnapshot{})
	if !waitFor(func() bool { return writes.shown() == nothingPlaying }) {
		t.Fatalf("the sensors say %+v, want nothing once the track ran out", writes.shown())
	}
	reads = room.read()
	time.Sleep(20 * nowHeldEvery)
	if n := room.read() - reads; n != 0 {
		t.Errorf("after the held track: read %d times more, want none", n)
	}
}

// The log says the sensors changed, but what is playing only at Debug: a log pasted into an issue
// should not be the owner's listening history.
func TestTheLogIsNoListeningHistory(t *testing.T) {
	var buf lockedBuffer
	was := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(was) })

	f, room, writes := nowPlayingFeature(t)
	nowSettle = 50 * time.Millisecond
	runNowPlaying(t, f)
	room.set(playing(playingSnapshot{recvFrom: AirPlayName, recvTitle: "Purple Rain", recvArtist: "Prince", recvAlbum: "Wild Album"}))
	f.pokeNowPlaying()
	if !waitFor(func() bool { return writes.shown().album == "Wild Album" }) {
		t.Fatalf("the sensors say %+v", writes.shown())
	}
	// The line is logged just after the sensors are written: waited for, not read at once.
	waitFor(func() bool { return strings.Contains(buf.String(), "now playing sensors") })
	logged := buf.String()
	if !strings.Contains(logged, "now playing sensors") {
		t.Errorf("the change was not logged: %q", logged)
	}
	for _, s := range []string{"Purple Rain", "Prince", "Wild Album"} {
		if strings.Contains(logged, s) {
			t.Errorf("the log at Info names %q: %q", s, logged)
		}
	}
}

// lockedBuffer is a bytes.Buffer safe to write from the loop's goroutine and read from the test's.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// A track Home Assistant named (media_title) is named so, over a station the radio sensors still name,
// with its subtitle where an artist goes; without a source it is Playback.
func TestATitledTrackIsNamedAsItWasTold(t *testing.T) {
	s := playingSnapshot{ownPlaying: true, radioStation: "Old Station", radioTitle: "Old Song",
		titled: true, own: media.Title{Title: "Message from Alex", Subtitle: "today 14:12", Source: "Answering machine"}}
	if got, want := nowPlayingOf(playing(s)), (nowPlaying{"Answering machine", "Message from Alex", "today 14:12", "", "playing"}); got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
	s.own.Source = ""
	if got := nowPlayingOf(playing(s)); got.source != PlaybackName {
		t.Errorf("without a source: named as from %q, want %q", got.source, PlaybackName)
	}
}
