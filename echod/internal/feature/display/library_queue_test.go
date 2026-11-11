//go:build !dot && !spot

package display

import (
	"encoding/json"
	"fmt"
	"image"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/musicassistant"
)

// libQueueTracks are n tracks as a queue holds them, the first playing: each with its queue item id.
func libQueueTracks(n int) []musicassistant.Entry {
	out := libEntries("track", n)
	for i := range out {
		out[i].QueueItemID = fmt.Sprintf("q%d", i)
	}
	return out
}

// libFakeMA is a Music Assistant that notes each command it is sent ("command {args}") and refuses the
// commands in refuse as a token without the right; its queue is libQueueTracks(8).
func libFakeMA(t *testing.T, refuse ...string) func() []string {
	t.Helper()
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Command string          `json:"command"`
			Args    json.RawMessage `json:"args"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		got = append(got, body.Command+" "+string(body.Args))
		mu.Unlock()
		if slices.Contains(refuse, body.Command) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch body.Command {
		case "player_queues/get":
			w.Write([]byte(`{"current_index":0}`))
		case "player_queues/items":
			var items []map[string]any
			for _, e := range libQueueTracks(8) {
				items = append(items, map[string]any{"queue_item_id": e.QueueItemID, "name": e.Name,
					"media_item": map[string]any{"uri": e.URI, "name": e.Name, "media_type": "track"}})
			}
			json.NewEncoder(w).Encode(items)
		default:
			w.Write([]byte(`null`))
		}
	}))
	t.Cleanup(srv.Close)
	libClient = func() (musicassistant.Client, bool) { return musicassistant.Client{URL: srv.URL, Token: "tok"}, true }
	return func() []string { mu.Lock(); defer mu.Unlock(); return slices.Clone(got) }
}

// asked is whether one of the commands sent starts with each of want, in that order.
func asked(got []string, want ...string) bool {
	i := 0
	for _, g := range got {
		if i < len(want) && strings.HasPrefix(g, want[i]) {
			i++
		}
	}
	return i == len(want)
}

// What the sheet offers: an item of a page the ways to play it and to mark it; an item of the queue
// to play from, move up (unless it is next already) and take out; the one playing only to be marked.
// A favorite is offered to be unmarked only when the library has it.
func TestTheActionSheetOffersWhatFits(t *testing.T) {
	plain := libEntries("track", 1)[0]
	fav := plain
	fav.Favorite = true
	service := musicassistant.Entry{URI: "ytmusic--x://track/abc", Provider: "ytmusic--x", ItemID: "abc", Favorite: true}
	for _, c := range []struct {
		name    string
		e       musicassistant.Entry
		kind    string
		onQueue bool
		pos     int
		want    []int
	}{
		{"a track", plain, "track", false, 0, []int{libMenuPlay, libMenuNext, libMenuAdd, libMenuRadio, libMenuFavorite}},
		{"a favorite", fav, "album", false, 0, []int{libMenuPlay, libMenuNext, libMenuAdd, libMenuRadio, libMenuUnfavorite}},
		{"a service's favorite", service, "track", false, 0, []int{libMenuPlay, libMenuNext, libMenuAdd, libMenuRadio}},
		{"queued, playing", plain, "track", true, 0, []int{libMenuFavorite}},
		{"queued, next", plain, "track", true, 1, []int{libMenuPlayFrom, libMenuRemove, libMenuFavorite}},
		{"queued, later", fav, "track", true, 4, []int{libMenuPlayFrom, libMenuMoveNext, libMenuRemove, libMenuUnfavorite}},
		{"queued chapter", plain, "chapter", true, 2, []int{libMenuPlayFrom, libMenuMoveNext, libMenuRemove}},
	} {
		if got := libMenuFor(c.e, c.kind, c.onQueue, c.pos); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// A tap on an item of the queue plays the queue from it and leaves the library; a tap on the one
// playing does nothing. A token that may not use the player has the item moved to play next instead,
// and Home Assistant skips to it; an item that is next already is only skipped to.
func TestATappedQueueItemPlaysFromIt(t *testing.T) {
	noLibrary(t)
	sent := libFakeMA(t, "player_queues/play_index")
	var mu sync.Mutex
	nexts := 0
	prev := libNextTrack
	libNextTrack = func(string) { mu.Lock(); nexts++; mu.Unlock() }
	t.Cleanup(func() { libNextTrack = prev })
	left := 0
	libLeaveAfter = func(time.Duration, func()) *time.Timer { left++; return nil }

	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	sheet := libSheetWith(libQueue, nil, "media_player.ma_kitchen")
	sheet.roots[libQueue].queue = "up0a1b2c3d"
	sheet.roots[libQueue].sections = []libSection{{word: -1, show: libShowTracks, kind: "track", entries: libQueueTracks(8)}}
	d.dashLibrary = &sheet
	zones, _ := libDraw(r, sheet)

	d.libraryTap(libMiddle(libFind(t, zones, libPartItem, 0)))
	if got := sent(); len(got) != 0 || left != 0 {
		t.Fatalf("a tap on what is playing sent %v and left %d times", got, left)
	}

	d.libraryTap(libMiddle(libFind(t, zones, libPartItem, 3)))
	waitUntil(t, "a skip", func() bool { mu.Lock(); defer mu.Unlock(); return nexts == 1 })
	if got := sent(); !asked(got, `player_queues/play_index {"index":"q3"`, `player_queues/move_item {"pos_shift":0,"queue_id":"up0a1b2c3d","queue_item_id":"q3"}`) {
		t.Errorf("refused, sent %v; want play_index, then q3 moved up", got)
	}
	if left != 1 {
		t.Errorf("the library left %d times, want once", left)
	}

	before := len(sent())
	d.libraryTap(libMiddle(libFind(t, zones, libPartItem, 1)))
	waitUntil(t, "a second skip", func() bool { mu.Lock(); defer mu.Unlock(); return nexts == 2 })
	if got := sent()[before:]; len(got) != 1 || !strings.HasPrefix(got[0], "player_queues/play_index") {
		t.Errorf("the next item sent %v; want only play_index, no move", got)
	}
}

// With a token that may use the player, the tap is play_index alone: nothing is moved or skipped.
func TestATappedQueueItemPlaysFromItWhenAllowed(t *testing.T) {
	noLibrary(t)
	sent := libFakeMA(t)
	prev := libNextTrack
	libNextTrack = func(string) { t.Error("skipped with a token allowed to play from the queue") }
	t.Cleanup(func() { libNextTrack = prev })
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	sheet := libSheetWith(libQueue, nil, "media_player.ma_kitchen")
	sheet.roots[libQueue].queue = "up0a1b2c3d"
	sheet.roots[libQueue].sections = []libSection{{word: -1, show: libShowTracks, kind: "track", entries: libQueueTracks(8)}}
	d.dashLibrary = &sheet
	zones, _ := libDraw(r, sheet)
	d.libraryTap(libMiddle(libFind(t, zones, libPartItem, 5)))
	waitUntil(t, "play_index", func() bool { return len(sent()) == 1 })
	time.Sleep(50 * time.Millisecond)
	if got := sent(); len(got) != 1 || got[0] != `player_queues/play_index {"index":"q5","queue_id":"up0a1b2c3d"}` {
		t.Errorf("sent %v", got)
	}
}

// A held item of the queue opens its sheet; Remove takes it out and Play next moves it up, and the
// queue is read again after each.
func TestAHeldQueueItemIsMovedOrRemoved(t *testing.T) {
	noLibrary(t)
	sent := libFakeMA(t)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	// The queue tab read as the device reads it, so the page learns its queue as it loads.
	sheet := libSheetWith(libQueue, nil, "media_player.ma_kitchen")
	sheet.roots[libQueue].loaded = false
	libQueueOf = func(string) string { return "up0a1b2c3d" }
	d.dashLibrary = &sheet
	d.libLoad(sheet.roots[libQueue].id)
	waitUntil(t, "the queue read", func() bool {
		p := d.libraryViewNow().sheet.roots[libQueue]
		return p.loaded && len(p.sections) == 1 && len(p.sections[0].entries) == 8
	})
	sent() // what loading sent is not what is checked below

	for _, c := range []struct {
		choice int
		want   string
	}{
		{libMenuRemove, `player_queues/delete_item {"item_id_or_index":"q4","queue_id":"up0a1b2c3d"}`},
		{libMenuMoveNext, `player_queues/move_item {"pos_shift":0,"queue_id":"up0a1b2c3d","queue_item_id":"q4"}`},
	} {
		zones, _ := libDraw(r, *d.dashLibrary)
		if !d.libraryHold(libMiddle(libFind(t, zones, libPartItem, 4))) {
			t.Fatal("a held queue item did not open its sheet")
		}
		m := d.dashLibrary.menu
		if m.queue != "up0a1b2c3d" || m.pos != 4 || m.entry.QueueItemID != "q4" {
			t.Fatalf("the sheet is %+v", m)
		}
		before := len(sent())
		zones, _ = libDraw(r, *d.dashLibrary)
		d.libraryTap(libMiddle(libFind(t, zones, libPartMenu, slices.Index(m.choices, c.choice))))
		waitUntil(t, "the queue read again", func() bool {
			return asked(sent()[before:], c.want, "player_queues/get", "player_queues/items")
		})
	}

	// The one playing can only be marked a favorite.
	zones, _ := libDraw(r, *d.dashLibrary)
	waitUntil(t, "the queue shown again", func() bool { return !d.libraryViewNow().sheet.roots[libQueue].loading })
	if !d.libraryHold(libMiddle(libFind(t, zones, libPartItem, 0))) {
		t.Fatal("the playing item held did not open its sheet")
	}
	if got := d.dashLibrary.menu.choices; !slices.Equal(got, []int{libMenuFavorite}) {
		t.Errorf("the playing item offers %v", got)
	}
}

// The Now Playing page's Library button shows only with Music Assistant set up and a dashboard the
// library can be drawn over. It brings the library back as a play on this device left it: the tab, the
// album opened from it, how far each was scrolled, the sheet put away, nothing read again; a queue tab
// is read afresh. With no library left, it opens a new one on the Artists tab.
func TestNowPlayingGoesBackToTheLibrary(t *testing.T) {
	noLibrary(t)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Dashboard().Mode(config.DashboardDrawn)
	t.Cleanup(func() { _ = config.Set().Dashboard().Mode(config.DashboardOff); libLast.Store(nil) })
	libLast.Store(nil)
	if w := nowPlayingLibraryWord(); w != "" {
		t.Errorf("without Music Assistant the button says %q", w)
	}
	sent := libFakeMA(t)
	if w := nowPlayingLibraryWord(); w != libWord(libWordLibrary) {
		t.Errorf("the button says %q", w)
	}
	libIsOwn = func(string) bool { return true }
	const target = "media_player.ma_kitchen"

	d := &Display{poke: make(chan struct{}, 1)}
	d.reopenLibrary()
	if d.dashLibrary == nil || d.dashLibrary.tab != libArtists || !d.dash {
		t.Fatalf("with none left, the button opened %+v (dashboard up %v)", d.dashLibrary, d.dash)
	}
	waitUntil(t, "the new library's artists asked for", func() bool { return len(sent()) > 0 })

	sheet := libSheetWith(libAlbums, libEntries("album", 30), target)
	sheet.roots[libAlbums].scroll = 240
	opened := newLibPage(libAlbum, libEntries("album", 4)[3])
	opened.loaded, opened.scroll = true, 120
	opened.sections = []libSection{{word: -1, show: libShowTracks, kind: "track", entries: libEntries("track", 12)}}
	sheet.stack = []libPage{opened}
	sheet.menu = libMenu{open: true}
	d.dashLibrary = &sheet
	d.libLeave(target, "")
	if d.dashLibrary != nil {
		t.Fatal("the play did not put the library away")
	}
	before := len(sent())
	d.reopenLibrary()
	l := d.dashLibrary
	if l == nil || !d.dash {
		t.Fatal("the button did not bring the library back")
	}
	if l.tab != libAlbums || len(l.stack) != 1 || l.stack[0].entry.URI != opened.entry.URI || l.stack[0].scroll != 120 ||
		l.roots[libAlbums].scroll != 240 || l.menu.open || l.target != target {
		t.Errorf("came back to tab %d, %d pages, scrolled %d/%d, sheet up %v", l.tab, len(l.stack), l.roots[libAlbums].scroll,
			l.stack[0].scroll, l.menu.open)
	}
	time.Sleep(50 * time.Millisecond)
	if got := sent()[before:]; len(got) != 0 {
		t.Errorf("an album already read was read again: %v", got)
	}

	// Left on the queue: read afresh.
	libQueueOf = func(string) string { return "up0a1b2c3d" }
	queue := libSheetWith(libQueue, nil, target)
	queue.roots[libQueue].sections = []libSection{{word: -1, show: libShowTracks, kind: "track", entries: libQueueTracks(2)}}
	d.dashLibrary = &queue
	d.libLeave(target, "")
	before = len(sent())
	d.reopenLibrary()
	waitUntil(t, "the queue read again", func() bool { return asked(sent()[before:], "player_queues/get", "player_queues/items") })
}

// Add to favorites marks the item at the server and on the page, so its sheet then offers to remove it,
// which unmarks it by its library id.
func TestTheSheetMarksFavorites(t *testing.T) {
	noLibrary(t)
	sent := libFakeMA(t)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	albums := libEntries("album", 6)
	sheet := libSheetWith(libAlbums, albums, "media_player.ma_kitchen")
	d.dashLibrary = &sheet

	choose := func(choice int) {
		t.Helper()
		zones, _ := libDraw(r, *d.dashLibrary)
		if !d.libraryHold(libMiddle(libFind(t, zones, libPartItem, 2))) {
			t.Fatal("the album held did not open its sheet")
		}
		i := slices.Index(d.dashLibrary.menu.choices, choice)
		if i < 0 {
			t.Fatalf("the sheet offers %v, not %d", d.dashLibrary.menu.choices, choice)
		}
		zones, _ = libDraw(r, *d.dashLibrary)
		d.libraryTap(libMiddle(libFind(t, zones, libPartMenu, i)))
	}
	choose(libMenuFavorite)
	waitUntil(t, "the album marked", func() bool {
		return d.libraryViewNow().sheet.roots[libAlbums].sections[0].entries[2].Favorite
	})
	if got := sent(); !asked(got, `music/favorites/add_item {"item":"library://album/2"}`) {
		t.Errorf("sent %v", got)
	}
	if albums[2].Favorite {
		t.Error("the page's entries were changed in place")
	}

	choose(libMenuUnfavorite)
	waitUntil(t, "the album unmarked", func() bool {
		return !d.libraryViewNow().sheet.roots[libAlbums].sections[0].entries[2].Favorite
	})
	if got := sent(); !asked(got, `music/favorites/add_item`, `music/favorites/remove_item {"library_item_id":"2","media_type":"album"}`) {
		t.Errorf("sent %v", got)
	}
}
