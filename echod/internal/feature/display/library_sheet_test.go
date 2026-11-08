//go:build !dot && !spot

package display

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
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

// libEntries are n made-up items of a media type, each with a picture.
func libEntries(kind string, n int) []musicassistant.Entry {
	out := make([]musicassistant.Entry, n)
	for i := range out {
		e := musicassistant.Entry{ItemID: fmt.Sprint(i), Provider: "library", MediaType: kind,
			URI: fmt.Sprintf("library://%s/%d", kind, i), Name: fmt.Sprintf("%s %d", strings.ToUpper(kind[:1])+kind[1:], i+1)}
		e.Artists = append(e.Artists, struct {
			Name string `json:"name"`
		}{"The Example Band"})
		e.Image = &musicassistant.Image{Type: "thumb", ProxyID: fmt.Sprintf("%s-%d", kind, i)}
		out[i] = e
	}
	return out
}

// libSheetWith is a library up on a tab with entries loaded, playing on target.
func libSheetWith(tab int, entries []musicassistant.Entry, target string) librarySheet {
	l := librarySheet{target: target, set: true, base: "http://ma.invalid:8095", tab: tab}
	for i := range l.roots {
		l.roots[i] = newLibPage(i, musicassistant.Entry{})
	}
	p := &l.roots[tab]
	p.loaded = true
	if tab < libSearch {
		p.sections = []libSection{{word: -1, grid: tab != libTracks, kind: libTypes[tab], entries: entries}}
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
// and no player is found.
func noLibrary(t *testing.T) {
	pc, pf := libClient, libFindPlayer
	libClient = func() (musicassistant.Client, bool) { return musicassistant.Client{}, false }
	libFindPlayer = func(string) string { return "" }
	t.Cleanup(func() { libClient, libFindPlayer = pc, pf })
}

// The library over the page has its six tabs and Done along the top, a grid of artists under them
// paged by dots, every part on the page, on the Show 5 and the Show 8; an album opened from it has
// Back, Play and Shuffle and its tracks in rows. Each tab is found where it is drawn. With SHOW_PREVIEW
// set, each is written there, with made-up covers.
func TestTheLibraryPageLaysOutItsParts(t *testing.T) {
	for _, icon := range []string{"chevron-left", "shuffle-variant", "magnify", "account-music", "album",
		"playlist-music", "radio", "music-note", "bookshelf", "play"} {
		if _, ok := mdi.Rune(icon); !ok {
			t.Errorf("no icon %q", icon)
		}
	}
	dir := os.Getenv("SHOW_PREVIEW")
	if dir != "" {
		// Covers made up, one color each, served as the proxy would.
		prev := libFetch
		libFetch = func(_ context.Context, u string) ([]byte, int, error) {
			h := 0
			for _, c := range u {
				h = h*31 + int(c)
			}
			img := image.NewRGBA(image.Rect(0, 0, 64, 64))
			for y := 0; y < 64; y++ {
				for x := 0; x < 64; x++ {
					img.Set(x, y, color.RGBA{uint8(h>>3) + uint8(x), uint8(h>>9) + uint8(y), uint8(h >> 15), 255})
				}
			}
			var b bytes.Buffer
			png.Encode(&b, img)
			return b.Bytes(), 200, nil
		}
		t.Cleanup(func() { libFetch = prev })
	}
	album := libSheetWith(libAlbums, libEntries("album", 30), "media_player.ma_kitchen")
	opened := newLibPage(libAlbum, libEntries("album", 1)[0])
	opened.loaded = true
	tracks := libEntries("track", 14)
	for i := range tracks {
		tracks[i].Album = &struct {
			Name string `json:"name"`
		}{"Album 1"}
	}
	opened.sections = []libSection{{word: -1, kind: "track", entries: tracks}}
	album.stack = []libPage{opened}
	artist := newLibPage(libArtist, libEntries("artist", 1)[0])
	artist.loaded = true
	artist.sections = []libSection{{word: libAlbums, grid: true, kind: "album", entries: libEntries("album", 4)},
		{word: libWordTopTracks, kind: "track", entries: tracks[:6]}}
	artistSheet := libSheetWith(libArtists, libEntries("artist", 40), "")
	artistSheet.stack = []libPage{artist}
	search := libSheetWith(libSearch, nil, "media_player.ma_kitchen")
	search.roots[libSearch].query = "example"
	search.roots[libSearch].sections = []libSection{{word: libArtists, grid: true, kind: "artist", entries: libEntries("artist", 3)},
		{word: libTracks, kind: "track", entries: tracks[:3]}}

	typing := search
	typing.typing, typing.text = true, "eagles"

	for _, size := range []image.Point{{showWide, showHigh}, {show8Wide, show8High}} {
		for _, c := range []struct {
			name  string
			sheet librarySheet
			want  map[int]int // at least as many of each part
			pages bool
		}{
			{"artists", libSheetWith(libArtists, libEntries("artist", 40), "media_player.ma_kitchen"),
				map[int]int{libPartTab: libTabs, libPartDone: 1, libPartItem: 8, libPartPage: 1}, true},
			{"tracks", libSheetWith(libTracks, tracks, "media_player.ma_kitchen"),
				map[int]int{libPartTab: libTabs, libPartItem: 5}, true},
			{"album", album, map[int]int{libPartBack: 1, libPartPlay: 1, libPartShuffle: 1, libPartDone: 1, libPartItem: 5}, true},
			{"artist-no-player", artistSheet, map[int]int{libPartBack: 1, libPartDone: 1, libPartItem: 4}, true},
			{"search", search, map[int]int{libPartTab: libTabs, libPartField: 1, libPartItem: 3}, false},
			{"not-set-up", librarySheet{}, map[int]int{libPartDone: 1}, false},
			{"keyboard", typing, map[int]int{}, false},
		} {
			img := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
			r := newRenderer(img)
			zones, card := libDraw(r, c.sheet)
			if dir != "" {
				// Once more, when the covers asked for have come.
				for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
					libPics.mu.Lock()
					waiting := len(libPics.asked)
					libPics.mu.Unlock()
					if waiting == 0 {
						break
					}
				}
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
			if c.name == "not-set-up" && len(zones) != 1 {
				t.Errorf("%s: parts %v, want Done alone", name, count)
			}
			for _, z := range zones {
				if !z.r.In(card) || !z.r.In(img.Bounds()) {
					t.Errorf("%s: part %d at %v off the page %v", name, z.kind, z.r, card)
				}
			}
			r.zmu.Lock()
			pages := r.libPages
			r.zmu.Unlock()
			if c.pages && pages < 2 {
				t.Errorf("%s: %d pages, want the list paged", name, pages)
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

// A tap on a tab shows it; on an album, its page; Back goes back to the tab where it was. Play and
// Shuffle play the album on the library's player, replacing what was queued, with the shuffle set
// first; a track plays at a tap. Done puts the library away.
func TestTheLibrarysTaps(t *testing.T) {
	noLibrary(t)
	var mu sync.Mutex
	var asked []dashboard.Action
	prev, prevAfter := sheetTap, libAfter
	sheetTap = func(a dashboard.Action) { mu.Lock(); asked = append(asked, a); mu.Unlock() }
	libAfter = func(_ time.Duration, f func()) *time.Timer { f(); return nil }
	t.Cleanup(func() { sheetTap, libAfter = prev, prevAfter })

	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	sheet := libSheetWith(libAlbums, libEntries("album", 40), "media_player.ma_kitchen")
	sheet.roots[libAlbums].page = 1
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
	// The album's tracks, as they would come.
	d.editLibrary(func(n *librarySheet) {
		p := n.current()
		p.loaded, p.loading = true, false
		p.sections = []libSection{{word: -1, kind: "track", entries: libEntries("track", 5)}}
	})
	zones = draw()
	tap(libFind(t, zones, libPartPlay, -1))
	tap(libFind(t, zones, libPartShuffle, -1))
	tap(libFind(t, zones, libPartItem, 2))
	album := d.libraryViewNow().sheet.current().entry.URI
	want := []dashboard.Action{
		{Entity: "media_player.ma_kitchen", Service: "media_player.shuffle_set", Data: map[string]any{"shuffle": false}},
		{Entity: "media_player.ma_kitchen", Service: "music_assistant.play_media", Data: map[string]any{"media_id": album, "media_type": "album", "enqueue": "replace"}},
		{Entity: "media_player.ma_kitchen", Service: "media_player.shuffle_set", Data: map[string]any{"shuffle": true}},
		{Entity: "media_player.ma_kitchen", Service: "music_assistant.play_media", Data: map[string]any{"media_id": album, "media_type": "album", "enqueue": "replace"}},
		{Entity: "media_player.ma_kitchen", Service: "music_assistant.play_media", Data: map[string]any{"media_id": "library://track/2", "media_type": "track", "enqueue": "replace"}},
	}
	mu.Lock()
	got := fmt.Sprint(asked)
	mu.Unlock()
	if got != fmt.Sprint(want) {
		t.Errorf("asked\n%s\nwant\n%s", got, fmt.Sprint(want))
	}

	tap(libFind(t, zones, libPartBack, -1))
	if l := d.libraryViewNow().sheet; len(l.stack) != 0 || l.tab != libAlbums || l.current().page != 1 {
		t.Errorf("Back left the library on %d pages up, tab %d, page %d", len(l.stack), l.tab, l.current().page)
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
	d.libraryKey("") // nothing
	d.editLibrary(func(n *librarySheet) { n.typing = true })
	d.libraryKey("cancel")
	if d.libraryViewNow().sheet.typing {
		t.Error("Cancel left the keyboard up")
	}
}

// A swipe along a tab's items turns its pages, and not past either end; as its last pages come up,
// the next of the library is asked for, from where it ends, and put after it.
func TestTheLibraryPagesAndLoadsMore(t *testing.T) {
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
		if body.Command != "music/artists/library_items" {
			t.Errorf("asked %s", body.Command)
		}
		off, limit := body.Args["offset"].(float64), body.Args["limit"].(float64)
		mu.Lock()
		offsets = append(offsets, off)
		mu.Unlock()
		n := int(min(limit, max(150-off, 0))) // 150 artists in all
		out := libEntries("artist", n)
		for i := range out {
			out[i].Name = fmt.Sprintf("Artist %03d", int(off)+i)
		}
		json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()
	pc, pf := libClient, libFindPlayer
	libClient = func() (musicassistant.Client, bool) { return musicassistant.Client{URL: srv.URL, Token: "tok"}, true }
	libFindPlayer = func(string) string { return "" }
	t.Cleanup(func() { libClient, libFindPlayer = pc, pf })

	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	d.openLibrary("media_player.ma_kitchen", "", false)
	waitFor := func(what string, ok func(librarySheet) bool) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
			if l := d.libraryViewNow(); l != nil && ok(l.sheet) {
				return
			}
		}
		t.Fatalf("no %s", what)
	}
	count := func(l librarySheet) int {
		if p := l.roots[libArtists]; len(p.sections) > 0 && !p.loading {
			return len(p.sections[0].entries)
		}
		return 0
	}
	waitFor("first artists", func(l librarySheet) bool { return count(l) == 48 })
	zones, _ := libDraw(r, d.libraryViewNow().sheet)
	r.zmu.Lock()
	pages, per := r.libPages, r.libPerPage
	r.zmu.Unlock()
	if per <= 0 || pages != (48+per-1)/per {
		t.Fatalf("%d pages of %d for 48", pages, per)
	}
	page := libFind(t, zones, libPartPage, -1)
	from := image.Pt(page.r.Min.X+page.r.Dx()*3/4, (page.r.Min.Y+page.r.Max.Y)/2)
	swipe := func(dx int) bool { return d.libraryPageSwipe(from, from.X+dx, from.Y+5) }
	if !swipe(-r.s(80)) || d.libraryViewNow().sheet.current().page != 1 {
		t.Fatal("a swipe to the left did not turn to the next page")
	}
	if !swipe(r.s(80)) || !swipe(r.s(80)) || d.libraryViewNow().sheet.current().page != 0 {
		t.Fatal("swipes to the right did not stop at the first page")
	}
	if swipe(r.s(10)) {
		t.Error("a short slide turned the page")
	}
	for i := 0; i < pages; i++ {
		swipe(-r.s(80))
	}
	limit := min(max(per*4, 24), 120)
	waitFor("more artists", func(l librarySheet) bool { return count(l) == 48+limit })
	mu.Lock()
	got := fmt.Sprint(offsets)
	mu.Unlock()
	if got != "[0 48]" {
		t.Errorf("asked from %s, want [0 48]", got)
	}
	l := d.libraryViewNow().sheet
	if e := l.roots[libArtists].sections[0].entries; e[48].Name != "Artist 048" || !l.roots[libArtists].more {
		t.Errorf("the next part is %q, more %v", e[48].Name, l.roots[libArtists].more)
	}
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
		for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
			if l := d.libraryViewNow().sheet; !l.finding {
				break
			}
		}
		if l := d.libraryViewNow().sheet; l.finding || l.target != want.target {
			t.Errorf("card %d plays on %q (finding %v), want %q", i, l.target, l.finding, want.target)
		}
	}
}

// The media sheet offers the library beside Done only where Music Assistant plays to its player, and
// a tap on it puts the library up in the sheet's place, to play on that player.
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
		if l == nil || l.sheet.target != "media_player.ma_kitchen" || l.sheet.finding || d.dashMedia != nil {
			t.Errorf("%s: Library opened %+v, the media sheet left %v", c.name, l, d.dashMedia != nil)
		}
	}
}

// A picture the proxy will not make at the size asked for (400) is asked for once more as it is, and
// comes back square at the size it is drawn, with its corners clear.
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
	img, err := loadLibPicture("http://ma.invalid:8095/imageproxy/abc?size=256", 100)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(asked) != "[http://ma.invalid:8095/imageproxy/abc?size=256 http://ma.invalid:8095/imageproxy/abc]" {
		t.Errorf("asked %v", asked)
	}
	if img.Rect.Dx() != 100 || img.Rect.Dy() != 100 || img.RGBAAt(0, 0).A != 0 || img.RGBAAt(50, 50).A != 255 {
		t.Errorf("a picture of %v, corner %v, middle %v", img.Rect, img.RGBAAt(0, 0), img.RGBAAt(50, 50))
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
		u := fmt.Sprintf("http://ma.invalid/imageproxy/%d", i)
		p.picture(u, 8)
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
	if _, ok := p.kept["8 http://ma.invalid/imageproxy/0"]; ok {
		t.Error("the oldest picture is still kept")
	}
}
