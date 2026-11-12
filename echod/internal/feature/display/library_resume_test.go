//go:build !dot && !spot

package display

import (
	"encoding/json"
	"fmt"
	"image"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/musicassistant"
)

// A paused player is where it says; a playing one has moved on since it said so.
func TestLibPositionOfMovesOnWhilePlaying(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	a := map[string]any{"media_position": 100.0, "media_position_updated_at": now.Add(-30 * time.Second).Format(time.RFC3339Nano)}
	if p, ok := libPositionOf(a, "paused", now); !ok || p != 100 {
		t.Errorf("paused at %v (%v); want 100", p, ok)
	}
	if p, ok := libPositionOf(a, "playing", now); !ok || p != 130 {
		t.Errorf("playing at %v (%v); want 130", p, ok)
	}
	if _, ok := libPositionOf(map[string]any{}, "playing", now); ok {
		t.Errorf("a player with no position gave one")
	}
}

func TestLibClock(t *testing.T) {
	for secs, want := range map[int]string{65: "1:05", 3723: "1:02:03", 0: "0:00"} {
		if got := libClock(secs); got != want {
			t.Errorf("libClock(%d) = %q, want %q", secs, got, want)
		}
	}
}

// A book's or an episode's page offers to go on from where Music Assistant says it was left, and not
// once it was finished; nothing else offers to.
func TestLibResumeOffer(t *testing.T) {
	book := musicassistant.Entry{URI: "library://audiobook/7", ResumeMS: 4_000_500}
	if s, ok := libResumeOffer(&libPage{what: libAudiobook, entry: book}); !ok || s != 4000 {
		t.Errorf("offer = %d (%v); want 4000", s, ok)
	}
	ep := musicassistant.Entry{URI: "rss://podcast_episode/1", ResumeMS: 61_000}
	if s, ok := libResumeOffer(&libPage{what: libEpisode, entry: ep}); !ok || s != 61 {
		t.Errorf("episode offer = %d (%v); want 61", s, ok)
	}
	for name, p := range map[string]*libPage{
		"a book never played": {what: libAudiobook, entry: musicassistant.Entry{URI: "library://audiobook/8"}},
		"a finished book":     {what: libAudiobook, entry: musicassistant.Entry{URI: "library://audiobook/9", ResumeMS: 9000, FullyPlayed: true}},
		"an album":            {what: libAlbum, entry: book},
	} {
		if _, ok := libResumeOffer(p); ok {
			t.Errorf("%s offered to resume", name)
		}
	}
}

// How far a book was listened to is its place over its length; a finished one is all of it.
func TestLibProgress(t *testing.T) {
	if f, played := libProgress(musicassistant.Entry{ResumeMS: 900_000, Duration: 3600}); played || f != 0.25 {
		t.Errorf("progress = %v (%v); want a quarter", f, played)
	}
	if f, played := libProgress(musicassistant.Entry{FullyPlayed: true}); !played || f != 1 {
		t.Errorf("finished = %v (%v)", f, played)
	}
	if f, _ := libProgress(musicassistant.Entry{ResumeMS: 900_000}); f != 0 {
		t.Errorf("a place with no length to measure it by gave %v", f)
	}
}

// libListenMA is a Music Assistant that keeps the commands asked of it, and refuses them all when told.
func libListenMA(t *testing.T, refuse bool) func() []string {
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Command string         `json:"command"`
			Args    map[string]any `json:"args"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		asked = append(asked, fmt.Sprintf("%s %v %v %v", body.Command, body.Args["media"], body.Args["start_item"], body.Args["start_from_beginning"]))
		mu.Unlock()
		if refuse {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write([]byte(`null`))
	}))
	t.Cleanup(srv.Close)
	libClient = func() (musicassistant.Client, bool) { return musicassistant.Client{URL: srv.URL, Token: "tok"}, true }
	libQueueOf = func(string) string { return "up0a1b2c3d" }
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), asked...) }
}

// Start over asks Music Assistant to play the book from its first chapter, an episode from its
// beginning, and a chapter from where it starts; Home Assistant is not asked to play or seek at all.
func TestStartOverAndChaptersPlayThroughMusicAssistant(t *testing.T) {
	noLibrary(t)
	taps := recordTaps(t)
	ma := libListenMA(t, false)
	d := &Display{poke: make(chan struct{}, 1)}
	sheet := libSheetWith(libAudiobooks, nil, "media_player.ma_kitchen")
	setLibUp(d, &sheet)
	book := musicassistant.Entry{URI: "library://audiobook/5", ResumeMS: 120_000}
	chapters := []musicassistant.Entry{musicassistant.ChapterEntry(book, musicassistant.Chapter{Position: 0, Name: "One", End: 10}),
		musicassistant.ChapterEntry(book, musicassistant.Chapter{Position: 1, Name: "Two", Start: 10, End: 20})}
	page := &libPage{what: libAudiobook, entry: book, sections: []libSection{{show: libShowTracks, kind: "chapter", entries: chapters}}}

	d.libStartOver(&sheet, page, "audiobook", libLitShuffle)
	d.libPlayChapter(&sheet, book, chapters[1], 1)
	d.libStartOver(&sheet, &libPage{what: libEpisode, entry: musicassistant.Entry{URI: "rss://podcast_episode/e1"}}, "podcast_episode", libLitShuffle)
	waitUntil(t, "three plays", func() bool { return len(ma()) == 3 })
	got := strings.Join(ma(), "; ")
	for _, want := range []string{"player_queues/play_media library://audiobook/5 0 <nil>", "player_queues/play_media library://audiobook/5 1 <nil>",
		"player_queues/play_media rss://podcast_episode/e1 <nil> true"} {
		if !strings.Contains(got, want) {
			t.Errorf("asked %q, want %q among them", got, want)
		}
	}
	time.Sleep(20 * time.Millisecond)
	if len(taps()) != 0 {
		t.Errorf("Home Assistant was asked %v as well", taps())
	}
}

// Start over from a held book's sheet, whose page (and so its chapters) was never opened, asks Music
// Assistant for the book from chapter 0, rather than playing it on from its place and seeking after.
func TestStartOverFromTheSheetAsksForChapterZero(t *testing.T) {
	noLibrary(t)
	taps := recordTaps(t)
	ma := libListenMA(t, false)
	d := &Display{poke: make(chan struct{}, 1)}
	sheet := libSheetWith(libAudiobooks, nil, "media_player.ma_kitchen")
	setLibUp(d, &sheet)
	book := musicassistant.Entry{URI: "library://audiobook/5", ResumeMS: 120_000, MediaType: "audiobook"}
	d.libMenuChose(&sheet, libMenu{open: true, entry: book, kind: "audiobook"}, libMenuStartOver)
	waitUntil(t, "the play", func() bool { return len(ma()) == 1 })
	if got := ma()[0]; got != "player_queues/play_media library://audiobook/5 0 <nil>" {
		t.Errorf("asked %q; want the book from chapter 0", got)
	}
	time.Sleep(20 * time.Millisecond)
	if len(taps()) != 0 {
		t.Errorf("Home Assistant was asked %v as well", taps())
	}
}

// When Music Assistant will not play on the player itself, or a book has no chapters to start from,
// Home Assistant plays it and then seeks to the place asked for.
func TestStartOverFallsBackToHomeAssistant(t *testing.T) {
	noLibrary(t)
	taps := recordTaps(t)
	libListenMA(t, true)
	firstWas, pollWas, triesWas, stateWas := libSeekFirst, libSeekPoll, libSeekTries, libPlayerState
	t.Cleanup(func() {
		libSeekFirst, libSeekPoll, libSeekTries, libPlayerState = firstWas, pollWas, triesWas, stateWas
	})
	libSeekFirst, libSeekPoll, libSeekTries = 0, time.Millisecond, 2
	libPlayerState = func(string) (hass.State, error) { return hass.State{State: "playing"}, nil }
	d := &Display{poke: make(chan struct{}, 1)}
	sheet := libSheetWith(libAudiobooks, nil, "media_player.ma_kitchen")
	setLibUp(d, &sheet)
	book := musicassistant.Entry{URI: "library://audiobook/5", ResumeMS: 120_000}

	// No chapters listed, and Music Assistant refuses: Home Assistant plays, then seeks to the start.
	d.libStartOver(&sheet, &libPage{what: libAudiobook, entry: book}, "audiobook", libLitShuffle)
	waitUntil(t, "a play and a seek", func() bool { return len(taps()) == 2 })
	if a := taps(); a[0].Service != "music_assistant.play_media" || a[1].Service != "media_player.media_seek" || a[1].Data["seek_position"] != 0.0 {
		t.Errorf("asked %v; want a play, then a seek to the start", a)
	}
	// A chapter, refused by Music Assistant: Home Assistant plays and seeks to where it starts.
	d.libPlayChapter(&sheet, book, musicassistant.ChapterEntry(book, musicassistant.Chapter{Position: 1, Start: 75.6, End: 200}), 1)
	waitUntil(t, "a second play and seek", func() bool { return len(taps()) == 4 })
	if a := taps(); a[3].Service != "media_player.media_seek" || a[3].Data["seek_position"] != 75.0 {
		t.Errorf("asked %v; want a seek to 75", a)
	}
}

// Held, an audiobook or an episode offers Play, Start over once started, its details and the queueing
// choices, but no radio; Details opens its page, which reads it in full.
func TestAHeldEpisodeOffersStartOverAndDetails(t *testing.T) {
	noLibrary(t)
	recordTaps(t)
	ep := musicassistant.Entry{ItemID: "e1", Provider: "rss", URI: "rss://podcast_episode/e1", Name: "Episode", MediaType: "podcast_episode", ResumeMS: 5000}
	if got, want := fmt.Sprint(libMenuFor(ep, "podcast_episode", false, 0)), fmt.Sprint([]int{libMenuPlay, libMenuStartOver, libMenuDetails, libMenuNext, libMenuAdd}); got != want {
		t.Errorf("an episode started offers %s, want %s", got, want)
	}
	ep.ResumeMS = 0
	if got := libMenuFor(ep, "podcast_episode", false, 0); len(got) != 4 || got[1] != libMenuDetails {
		t.Errorf("an episode not started offers %v", got)
	}
	book := musicassistant.Entry{URI: "library://audiobook/2", ResumeMS: 1}
	if got := libMenuFor(book, "audiobook", false, 0); got[len(got)-1] != libMenuFavorite {
		t.Errorf("a book offers %v; want it to end with Add to favorites", got)
	}

	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	sheet := libSheetWith(libPodcasts, nil, "media_player.ma_kitchen")
	pod := newLibPage(libPodcast, musicassistant.Entry{Name: "A podcast", URI: "library://podcast/1"})
	pod.loaded = true
	pod.sections = []libSection{{word: -1, show: libShowTracks, kind: "podcast_episode", entries: []musicassistant.Entry{ep}}}
	sheet.stack = []libPage{pod}
	setLibUp(d, &sheet)
	zones, _ := libDraw(r, sheet)
	if !d.libraryHold(libMiddle(libFind(t, zones, libPartItem, 0))) {
		t.Fatal("holding an episode did not open its sheet")
	}
	zones, _ = libDraw(r, d.libraryViewNow().sheet)
	d.libraryTap(libMiddle(libFind(t, zones, libPartMenu, 1))) // Details, after Play
	if l := d.libraryViewNow().sheet; len(l.stack) != 2 || l.stack[1].what != libEpisode || l.stack[1].entry.URI != ep.URI {
		t.Errorf("Details opened %+v", l.stack)
	}
}
