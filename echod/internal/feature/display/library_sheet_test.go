//go:build !dot && !spot

package display

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mdi"
	"github.com/HuskerMinion/techo5/echod/internal/lib/musicassistant"
)

// libNames are made-up names, so a preview looks like a library rather than a list of numbers.
var libNames = []string{"Northern Lights", "Paper Boats", "The Long Road", "Velvet Hours", "Static Bloom",
	"Harbor Songs", "Glass Garden", "Midnight Diner", "Copper Sky", "Low Tide", "Wild Orchard", "Neon Rain",
	"Quiet Engines", "Saltwater", "Golden Hour", "Fable", "Driftwood", "Polaroid Summer", "Blue Atlas", "Ember"}

// libEntries are n made-up items of a media type, each with a picture.
func libEntries(kind string, n int) []musicassistant.Entry {
	out := make([]musicassistant.Entry, n)
	for i := range out {
		e := musicassistant.Entry{ItemID: fmt.Sprint(i), Provider: "library", MediaType: kind,
			URI: fmt.Sprintf("library://%s/%d", kind, i), Name: libNames[i%len(libNames)]}
		if i >= len(libNames) {
			e.Name += fmt.Sprintf(" %d", i/len(libNames)+1)
		}
		e.Artists = []musicassistant.Entry{{Name: "The Example Band"}}
		e.Image = &musicassistant.Image{Type: "thumb", ProxyID: fmt.Sprintf("%s-%d", kind, i)}
		e.Duration, e.TrackNumber, e.Year, e.Owner = 150+i*17, i+1, 2019, "Kitchen"
		out[i] = e
	}
	return out
}

// libSheetWith is a library up on a tab with entries loaded, playing on target.
func libSheetWith(tab int, entries []musicassistant.Entry, target string) librarySheet {
	l := librarySheet{target: target, set: true, base: "http://ma.invalid:8095", tab: tab, lit: -1}
	for i := range l.roots {
		l.roots[i] = newLibPage(i, musicassistant.Entry{})
	}
	p := &l.roots[tab]
	p.loaded = true
	if tab < libSearch {
		p.sections = []libSection{{word: -1, show: libShowGrid, kind: libTypes[tab], entries: entries}}
	}
	return l
}

// libDraw draws the library over the drawn dashboard on r, and gives back its parts.
func libDraw(r *renderer, l librarySheet) ([]mediaZone, image.Rectangle) {
	v := libraryView{sheet: l}
	r.draw(scene{now: time.Date(2026, 11, 8, 20, 15, 0, 0, time.Local), phase: "idle", showDash: true,
		dashMode: config.DashboardDrawn, drawn: fourControls(), dashLibrary: &v})
	r.zmu.Lock()
	defer r.zmu.Unlock()
	return r.libZones, r.libCard
}

func libCount(zones []mediaZone) map[int]int {
	n := map[int]int{}
	for _, z := range zones {
		n[z.kind]++
	}
	return n
}

func libMiddle(z mediaZone) (int, int) {
	return (z.r.Min.X + z.r.Max.X) / 2, (z.r.Min.Y + z.r.Max.Y) / 2
}

func libFind(t *testing.T, zones []mediaZone, kind, index int) mediaZone {
	t.Helper()
	for _, z := range zones {
		if z.kind == kind && (index < 0 || z.index == index) {
			return z
		}
	}
	t.Fatalf("no part %d (%d) in %v", kind, index, libCount(zones))
	return mediaZone{}
}

// noLibrary keeps a test away from any Music Assistant and Home Assistant: no music library is set up,
// no player is found, nothing is this device's, and a play does not leave the library.
func noLibrary(t *testing.T) {
	pc, pf, po, pl, pq, ps := libClient, libFindPlayer, libIsOwn, libLeaveAfter, libQueueOf, libOpenSheet
	libClient = func() (musicassistant.Client, bool) { return musicassistant.Client{}, false }
	libFindPlayer = func(string) string { return "" }
	libIsOwn = func(string) bool { return false }
	libLeaveAfter = func(time.Duration, func()) *time.Timer { return nil }
	libQueueOf = func(string) string { return "" }
	libOpenSheet = func(*Display, string) bool { return false }
	t.Cleanup(func() {
		libClient, libFindPlayer, libIsOwn, libLeaveAfter, libQueueOf, libOpenSheet = pc, pf, po, pl, pq, ps
	})
}

// recordTaps catches what the library asks of Home Assistant.
func recordTaps(t *testing.T) func() []dashboard.Action {
	var mu sync.Mutex
	var asked []dashboard.Action
	prev, prevAfter := sheetTap, libAfter
	sheetTap = func(a dashboard.Action) { mu.Lock(); asked = append(asked, a); mu.Unlock() }
	libAfter = func(_ time.Duration, f func()) *time.Timer { f(); return nil }
	t.Cleanup(func() { sheetTap, libAfter = prev, prevAfter })
	return func() []dashboard.Action {
		mu.Lock()
		defer mu.Unlock()
		return append([]dashboard.Action(nil), asked...)
	}
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("no %s", what)
}

// libFakeCovers serves made-up covers, each its own color and pattern, as the proxy would.
func libFakeCovers(t *testing.T) {
	prev := libFetch
	libFetch = func(_ context.Context, u string) ([]byte, int, error) {
		h := fnv.New32a()
		h.Write([]byte(u))
		n := h.Sum32()
		a := libHues[n%uint32(len(libHues))]
		b := libHues[(n/8)%uint32(len(libHues))]
		img := image.NewRGBA(image.Rect(0, 0, 256, 256))
		light := color.RGBA{250, 245, 235, 255}
		cx, cy, rad := float64(70+int(n%120)), float64(60+int(n/7%140)), float64(30+int(n/5%60))
		for y := 0; y < 256; y++ {
			for x := 0; x < 256; x++ {
				c := lerp(lerp(a, color.RGBA{20, 20, 30, 255}, 0.35), b, float64(x+y)/512)
				// A disc, or bands, softened at their edges, so one cover is not another in another color.
				var f float64
				if n%3 != 1 {
					f = min(max(rad-math.Hypot(float64(x)-cx, float64(y)-cy), 0), 1.5) / 1.5
				} else {
					f = (math.Sin(float64(y+int(n%40))/float64(8+n%10)) + 1) / 2
					f = min(max((f-0.6)*6, 0), 1)
				}
				img.SetRGBA(x, y, lerp(c, light, 0.5*f))
			}
		}
		var buf bytes.Buffer
		png.Encode(&buf, img)
		return buf.Bytes(), http.StatusOK, nil
	}
	t.Cleanup(func() { libFetch = prev })
}

// libWaitCovers waits for the covers asked for to come.
func libWaitCovers() {
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		libPics.mu.Lock()
		waiting := len(libPics.asked)
		libPics.mu.Unlock()
		if waiting == 0 {
			return
		}
	}
}

// Every tab, an album, an artist and a search's findings draw on the Show 5 and the Show 8 with their
// parts on the page: the tabs and Done along the top, four covers abreast on the Show 5 and five on the
// Show 8, an opened page's Back, Play and Shuffle, an artist's albums and a search's findings in rows
// that scroll along. With SHOW_PREVIEW set, each is written there, with made-up covers.
func TestTheLibraryPageLaysOutItsParts(t *testing.T) {
	for _, icon := range []string{"chevron-left", "shuffle-variant", "magnify", "account-music", "album",
		"playlist-music", "radio", "music-note", "bookshelf", "play", "alert-circle-outline"} {
		if _, ok := mdi.Rune(icon); !ok {
			t.Errorf("no icon %q", icon)
		}
	}
	dir := os.Getenv("SHOW_PREVIEW")
	if dir != "" {
		libFakeCovers(t)
	}
	tracks := libEntries("track", 14)
	for i := range tracks {
		tracks[i].Album = &musicassistant.Entry{Name: "Velvet Hours"}
	}
	album := libSheetWith(libAlbums, libEntries("album", 30), "media_player.ma_kitchen")
	opened := newLibPage(libAlbum, libEntries("album", 4)[3])
	opened.loaded = true
	opened.sections = []libSection{{word: -1, show: libShowTracks, kind: "track", entries: tracks}}
	album.stack = []libPage{opened}
	playlist := libSheetWith(libPlaylists, libEntries("playlist", 9), "media_player.ma_kitchen")
	pl := newLibPage(libPlaylist, libEntries("playlist", 2)[1])
	pl.loaded = true
	pl.sections = []libSection{{word: -1, show: libShowTracks, kind: "track", entries: tracks}}
	playlist.stack = []libPage{pl}
	artist := newLibPage(libArtist, libEntries("artist", 6)[5])
	artist.loaded = true
	artist.sections = []libSection{{word: libAlbums, show: libShowRow, kind: "album", entries: libEntries("album", 8)},
		{word: libWordTopTracks, show: libShowTracks, kind: "track", entries: tracks[:6]}}
	artistSheet := libSheetWith(libArtists, libEntries("artist", 40), "media_player.ma_kitchen")
	artistSheet.stack = []libPage{artist}
	search := libSheetWith(libSearch, nil, "media_player.ma_kitchen")
	search.roots[libSearch].query = "harbor"
	search.roots[libSearch].sections = []libSection{
		{word: libArtists, show: libShowRow, kind: "artist", entries: libEntries("artist", 7)},
		{word: libAlbums, show: libShowRow, kind: "album", entries: libEntries("album", 3)},
		{word: libTracks, show: libShowTracks, kind: "track", entries: tracks[:4]}}
	noPlayer := artistSheet
	noPlayer.target = ""
	typing := search
	typing.typing, typing.text = true, "harbor"
	emptyRadio := libSheetWith(libRadio, nil, "media_player.ma_kitchen")
	emptyRadio.roots[libRadio].sections = []libSection{{word: -1, show: libShowGrid, kind: "radio"}}
	derived := libSheetWith(libAlbums, libEntries("album", 12), "media_player.ma_kitchen")
	derived.roots[libAlbums].sections[0].derived = true

	for _, size := range []image.Point{{showWide, showHigh}, {show8Wide, show8High}} {
		for _, c := range []struct {
			name  string
			sheet librarySheet
			want  map[int]int // at least as many of each part
		}{
			{"artists", libSheetWith(libArtists, libEntries("artist", 40), "media_player.ma_kitchen"), map[int]int{libPartTab: libTabs, libPartDone: 1, libPartItem: 4, libPartContent: 1}},
			{"albums", libSheetWith(libAlbums, libEntries("album", 40), "media_player.ma_kitchen"), map[int]int{libPartTab: libTabs, libPartItem: 4}},
			{"playlists", libSheetWith(libPlaylists, libEntries("playlist", 9), "media_player.ma_kitchen"), map[int]int{libPartItem: 4}},
			{"tracks", libSheetWith(libTracks, tracks, "media_player.ma_kitchen"), map[int]int{libPartItem: 4}},
			{"radio", libSheetWith(libRadio, libEntries("radio", 10), "media_player.ma_kitchen"), map[int]int{libPartItem: 4}},
			{"album-page", album, map[int]int{libPartBack: 1, libPartPlay: 1, libPartShuffle: 1, libPartDone: 1, libPartItem: 1}},
			{"playlist-page", playlist, map[int]int{libPartBack: 1, libPartPlay: 1, libPartShuffle: 1, libPartItem: 1}},
			{"artist-page", artistSheet, map[int]int{libPartBack: 1, libPartPlay: 1, libPartShuffle: 1, libPartRow: 1}},
			{"artist-no-player", noPlayer, map[int]int{libPartBack: 1, libPartDone: 1}},
			{"search", search, map[int]int{libPartTab: libTabs, libPartField: 1, libPartRow: 1, libPartItem: 4}},
			{"radio-empty", emptyRadio, map[int]int{libPartGoSearch: 1}},
			{"albums-from-tracks", derived, map[int]int{libPartItem: 4}},
			{"not-set-up", librarySheet{}, map[int]int{libPartDone: 1}},
			{"keyboard", typing, map[int]int{}},
		} {
			img := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
			r := newRenderer(img)
			zones, card := libDraw(r, c.sheet)
			if dir != "" {
				libWaitCovers()
				zones, card = libDraw(r, c.sheet)
			}
			name := fmt.Sprintf("%s %dx%d", c.name, size.X, size.Y)
			count := libCount(zones)
			for kind, n := range c.want {
				if count[kind] < n {
					t.Errorf("%s: %d of part %d, want %d or more (%v)", name, count[kind], kind, n, count)
				}
			}
			if c.name == "artist-no-player" && (count[libPartPlay] != 0 || count[libPartShuffle] != 0) {
				t.Errorf("%s: Play or Shuffle can be tapped with nothing to play on", name)
			}
			if c.name == "keyboard" && (len(zones) != 0 || card != img.Bounds()) {
				t.Errorf("%s: parts %v on %v, want the whole screen the keyboard's", name, count, card)
			}
			for _, z := range zones {
				if !z.r.In(card) || !z.r.In(img.Bounds()) {
					t.Errorf("%s: part %d at %v off the page %v", name, z.kind, z.r, card)
				}
			}
			if c.name == "artists" || c.name == "albums" {
				// The first row of covers: four abreast on the Show 5, five on the Show 8.
				want, top := map[int]int{showWide: 4, show8Wide: 5}[size.X], -1
				n := 0
				for _, z := range zones {
					if z.kind == libPartItem && (top < 0 || z.r.Min.Y == top) {
						top = z.r.Min.Y
						n++
					}
				}
				if n != want {
					t.Errorf("%s: %d covers abreast, want %d", name, n, want)
				}
			}
			for i := 0; i < libTabs && c.want[libPartTab] > 0; i++ {
				x, y := libMiddle(libFind(t, zones, libPartTab, i))
				if z, _, _ := r.libraryAt(x, y); z.kind != libPartTab || z.index != i {
					t.Errorf("%s: tab %d is found as %+v", name, i, z)
				}
			}
			if dir != "" {
				f, err := os.Create(filepath.Join(dir, fmt.Sprintf("library-%s-%dx%d.png", c.name, size.X, size.Y)))
				if err != nil {
					t.Fatal(err)
				}
				png.Encode(f, img)
				f.Close()
			}
		}
	}
}

// A tap on a tab shows it; on an album, its page; Back goes back to the tab where it was scrolled.
// Play and Shuffle play the album on the library's player, replacing what was queued, with the shuffle
// set first (through Home Assistant when Music Assistant has no queue to name); a track plays at a tap
// and is lit. Done puts the library away.
func TestTheLibrarysTaps(t *testing.T) {
	noLibrary(t)
	asked := recordTaps(t)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	sheet := libSheetWith(libAlbums, libEntries("album", 40), "media_player.ma_kitchen")
	sheet.roots[libAlbums].scroll = 150
	d.dashLibrary = &sheet
	draw := func() []mediaZone {
		zones, _ := libDraw(r, d.libraryViewNow().sheet)
		return zones
	}
	tap := func(z mediaZone) { d.libraryTap(libMiddle(z)) }

	zones := draw()
	first := libFind(t, zones, libPartItem, -1)
	tap(first)
	if l := d.libraryViewNow().sheet; len(l.stack) != 1 || l.stack[0].what != libAlbum || l.stack[0].entry.URI != sheet.roots[libAlbums].sections[0].entries[first.index].URI {
		t.Fatalf("a tap on an album opened %+v", l.stack)
	}
	d.editLibrary(func(n *librarySheet) {
		p := n.current()
		p.loaded, p.loading = true, false
		p.sections = []libSection{{word: -1, show: libShowTracks, kind: "track", entries: libEntries("track", 5)}}
	})
	zones = draw()
	album := d.libraryViewNow().sheet.current().entry.URI
	tap(libFind(t, zones, libPartPlay, -1))
	waitUntil(t, "Play's two calls", func() bool { return len(asked()) == 2 })
	if !d.libraryViewNow().sheet.isLit(libLitPlay) {
		t.Error("Play is not lit after a tap")
	}
	tap(libFind(t, zones, libPartShuffle, -1))
	waitUntil(t, "Shuffle's two calls", func() bool { return len(asked()) == 4 })
	tap(libFind(t, zones, libPartItem, 2))
	waitUntil(t, "the track's call", func() bool { return len(asked()) == 5 })
	if !d.libraryViewNow().sheet.isLit(2) {
		t.Error("the track tapped is not lit")
	}
	want := []dashboard.Action{
		{Entity: "media_player.ma_kitchen", Service: "media_player.shuffle_set", Data: map[string]any{"shuffle": false}},
		{Entity: "media_player.ma_kitchen", Service: "music_assistant.play_media", Data: map[string]any{"media_id": album, "media_type": "album", "enqueue": "replace"}},
		{Entity: "media_player.ma_kitchen", Service: "media_player.shuffle_set", Data: map[string]any{"shuffle": true}},
		{Entity: "media_player.ma_kitchen", Service: "music_assistant.play_media", Data: map[string]any{"media_id": album, "media_type": "album", "enqueue": "replace"}},
		{Entity: "media_player.ma_kitchen", Service: "music_assistant.play_media", Data: map[string]any{"media_id": "library://track/2", "media_type": "track", "enqueue": "replace"}},
	}
	if got := fmt.Sprint(asked()); got != fmt.Sprint(want) {
		t.Errorf("asked\n%s\nwant\n%s", got, fmt.Sprint(want))
	}

	tap(libFind(t, zones, libPartBack, -1))
	if l := d.libraryViewNow().sheet; len(l.stack) != 0 || l.tab != libAlbums || l.current().scroll != 150 {
		t.Errorf("Back left the library on %d pages up, tab %d, scrolled %d", len(l.stack), l.tab, l.current().scroll)
	}
	zones = draw()
	tap(libFind(t, zones, libPartTab, libRadio))
	if l := d.libraryViewNow().sheet; l.tab != libRadio {
		t.Errorf("a tap on Radio left the library on tab %d", l.tab)
	}
	zones = draw()
	tap(libFind(t, zones, libPartDone, -1))
	if d.libraryViewNow() != nil {
		t.Error("Done left the library up")
	}
}

// After a play the library goes: for this device's Now Playing page when the music plays here (the
// dashboard put away, and the page brought back even if it was put away for an earlier track), for the
// media sheet it came from when it plays on another speaker, and simply when it came from a card with
// no player.
func TestTheLibraryLeavesAfterAPlay(t *testing.T) {
	noLibrary(t)
	recordTaps(t)
	var leave func()
	libLeaveAfter = func(_ time.Duration, f func()) *time.Timer { leave = f; return nil }
	var sheetFor []string
	libOpenSheet = func(_ *Display, entity string) bool { sheetFor = append(sheetFor, entity); return true }
	for _, c := range []struct {
		name, target, from string
		own                bool
		wantSheet          string
	}{
		{"this device", "media_player.ma_echo_show", "", true, ""},
		{"another speaker", "media_player.ma_kitchen", "media_player.sonos_kitchen", false, "media_player.sonos_kitchen"},
		{"a card with no player", "media_player.ma_kitchen", "", false, ""},
	} {
		libIsOwn = func(target string) bool { return c.own && target == c.target }
		sheetFor, leave = nil, nil
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
		d := &Display{r: r, poke: make(chan struct{}, 1)}
		d.dash, d.away = true, true
		sheet := libSheetWith(libTracks, libEntries("track", 8), c.target)
		sheet.from = c.from
		d.dashLibrary = &sheet
		zones, _ := libDraw(r, sheet)
		d.libraryTap(libMiddle(libFind(t, zones, libPartItem, 0)))
		if d.libraryViewNow() == nil || leave == nil {
			t.Fatalf("%s: the library went before the tap was seen", c.name)
		}
		leave()
		if d.libraryViewNow() != nil {
			t.Errorf("%s: the library is still up", c.name)
		}
		if d.dash == c.own || d.away == c.own {
			t.Errorf("%s: dashboard up %v, Now Playing put away %v", c.name, d.dash, d.away)
		}
		if got := strings.Join(sheetFor, ","); got != c.wantSheet {
			t.Errorf("%s: media sheet opened for %q, want %q", c.name, got, c.wantSheet)
		}
	}
}

// With Music Assistant set up and a queue to name, the shuffle is set through Music Assistant's own
// player_queues/shuffle before the play goes to Home Assistant, and Home Assistant's shuffle_set is not
// asked; a server that refuses it leaves Home Assistant's to try.
func TestTheLibraryShufflesThroughMusicAssistant(t *testing.T) {
	noLibrary(t)
	var mu sync.Mutex
	var events []string
	refuse := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Command string         `json:"command"`
			Args    map[string]any `json:"args"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		events = append(events, fmt.Sprintf("ma %s %v %v", body.Command, body.Args["queue_id"], body.Args["shuffle_enabled"]))
		no := refuse
		mu.Unlock()
		if no {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write([]byte(`null`))
	}))
	defer srv.Close()
	libClient = func() (musicassistant.Client, bool) { return musicassistant.Client{URL: srv.URL, Token: "tok"}, true }
	libQueueOf = func(string) string { return "up0a1b2c3d" }
	prev, prevAfter := sheetTap, libAfter
	sheetTap = func(a dashboard.Action) {
		mu.Lock()
		events = append(events, "ha "+a.Service)
		mu.Unlock()
	}
	libAfter = func(_ time.Duration, f func()) *time.Timer { f(); return nil }
	t.Cleanup(func() { sheetTap, libAfter = prev, prevAfter })
	got := func() string { mu.Lock(); defer mu.Unlock(); return strings.Join(events, "; ") }

	d := &Display{poke: make(chan struct{}, 1)}
	album := libEntries("album", 1)[0]
	d.libPlay("media_player.ma_kitchen", album, "album", true)
	waitUntil(t, "a play", func() bool { return strings.Contains(got(), "play_media") })
	if want := "ma player_queues/shuffle up0a1b2c3d true; ha music_assistant.play_media"; got() != want {
		t.Errorf("asked %q, want %q", got(), want)
	}

	mu.Lock()
	events, refuse = nil, true
	mu.Unlock()
	d.libPlay("media_player.ma_kitchen", album, "album", false)
	waitUntil(t, "a play", func() bool { return strings.Contains(got(), "play_media") })
	if want := "ma player_queues/shuffle up0a1b2c3d false; ha media_player.shuffle_set; ha music_assistant.play_media"; got() != want {
		t.Errorf("refused, asked %q, want %q", got(), want)
	}
}

// The search's field opens the keyboard with what was searched for last; typing and Search searches
// for it, on the search tab; Cancel puts the keyboard away.
func TestTheLibrarysSearchTypes(t *testing.T) {
	noLibrary(t)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	sheet := libSheetWith(libSearch, nil, "media_player.ma_kitchen")
	d.dashLibrary = &sheet
	zones, _ := libDraw(r, sheet)
	d.libraryTap(libMiddle(libFind(t, zones, libPartField, -1)))
	if !d.libraryViewNow().sheet.typing {
		t.Fatal("the field did not open the keyboard")
	}
	for _, k := range []string{"shift", "e", "a", "g", "backspace", "g", "space", "l"} {
		d.libraryKey(k)
	}
	if l := d.libraryViewNow().sheet; l.text != "Eag l" {
		t.Errorf("typed %q", l.text)
	}
	d.libraryKey("join")
	l := d.libraryViewNow().sheet
	if l.typing || l.tab != libSearch || l.roots[libSearch].query != "Eag l" {
		t.Errorf("Search left typing %v, tab %d, query %q", l.typing, l.tab, l.roots[libSearch].query)
	}
	d.editLibrary(func(n *librarySheet) { n.typing = true })
	d.libraryKey("cancel")
	if d.libraryViewNow().sheet.typing {
		t.Error("Cancel left the keyboard up")
	}
}

// libDragBy is a finger coming down at from, moving by dx, dy in steps, and lifting.
func libDragBy(d *Display, from image.Point, dx, dy int) bool {
	d.libDragStart(from.X, from.Y)
	for i := 1; i <= 4; i++ {
		d.libDragMove(from.X+dx*i/4, from.Y+dy*i/4)
	}
	return d.libraryDragEnd()
}

// A finger dragged up the grid scrolls it, and not past either end; a tap then finds the cover drawn
// under it at that scroll. As the grid nears its end, the next of the library is asked for, from where
// it ends, and put after it.
func TestTheLibraryScrollsAndLoadsMore(t *testing.T) {
	noLibrary(t)
	var mu sync.Mutex
	var offsets []float64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api" {
			http.NotFound(w, r) // the artists' pictures
			return
		}
		var body struct {
			Command string         `json:"command"`
			Args    map[string]any `json:"args"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		off, limit := body.Args["offset"].(float64), body.Args["limit"].(float64)
		mu.Lock()
		offsets = append(offsets, off)
		mu.Unlock()
		n := int(min(limit, max(150-off, 0))) // 150 artists in all
		out := libEntries("artist", n)
		for i := range out {
			out[i].Name = fmt.Sprintf("Artist %03d", int(off)+i)
		}
		w.Write(libJSON(out))
	}))
	defer srv.Close()
	libClient = func() (musicassistant.Client, bool) { return musicassistant.Client{URL: srv.URL, Token: "tok"}, true }

	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	d.openLibrary("media_player.ma_kitchen", "", false)
	count := func() int {
		l := d.libraryViewNow()
		if l == nil {
			return 0
		}
		if p := l.sheet.roots[libArtists]; len(p.sections) > 0 && !p.loading {
			return len(p.sections[0].entries)
		}
		return 0
	}
	waitUntil(t, "the first artists", func() bool { return count() == 48 })
	zones, _ := libDraw(r, d.libraryViewNow().sheet)
	content := libFind(t, zones, libPartContent, -1)
	at := image.Pt(content.r.Min.X+40, content.r.Min.Y+40)
	before, _, _ := r.libraryAt(at.X, at.Y)
	g := r.libGeometry()
	step := g.tileH + r.s(10)

	// One row of covers up.
	if !libDragBy(d, image.Pt(at.X, content.r.Max.Y-20), 0, -step) {
		t.Fatal("a drag up did not scroll")
	}
	if s := d.libraryViewNow().sheet.current().scroll; s != step {
		t.Errorf("scrolled %d, want %d", s, step)
	}
	libDraw(r, d.libraryViewNow().sheet)
	after, _, _ := r.libraryAt(at.X, at.Y)
	if before.kind != libPartItem || after.kind != libPartItem || after.index != before.index+g.cols {
		t.Errorf("under the finger: %+v before, %+v after a row's scroll", before, after)
	}
	libDragBy(d, at, 0, 5000) // back past the top
	if s := d.libraryViewNow().sheet.current().scroll; s != 0 {
		t.Errorf("scrolled %d above the top", s)
	}
	if libDragBy(d, at, 4, 4) {
		t.Error("a finger that wandered a little scrolled")
	}

	// To the end of what there is: the next is asked for as the next frame is put together.
	libDragBy(d, image.Pt(at.X, content.r.Max.Y-20), 0, -5000)
	libDraw(r, d.libraryViewNow().sheet)
	r.zmu.Lock()
	contentH, viewH, scrolled := r.libContentH, r.libViewH, r.libScrolled
	r.zmu.Unlock()
	if scrolled != contentH-viewH {
		t.Errorf("scrolled %d of %d in %d", scrolled, contentH, viewH)
	}
	d.libraryViewNow()
	waitUntil(t, "more artists", func() bool { return count() > 48 })
	mu.Lock()
	got := fmt.Sprint(offsets[:2])
	mu.Unlock()
	if got != "[0 48]" {
		t.Errorf("asked from %s, want [0 48]", got)
	}
	if e := d.libraryViewNow().sheet.roots[libArtists].sections[0].entries; e[48].Name != "Artist 048" {
		t.Errorf("the next part begins with %q", e[48].Name)
	}
}

// An artist's albums are a row that a finger drags along, apart from the page, and a tap finds the
// album drawn under it there.
func TestAnArtistsAlbumsScrollAlong(t *testing.T) {
	noLibrary(t)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, show8Wide, show8High)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	sheet := libSheetWith(libArtists, libEntries("artist", 4), "media_player.ma_kitchen")
	artist := newLibPage(libArtist, libEntries("artist", 1)[0])
	artist.loaded = true
	artist.sections = []libSection{{word: libAlbums, show: libShowRow, kind: "album", entries: libEntries("album", 12)},
		{word: libWordTopTracks, show: libShowTracks, kind: "track", entries: libEntries("track", 5)}}
	sheet.stack = []libPage{artist}
	d.dashLibrary = &sheet
	zones, _ := libDraw(r, sheet)
	row := libFind(t, zones, libPartRow, 0)
	at := image.Pt(row.r.Min.X+r.s(40), row.r.Min.Y+r.s(40))
	before, _, _ := r.libraryAt(at.X, at.Y)
	g := r.libGeometry()
	if !libDragBy(d, image.Pt(row.r.Max.X-r.s(40), at.Y), -(g.tile + g.gap), r.s(3)) {
		t.Fatal("a drag along the row did not scroll it")
	}
	p := d.libraryViewNow().sheet.current()
	if libAlong(p, 0) != g.tile+g.gap || p.scroll != 0 {
		t.Errorf("along %d, down %d, want %d along", libAlong(p, 0), p.scroll, g.tile+g.gap)
	}
	libDraw(r, d.libraryViewNow().sheet)
	after, _, _ := r.libraryAt(at.X, at.Y)
	if before.kind != libPartItem || after.index != before.index+1 {
		t.Errorf("under the finger: %+v before, %+v after an album's scroll", before, after)
	}
	libDragBy(d, at, -100000, 0)
	r.zmu.Lock()
	most := r.libRowMax[0]
	r.zmu.Unlock()
	if p := d.libraryViewNow().sheet.current(); libAlong(p, 0) != most || most <= 0 {
		t.Errorf("along %d past its end %d", libAlong(p, 0), most)
	}
}

// An empty tab says so and offers the search, which a tap goes to.
func TestAnEmptyTabOffersTheSearch(t *testing.T) {
	noLibrary(t)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	for _, tab := range []int{libRadio, libPlaylists} {
		sheet := libSheetWith(tab, nil, "media_player.ma_kitchen")
		d.dashLibrary = &sheet
		zones, _ := libDraw(r, sheet)
		d.libraryTap(libMiddle(libFind(t, zones, libPartGoSearch, -1)))
		if l := d.libraryViewNow().sheet; l.tab != libSearch {
			t.Errorf("tab %d: the search button went to tab %d", tab, l.tab)
		}
	}
}

// A library of saved tracks alone: its Artists and Albums tabs are the tracks' artists and albums, each
// once and by name, an album without a picture taking its track's, and only those with an id to open.
func TestArtistsAndAlbumsFromSavedTracks(t *testing.T) {
	track := func(name, album, albumID string, artists ...string) musicassistant.Entry {
		e := musicassistant.Entry{Name: name, URI: "spotify://track/" + name, MediaType: "track",
			Image: &musicassistant.Image{Type: "thumb", ProxyID: "cover-" + albumID}}
		if album != "" {
			e.Album = &musicassistant.Entry{Name: album, ItemID: albumID, Provider: "spotify--a", URI: "spotify://album/" + albumID}
		}
		for _, a := range artists {
			e.Artists = append(e.Artists, musicassistant.Entry{Name: a, ItemID: "id-" + a, Provider: "spotify--a", URI: "spotify://artist/" + a})
		}
		return e
	}
	tracks := []musicassistant.Entry{
		track("One", "Zephyr", "z1", "Moss", "Alder"),
		track("Two", "Zephyr", "z1", "Moss"),
		track("Three", "aurora", "a1", "Birch"),
		track("Four", "", "", "Birch"),
		{Name: "Five", Album: &musicassistant.Entry{Name: "Named only"}},
	}
	artists, albums := libArtistsAndAlbums(tracks)
	names := func(list []musicassistant.Entry) string {
		var n []string
		for _, e := range list {
			n = append(n, e.Name+":"+e.MediaType)
		}
		return strings.Join(n, ",")
	}
	if got := names(artists); got != "Alder:artist,Birch:artist,Moss:artist" {
		t.Errorf("artists %s", got)
	}
	if got := names(albums); got != "aurora:album,Zephyr:album" {
		t.Errorf("albums %s", got)
	}
	if a := albums[1]; a.Provider != "spotify--a" || a.ItemID != "z1" || a.Image == nil || a.Image.ProxyID != "cover-z1" || a.By() != "Moss, Alder" {
		t.Errorf("an album from its tracks: %+v", a)
	}

	// The tab: the library's own list is empty, so the tracks are read and the tab made of them, once
	// for both tabs.
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Command string         `json:"command"`
			Args    map[string]any `json:"args"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		asked = append(asked, body.Command)
		mu.Unlock()
		if body.Command == "music/tracks/library_items" && body.Args["offset"].(float64) == 0 {
			w.Write(libJSON(tracks))
			return
		}
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	libDerived.Lock()
	libDerived.server = ""
	libDerived.Unlock()
	c := musicassistant.Client{URL: srv.URL, Token: "tok"}
	sections, more, err := fetchLibPage(context.Background(), c, newLibPage(libAlbums, musicassistant.Entry{}), 48)
	if err != nil || more || len(sections) != 1 || !sections[0].derived || names(sections[0].entries) != "aurora:album,Zephyr:album" {
		t.Errorf("the Albums tab from tracks: %+v %v %v", sections, more, err)
	}
	if _, _, err := fetchLibPage(context.Background(), c, newLibPage(libArtists, musicassistant.Entry{}), 48); err != nil {
		t.Error(err)
	}
	mu.Lock()
	got := strings.Join(asked, ",")
	mu.Unlock()
	if got != "music/albums/library_items,music/tracks/library_items,music/artists/library_items" {
		t.Errorf("asked %s", got)
	}
}

// libJSON is entries as the server sends them.
func libJSON(tracks []musicassistant.Entry) []byte {
	var out []map[string]any
	named := func(e musicassistant.Entry) map[string]any {
		return map[string]any{"name": e.Name, "item_id": e.ItemID, "provider": e.Provider, "uri": e.URI}
	}
	for _, t := range tracks {
		m := map[string]any{"name": t.Name, "uri": t.URI, "media_type": t.MediaType, "item_id": t.ItemID, "provider": t.Provider}
		if t.Image != nil {
			m["image"] = map[string]any{"type": t.Image.Type, "proxy_id": t.Image.ProxyID, "path": "/x.jpg"}
		}
		if t.Album != nil {
			m["album"] = named(*t.Album)
		}
		var artists []any
		for _, a := range t.Artists {
			artists = append(artists, named(a))
		}
		m["artists"] = artists
		out = append(out, m)
	}
	b, _ := json.Marshal(out)
	return b
}

// A card whose tap navigates to the library's hash opens the library instead of a view, to play on
// Music Assistant's player for the card's media player, found off the finger's goroutine; a card
// without a player plays on this device's.
func TestALibraryCardOpensTheLibrary(t *testing.T) {
	noLibrary(t)
	found := make(chan string, 2)
	libFindPlayer = func(entity string) string {
		found <- entity
		if entity == "media_player.sonos_kitchen" {
			return "media_player.ma_kitchen"
		}
		return ""
	}
	page := dashboard.Drawn{Sections: []dashboard.Section{{Blocks: []dashboard.Block{{Tiles: []dashboard.Tile{
		{Name: "Library", Icon: "bookshelf", Tap: &dashboard.Action{View: dashboard.LibraryPath, Entity: "media_player.sonos_kitchen"}},
		{Name: "Here", Icon: "bookshelf", Tap: &dashboard.Action{View: dashboard.LibraryPath}},
	}}}}}}
	for i, want := range []struct{ entity, target string }{{"media_player.sonos_kitchen", "media_player.ma_kitchen"}, {"", ""}} {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
		r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: page})
		d := &Display{r: r, poke: make(chan struct{}, 1)}
		tiles, _ := r.dash()
		if len(tiles) != 2 {
			t.Fatalf("%d tiles drawn", len(tiles))
		}
		d.drawnTap((tiles[i].r.Min.X+tiles[i].r.Max.X)/2, (tiles[i].r.Min.Y+tiles[i].r.Max.Y)/2)
		if d.libraryViewNow() == nil {
			t.Fatalf("card %d did not open the library", i)
		}
		select {
		case e := <-found:
			if e != want.entity {
				t.Errorf("card %d looked for the player of %q, want %q", i, e, want.entity)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("card %d looked for no player", i)
		}
		waitUntil(t, "the player", func() bool { return !d.libraryViewNow().sheet.finding })
		if l := d.libraryViewNow().sheet; l.target != want.target || l.from != want.entity {
			t.Errorf("card %d plays on %q from %q, want %q", i, l.target, l.from, want.target)
		}
	}
}

// The media sheet offers the library beside Done only where Music Assistant plays to its player, and
// a tap on it puts the library up in the sheet's place, to play on that player, to come back to.
func TestTheMediaSheetsLibraryButton(t *testing.T) {
	noLibrary(t)
	now := dashboard.MediaNow{Entity: "media_player.sonos_kitchen", Name: "Kitchen", State: "idle", Volume: 0.3}
	for _, c := range []struct {
		name  string
		lists dashboard.MediaLists
		shown bool
	}{
		{"music assistant", dashboard.MediaLists{MusicAssistant: true, Target: "media_player.ma_kitchen"}, true},
		{"not to this player", dashboard.MediaLists{MusicAssistant: true}, false},
		{"none", dashboard.MediaLists{}, false},
	} {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
		sheet := mediaSheet{entity: now.Entity, lists: c.lists, volume: -1}
		v := mediaView{sheet: sheet, now: now}
		r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls(), dashMedia: &v})
		r.zmu.Lock()
		zones, card := r.mediaZones, r.mediaCard
		r.zmu.Unlock()
		var lib *mediaZone
		for i := range zones {
			if zones[i].kind == mediaPartLibrary {
				lib = &zones[i]
			}
		}
		if (lib != nil) != c.shown {
			t.Errorf("%s: Library shown %v", c.name, lib != nil)
		}
		if lib == nil {
			continue
		}
		if !lib.r.In(card) {
			t.Errorf("%s: Library at %v off the sheet %v", c.name, lib.r, card)
		}
		d := &Display{r: r, poke: make(chan struct{}, 1)}
		d.dashMedia = &sheet
		d.mediaTap(libMiddle(*lib))
		l := d.libraryViewNow()
		if l == nil || l.sheet.target != "media_player.ma_kitchen" || l.sheet.from != now.Entity || l.sheet.finding || d.dashMedia != nil {
			t.Errorf("%s: Library opened %+v, the media sheet left %v", c.name, l, d.dashMedia != nil)
		}
	}
}

// A picture the proxy will not make at the size asked for (400) is asked for once more as it is, and
// comes back square at the size it is drawn, with its corners clear, or round for an artist.
func TestALibraryPictureFallsBackToItsOriginal(t *testing.T) {
	var asked []string
	prev := libFetch
	libFetch = func(_ context.Context, u string) ([]byte, int, error) {
		asked = append(asked, u)
		if strings.Contains(u, "size=") {
			return nil, http.StatusBadRequest, nil
		}
		img := image.NewRGBA(image.Rect(0, 0, 300, 200))
		for i := range img.Pix {
			img.Pix[i] = 200
			if i%4 == 3 {
				img.Pix[i] = 255
			}
		}
		var b bytes.Buffer
		png.Encode(&b, img)
		return b.Bytes(), http.StatusOK, nil
	}
	t.Cleanup(func() { libFetch = prev })
	img, err := loadLibPicture("http://ma.invalid:8095/imageproxy/abc?size=256", 100, false)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(asked) != "[http://ma.invalid:8095/imageproxy/abc?size=256 http://ma.invalid:8095/imageproxy/abc]" {
		t.Errorf("asked %v", asked)
	}
	if img.Rect.Dx() != 100 || img.Rect.Dy() != 100 || img.RGBAAt(0, 0).A != 0 || img.RGBAAt(50, 50).A != 255 || img.RGBAAt(8, 8).A != 255 {
		t.Errorf("a picture of %v, corner %v, middle %v", img.Rect, img.RGBAAt(0, 0), img.RGBAAt(50, 50))
	}
	round, err := loadLibPicture("http://ma.invalid:8095/imageproxy/abc", 100, true)
	if err != nil || round.RGBAAt(8, 8).A != 0 || round.RGBAAt(1, 50).A < 200 || round.RGBAAt(50, 50).A != 255 {
		t.Errorf("a round picture: %v, inside corner %v, edge %v", err, round.RGBAAt(8, 8), round.RGBAAt(1, 50))
	}
	if _, ok := withoutSize("http://ma.invalid/imageproxy/abc"); ok {
		t.Error("a picture without a size has one to drop")
	}
}

// The library keeps as many pictures as it may, the ones drawn last, and lets the oldest go.
func TestTheLibrarysPicturesAreBounded(t *testing.T) {
	prev := libFetch
	libFetch = func(_ context.Context, u string) ([]byte, int, error) {
		var b bytes.Buffer
		png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 8)))
		return b.Bytes(), http.StatusOK, nil
	}
	t.Cleanup(func() { libFetch = prev })
	p := &libPictures{}
	for i := 0; i < libPicsMost+20; i++ {
		p.picture(fmt.Sprintf("http://ma.invalid/imageproxy/%d", i), 8, false)
		for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(time.Millisecond) {
			p.mu.Lock()
			waiting := len(p.asked)
			p.mu.Unlock()
			if waiting == 0 {
				break
			}
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.order.Len() != libPicsMost || len(p.kept) != libPicsMost {
		t.Errorf("%d pictures kept, want %d", p.order.Len(), libPicsMost)
	}
	if _, ok := p.kept[libPicKey("http://ma.invalid/imageproxy/0", 8, false)]; ok {
		t.Error("the oldest picture is still kept")
	}
}

// A track's length is written as a music app writes it.
func TestLibDuration(t *testing.T) {
	for in, want := range map[int]string{0: "", 7: "0:07", 187: "3:07", 3765: "1:02:45"} {
		if got := libDuration(in); got != want {
			t.Errorf("%d seconds: %q, want %q", in, got, want)
		}
	}
}
