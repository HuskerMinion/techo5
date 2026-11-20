package home

import (
	"sync"
	"testing"
	"time"
)

func TestMAServiceOf(t *testing.T) {
	for id, want := range map[string]string{
		"spotify--Xy12Ab34://track/0aBcDeFgHiJkLmNoPqRsTu":          "Spotify",
		"spotify://track/0aBcDeFgHiJkLmNoPqRsTu":                    "Spotify",
		"plex--Ab12Cd34://track/12345":                              "Plex",
		"ytmusic--x1://track/abc":                                   "YouTube Music",
		"radiobrowser://radio/00000000-1111-2222-3333-444444444444": "Radio",
		"tunein--q://radio/s12345":                                  "Radio",
		"library://track/825":                                       "Library",
		"filesystem_local--music://track/Artist/Album/01.flac":      "Library",
		"filesystem_smb--nas://track/Artist/Album/01.flac":          "Library",
		"  SPOTIFY--abc://track/x  ":                                "Spotify",
		"https://stream.example.com/live.mp3":                       "",
		"someprovider--abc://track/1":                               "",
		"":                                                          "",
		"not a uri":                                                 "",
		"://track/1":                                                "",
		"media-source://media_source/local/song.mp3":                "",
		"spotify_connect--abc://track/x":                            "",
		"plexamp--abc://track/x":                                    "",
		"radiobrowser":                                              "",
		"library:/track/825":                                        "",
		"filesystem_local://track/x":                                "Library",
		"tidal--t://track/1":                                        "Tidal",
		"qobuz--q://track/1":                                        "Qobuz",
		"deezer--d://track/1":                                       "Deezer",
		"apple_music--a://track/1":                                  "Apple Music",
		"soundcloud--s://track/1":                                   "SoundCloud",
		"jellyfin--j://track/1":                                     "Jellyfin",
	} {
		if got := maServiceOf(id); got != want {
			t.Errorf("maServiceOf(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestMASourceNames(t *testing.T) {
	if got := maSourceName(""); got != MusicAssistantName {
		t.Errorf("sensor name of nothing known = %q, want %q", got, MusicAssistantName)
	}
	if got := maSourceName("Spotify"); got != "Spotify" {
		t.Errorf("sensor name of Spotify = %q", got)
	}
	for s, want := range map[string]string{"": MusicAssistantName, "Spotify": "Spotify", "Radio": "Radio", "Library": "Library"} {
		if got := maScreenName(s); got != want {
			t.Errorf("maScreenName(%q) = %q, want %q", s, got, want)
		}
	}
}

// maHome is Home Assistant as the lookup sees it: what Music Assistant's entity names on each ask.
type maHome struct {
	mu      sync.Mutex
	answers []maAnswer // one per ask; the last is repeated
	asks    int
	gate    chan struct{} // when set, each ask waits for a receive from it
}

type maAnswer struct {
	content, title string
	ok             bool
}

func (h *maHome) ask() (string, string, bool) {
	if h.gate != nil {
		<-h.gate
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	a := h.answers[min(h.asks, len(h.answers)-1)]
	h.asks++
	return a.content, a.title, a.ok
}

func (h *maHome) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.asks
}

func (h *maHome) set(answers ...maAnswer) {
	h.mu.Lock()
	h.answers, h.asks = answers, 0
	h.mu.Unlock()
}

// fakeMASource puts h in Home Assistant's place, with no wait between tries, from a clean start, and
// returns what is told each time an answer comes.
func fakeMASource(t *testing.T, h *maHome) <-chan struct{} {
	t.Helper()
	wasCan, wasContent, wasWait, wasFound := maCanAsk, maContent, maLookupWait, maSourceFound
	found := make(chan struct{}, 16)
	maCanAsk = func() bool { return true }
	maContent = h.ask
	maLookupWait = func() {}
	maSourceFound = func() { found <- struct{}{} }
	reset := func() {
		maSource.mu.Lock()
		maSource.title, maSource.known, maSource.name, maSource.last, maSource.asking = "", false, "", "", false
		maSource.mu.Unlock()
	}
	reset()
	t.Cleanup(func() {
		maCanAsk, maContent, maLookupWait, maSourceFound = wasCan, wasContent, wasWait, wasFound
		reset()
	})
	return found
}

func waitFound(t *testing.T, found <-chan struct{}) {
	t.Helper()
	select {
	case <-found:
	case <-time.After(5 * time.Second):
		t.Fatal("no answer came")
	}
}

func TestMASourceIsAskedOncePerTrack(t *testing.T) {
	h := &maHome{}
	h.set(maAnswer{"spotify--abc://track/1", "A Song", true})
	found := fakeMASource(t, h)

	if got := maSourceFor("A Song"); got != "" {
		t.Errorf("before the answer: %q, want nothing known", got)
	}
	waitFound(t, found)
	for range 3 {
		if got := maSourceFor("A Song"); got != "Spotify" {
			t.Fatalf("after the answer: %q, want Spotify", got)
		}
	}
	if n := h.count(); n != 1 {
		t.Errorf("Home Assistant asked %d times for one track, want 1", n)
	}
	// Music Assistant clears the name with a pause: the answer stands, and nothing is asked.
	if got := maSourceFor(""); got != "Spotify" {
		t.Errorf("unnamed: %q, want the last answer", got)
	}
	if n := h.count(); n != 1 {
		t.Errorf("Home Assistant asked again for an unnamed track")
	}
}

func TestMASourceKeepsTheLastWhileAsking(t *testing.T) {
	h := &maHome{}
	h.set(maAnswer{"spotify--abc://track/1", "First", true})
	found := fakeMASource(t, h)
	maSourceFor("First")
	waitFound(t, found)

	h.gate = make(chan struct{})
	h.set(maAnswer{"plex--p://track/9", "Second", true})
	if got := maSourceFor("Second"); got != "Spotify" {
		t.Errorf("while the next track is asked about: %q, want the last answer (Spotify)", got)
	}
	h.gate <- struct{}{}
	waitFound(t, found)
	if got := maSourceFor("Second"); got != "Plex" {
		t.Errorf("after the answer: %q, want Plex", got)
	}
}

func TestMASourceWaitsForHomeAssistantToCatchUp(t *testing.T) {
	h := &maHome{}
	// Home Assistant still names the last song twice before the new one.
	h.set(
		maAnswer{"plex--p://track/9", "Old Song", true},
		maAnswer{"plex--p://track/9", "Old Song", true},
		maAnswer{"spotify--abc://track/1", "New Song", true},
	)
	found := fakeMASource(t, h)
	maSourceFor("New Song")
	waitFound(t, found)
	if got := maSourceFor("New Song"); got != "Spotify" {
		t.Errorf("got %q, want Spotify, the service of the matching answer", got)
	}
	if n := h.count(); n != 3 {
		t.Errorf("asked %d times, want 3", n)
	}
}

func TestMASourceTakesTheLastAnswerWhenTitlesNeverMatch(t *testing.T) {
	h := &maHome{}
	// A station's song: Home Assistant names the station, Sendspin the song.
	h.set(maAnswer{"radiobrowser://radio/abc", "Some Station", true})
	found := fakeMASource(t, h)
	maSourceFor("Artist - Song")
	waitFound(t, found)
	if got := maSourceFor("Artist - Song"); got != "Radio" {
		t.Errorf("got %q, want Radio", got)
	}
	if n := h.count(); n != maLookupTries {
		t.Errorf("asked %d times, want %d", n, maLookupTries)
	}
}

func TestMASourceUnknownProviderAndNoEntity(t *testing.T) {
	h := &maHome{}
	h.set(maAnswer{"newprovider--x://track/1", "Song", true})
	found := fakeMASource(t, h)
	maSourceFor("Song")
	waitFound(t, found)
	if got := maSourceFor("Song"); got != "" {
		t.Errorf("unknown provider: %q, want nothing known", got)
	}

	h.set(maAnswer{})
	maSourceFor("Other")
	waitFound(t, found)
	if got := maSourceFor("Other"); got != "" {
		t.Errorf("no entity: %q, want nothing known", got)
	}
	if n := h.count(); n != 1 {
		t.Errorf("asked %d times with no entity, want 1", n)
	}
}

func TestMASourceWithoutHomeAssistant(t *testing.T) {
	h := &maHome{}
	h.set(maAnswer{"spotify--abc://track/1", "Song", true})
	fakeMASource(t, h)
	maCanAsk = func() bool { return false }
	if got := maSourceFor("Song"); got != "" {
		t.Errorf("got %q, want nothing known", got)
	}
	time.Sleep(10 * time.Millisecond)
	if n := h.count(); n != 0 {
		t.Errorf("asked %d times without Home Assistant", n)
	}
}

func TestNowPlayingNamesTheMusicAssistantService(t *testing.T) {
	carried := playingSnapshot{carried: true, extTitle: "Song", extArtist: "Band", screenPlaying: true, maService: "Spotify"}
	if got := nowPlayingOf(carried); got.source != "Spotify" || got.title != "Song" || got.state != nowStatePlaying {
		t.Errorf("carried: %+v", got)
	}
	held := playingSnapshot{held: true, heldTitle: "Song", screenPaused: true, maService: "Plex"}
	if got := nowPlayingOf(held); got.source != "Plex" || got.state != nowStatePaused {
		t.Errorf("held: %+v", got)
	}
	unknown := playingSnapshot{carried: true, extTitle: "Song", screenPlaying: true}
	if got := nowPlayingOf(unknown); got.source != MusicAssistantName {
		t.Errorf("unknown: source %q, want %q", got.source, MusicAssistantName)
	}
}
