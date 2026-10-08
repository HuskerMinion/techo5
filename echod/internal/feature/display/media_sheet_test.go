//go:build !dot && !spot

package display

import (
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mdi"
)

// The media sheet over the page has the controls, the volume, the favorites and the speakers, each
// part on the sheet, and fits the panel with a full list. A tap at the volume's end is all the way
// up; one beside the sheet is off it. With SHOW_PREVIEW set, each is written there.
func TestTheMediaSheetOffersWhatThePlayerCanDo(t *testing.T) {
	for _, icon := range []string{"skip-previous", "play", "pause", "skip-next", "volume-high", "radio", "playlist-music", "speaker"} {
		if _, ok := mdi.Rune(icon); !ok {
			t.Errorf("no icon %q", icon)
		}
	}
	dir := os.Getenv("SHOW_PREVIEW")
	at := time.Date(2026, 10, 6, 20, 15, 0, 0, time.Local)
	full := dashboard.MediaLists{MusicAssistant: true, Target: "media_player.ma_kitchen", Group: "media_player.ma_kitchen", Speakers: []dashboard.Speaker{
		{Entity: "media_player.ma_bath", Name: "Bathroom"}, {Entity: "media_player.ma_deck", Name: "Deck"},
		{Entity: "media_player.echo", Name: "Echo Show 5"}}}
	for _, name := range []string{"Liked Songs", "Morning", "Dinner", "Radio One", "Radio Two", "Jazz", "Focus", "Party", "Sleep", "Road trip"} {
		full.Favorites = append(full.Favorites, dashboard.MediaChoice{Name: name, URI: "library://playlist/" + name, Kind: "playlist"})
	}
	now := dashboard.MediaNow{Entity: "media_player.sonos_kitchen", Name: "Kitchen", State: "playing", Title: "Slow Morning", Artist: "The Example Band", Volume: 0.35}
	for _, c := range []struct {
		name               string
		view               mediaView
		favorites, speaker int
	}{
		{"full", mediaView{sheet: mediaSheet{entity: now.Entity, lists: full, grouped: map[string]bool{"media_player.ma_bath": true}, volume: -1}, now: now}, 8, 3},
		{"loading", mediaView{sheet: mediaSheet{entity: now.Entity, loading: true, volume: -1}, now: now}, 0, 0},
		{"controls-only", mediaView{sheet: mediaSheet{entity: "media_player.tv", volume: -1},
			now: dashboard.MediaNow{Entity: "media_player.tv", Name: "TV", State: "off", Volume: -1}}, 0, 0},
		{"grouped-without-music-assistant", mediaView{sheet: mediaSheet{entity: "media_player.living_room", volume: -1,
			lists: dashboard.MediaLists{Group: "media_player.living_room", Speakers: []dashboard.Speaker{
				{Entity: "media_player.bedroom", Name: "Bedroom"}, {Entity: "media_player.office", Name: "Office"}}},
			grouped: map[string]bool{"media_player.bedroom": true}},
			now: dashboard.MediaNow{Entity: "media_player.living_room", Name: "Living Room", State: "paused", Title: "Morning", Volume: 0.2}}, 0, 2},
	} {
		img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
		r := newRenderer(img)
		v := c.view
		r.draw(scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls(), dashMedia: &v})
		r.zmu.Lock()
		zones, card := r.mediaZones, r.mediaCard
		r.zmu.Unlock()
		count := map[int]int{}
		for _, z := range zones {
			count[z.kind]++
			if !z.r.In(card) {
				t.Errorf("%s: part %d at %v outside the sheet %v", c.name, z.kind, z.r, card)
			}
		}
		if !card.In(img.Bounds()) {
			t.Errorf("%s: the sheet %v is bigger than the panel", c.name, card)
		}
		if count[mediaPartPlay] != 1 || count[mediaPartDone] != 1 || count[mediaPartFavorite] < c.favorites || count[mediaPartSpeaker] != c.speaker {
			t.Errorf("%s: parts %v, want play, done, %d+ favorites, %d speakers", c.name, count, c.favorites, c.speaker)
		}
		if shown := count[mediaPartVolume] == 1; shown != (v.now.Volume >= 0) {
			t.Errorf("%s: volume shown %v for a volume of %v", c.name, shown, v.now.Volume)
		}
		for _, z := range zones {
			if z.kind != mediaPartVolume {
				continue
			}
			mid := (z.r.Min.Y + z.r.Max.Y) / 2
			if hit, f, _ := mediaHit(zones, card, z.r.Max.X-1, mid); hit.kind != mediaPartVolume || f != 1 {
				t.Errorf("%s: the volume's end is %v", c.name, f)
			}
		}
		if _, _, in := mediaHit(zones, card, card.Min.X-5, card.Min.Y-5); in {
			t.Errorf("%s: a tap beside the sheet was on it", c.name)
		}
		if dir != "" {
			f, err := os.Create(filepath.Join(dir, "media-sheet-"+c.name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			png.Encode(f, img)
			f.Close()
		}
	}
}

// Back, play or pause and next name the tile's own player: the dashboard works out whether Music
// Assistant's reaches the group from there (it is the one playing) or not (the speaker plays from
// elsewhere, and Music Assistant's would start its own old queue).
func TestTheMediaSheetsControlsNameTheTilesPlayer(t *testing.T) {
	var asked []dashboard.Action
	prev := sheetTap
	sheetTap = func(a dashboard.Action) { asked = append(asked, a) }
	t.Cleanup(func() { sheetTap = prev })

	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	sheet := mediaSheet{entity: "media_player.sonos_kitchen", volume: -1,
		lists: dashboard.MediaLists{MusicAssistant: true, Target: "media_player.ma_kitchen", Group: "media_player.ma_kitchen"}}
	view := mediaView{sheet: sheet, now: dashboard.MediaNow{Entity: sheet.entity, Name: "Kitchen", State: "playing", Volume: 0.4}}
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls(), dashMedia: &view})
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	d.dashMedia = &sheet
	r.zmu.Lock()
	zones := r.mediaZones
	r.zmu.Unlock()
	for _, z := range zones {
		if z.kind == mediaPartPrev || z.kind == mediaPartPlay || z.kind == mediaPartNext {
			d.mediaTap((z.r.Min.X+z.r.Max.X)/2, (z.r.Min.Y+z.r.Max.Y)/2)
		}
	}
	if len(asked) != 3 {
		t.Fatalf("the three controls asked %d times", len(asked))
	}
	for _, a := range asked {
		if a.Entity != "media_player.sonos_kitchen" {
			t.Errorf("%s went to %s, want the tile's own player", a.Service, a.Entity)
		}
	}
}

// The sheet's volume moves the speakers grouped in with it, each by as much and kept between 0 and 1;
// a grouped speaker's button sets its own volume when a finger slides along it, and a tap on it still
// takes it out of the group.
func TestTheMediaSheetsVolumeMovesTheGroup(t *testing.T) {
	var mu sync.Mutex
	set := map[string]float64{}
	prev := sliderTap
	sliderTap = func(a dashboard.Action) {
		mu.Lock()
		defer mu.Unlock()
		set[a.Entity] = a.Data["volume_level"].(float64)
	}
	prevSettle := sliderSettle
	sliderSettle = func(time.Duration, func()) *time.Timer { return nil } // the lift sent again: not what this checks
	t.Cleanup(func() { sliderTap, sliderSettle = prev, prevSettle })

	sheet := mediaSheet{entity: "media_player.den", volume: -1,
		lists: dashboard.MediaLists{MusicAssistant: true, Target: "media_player.ma_den", Group: "media_player.ma_den",
			Speakers: []dashboard.Speaker{
				{Entity: "media_player.ma_bath", Name: "Bathroom", Volume: 0.40},
				{Entity: "media_player.ma_deck", Name: "Deck", Volume: 0.95},
				{Entity: "media_player.ma_hall", Name: "Hall", Volume: 0.30}}},
		grouped: map[string]bool{"media_player.ma_bath": true, "media_player.ma_deck": true}}
	view := mediaView{sheet: sheet, now: dashboard.MediaNow{Entity: "media_player.den", Name: "Den", State: "playing", Volume: 0.5}}
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls(), dashMedia: &view})
	var bar, bath image.Rectangle
	r.zmu.Lock()
	for _, z := range r.mediaZones {
		switch {
		case z.kind == mediaPartVolume:
			bar = z.r
		case z.kind == mediaPartSpeaker && z.index == 0:
			bath = z.r
		}
	}
	r.zmu.Unlock()

	d := &Display{r: r, poke: make(chan struct{}, 1)}
	d.dashMedia = &sheet
	d.dashMedia.volume = 0.5
	my := (bar.Min.Y + bar.Max.Y) / 2
	at := func(v float64) int { return bar.Min.X + int(v*float64(bar.Dx()-1)+0.5) }
	d.drawnHold(at(0.5), my)
	d.drawnRelease(at(0.6), my)
	mu.Lock()
	if set["media_player.den"] != 0.6 || set["media_player.ma_bath"] != 0.5 || set["media_player.ma_deck"] != 1 {
		t.Errorf("the group's volume up by 0.1 set %v, want den 0.6, bathroom 0.5 and deck at most 1", set)
	}
	if _, hall := set["media_player.ma_hall"]; hall {
		t.Error("a speaker not grouped in was set")
	}
	mu.Unlock()

	by := (bath.Min.Y + bath.Max.Y) / 2
	d.drawnHold(bath.Min.X+bath.Dx()/2, by)
	d.drawnMove(bath.Min.X+bath.Dx()/5, by)
	d.drawnRelease(bath.Min.X+bath.Dx()/5, by)
	mu.Lock()
	if v := set["media_player.ma_bath"]; v < 0.15 || v > 0.25 {
		t.Errorf("a fifth along the bathroom's button set it to %v", v)
	}
	mu.Unlock()
	if !d.dashMedia.grouped["media_player.ma_bath"] {
		t.Error("sliding along a speaker's button took it out of the group")
	}

	d.drawnHold(bath.Min.X+bath.Dx()/2, by)
	d.drawnRelease(bath.Min.X+bath.Dx()/2, by)
	if d.dashMedia.grouped["media_player.ma_bath"] {
		t.Error("a tap on a grouped speaker left it in the group")
	}

	// A quick tap on the volume, with no slide, moves the group too.
	mu.Lock()
	deckBefore := set["media_player.ma_deck"]
	mu.Unlock()
	d.mediaTap(at(0.4), my)
	mu.Lock()
	if set["media_player.den"] != 0.4 || set["media_player.ma_deck"] >= deckBefore {
		t.Errorf("a tap on the volume at 0.4 set %v; want den 0.4 and the deck lower than %v", set, deckBefore)
	}
	mu.Unlock()
}

// More favorites than fit are paged through: a page holds what fits, dots say which page it is, a
// swipe to the left turns to the next and one to the right back, never past either end, and a swipe
// plays nothing.
func TestTheMediaSheetPagesThroughFavorites(t *testing.T) {
	lists := dashboard.MediaLists{MusicAssistant: true, Target: "media_player.ma_den", Group: "media_player.ma_den",
		Speakers: []dashboard.Speaker{{Entity: "media_player.ma_bath", Name: "Bathroom", Volume: 0.4}}}
	for i := 0; i < 20; i++ {
		lists.Favorites = append(lists.Favorites, dashboard.MediaChoice{Name: fmt.Sprintf("Playlist %d", i), URI: fmt.Sprintf("library://playlist/%d", i), Kind: "playlist"})
	}
	now := dashboard.MediaNow{Entity: "media_player.den", Name: "Den", State: "idle", Volume: 0.3}
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	shown := func(page int) (first, count, pages int, chip image.Rectangle) {
		view := mediaView{sheet: mediaSheet{entity: now.Entity, lists: lists, page: page, volume: -1}, now: now}
		r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls(), dashMedia: &view})
		r.zmu.Lock()
		defer r.zmu.Unlock()
		first = -1
		for _, z := range r.mediaZones {
			if z.kind == mediaPartFavorite {
				if first < 0 {
					first, chip = z.index, z.r
				}
				count++
			}
		}
		return first, count, r.mediaPages, chip
	}
	first, perPage, pages, chip := shown(0)
	if first != 0 || perPage == 0 || pages != (20+perPage-1)/perPage || pages < 2 {
		t.Fatalf("page 0 shows %d from %d over %d pages", perPage, first, pages)
	}

	d := &Display{r: r, poke: make(chan struct{}, 1)}
	d.dashMedia = &mediaSheet{entity: now.Entity, lists: lists, volume: -1}
	swipe := func(dx int) {
		y := (chip.Min.Y + chip.Max.Y) / 2
		x := (chip.Min.X+chip.Max.X)/2 + max(-dx, 0)
		d.drawnHold(x, y)
		d.drawnMove(x+dx, y)
		d.drawnRelease(x+dx, y)
	}
	swipe(-150)
	if d.dashMedia.page != 1 || !d.dashMedia.litUntil.IsZero() {
		t.Fatalf("a swipe to the left: page %d, lit until %v; want page 1 and nothing played", d.dashMedia.page, d.dashMedia.litUntil)
	}
	if first, _, _, _ := shown(1); first != perPage {
		t.Errorf("page 1 starts at favorite %d, want %d", first, perPage)
	}
	for i := 0; i < pages+2; i++ {
		swipe(-150)
	}
	if d.dashMedia.page != pages-1 {
		t.Errorf("swiped past the last page: on page %d, want %d", d.dashMedia.page, pages-1)
	}
	for i := 0; i < pages+2; i++ {
		swipe(150)
	}
	if d.dashMedia.page != 0 {
		t.Errorf("swiped back past the first page: on page %d, want 0", d.dashMedia.page)
	}
}

// With a problem fetching the lists and the favorites of before still to show, the sheet is sized for
// what it draws: every part on it, and Done clear of the speakers.
func TestAStaleListKeepsTheSheetsShape(t *testing.T) {
	lists := dashboard.MediaLists{MusicAssistant: true, Target: "media_player.ma_den", Group: "media_player.ma_den"}
	for i := 0; i < 6; i++ {
		lists.Favorites = append(lists.Favorites, dashboard.MediaChoice{Name: fmt.Sprintf("Playlist %d", i), URI: fmt.Sprintf("library://playlist/%d", i), Kind: "playlist"})
		lists.Speakers = append(lists.Speakers, dashboard.Speaker{Entity: fmt.Sprintf("media_player.s%d", i), Name: fmt.Sprintf("Room %d", i), Volume: 0.3})
	}
	view := mediaView{sheet: mediaSheet{entity: "media_player.den", lists: lists, problem: "hass: GET /api/states: 502 Bad Gateway", volume: -1},
		now: dashboard.MediaNow{Entity: "media_player.den", Name: "Den", State: "idle", Volume: 0.3}}
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls(), dashMedia: &view})
	r.zmu.Lock()
	zones, card := r.mediaZones, r.mediaCard
	r.zmu.Unlock()
	var done image.Rectangle
	for _, z := range zones {
		if !z.r.In(card) {
			t.Errorf("part %d at %v is off the sheet %v", z.kind, z.r, card)
		}
		if z.kind == mediaPartDone {
			done = z.r
		}
	}
	for _, z := range zones {
		if (z.kind == mediaPartSpeaker || z.kind == mediaPartFavorite) && z.r.Overlaps(done) {
			t.Errorf("Done %v is on top of %v", done, z.r)
		}
	}
}

// A drag that came down on a favorite and lifted on a speaker taps neither; more speakers than fit
// are paged through as the favorites are.
func TestADragBetweenPartsTapsNothingAndSpeakersPage(t *testing.T) {
	var asked []dashboard.Action
	prev := sheetTap
	sheetTap = func(a dashboard.Action) { asked = append(asked, a) }
	t.Cleanup(func() { sheetTap = prev })

	lists := dashboard.MediaLists{MusicAssistant: true, Target: "media_player.ma_den", Group: "media_player.ma_den"}
	for i := 0; i < 8; i++ {
		lists.Favorites = append(lists.Favorites, dashboard.MediaChoice{Name: fmt.Sprintf("Playlist %d", i), URI: fmt.Sprintf("library://playlist/%d", i), Kind: "playlist"})
	}
	for i := 0; i < 12; i++ {
		lists.Speakers = append(lists.Speakers, dashboard.Speaker{Entity: fmt.Sprintf("media_player.s%d", i), Name: fmt.Sprintf("Room %d", i), Volume: 0.3})
	}
	sheet := mediaSheet{entity: "media_player.den", lists: lists, volume: -1, grouped: map[string]bool{}}
	view := mediaView{sheet: sheet, now: dashboard.MediaNow{Entity: "media_player.den", Name: "Den", State: "idle", Volume: 0.3}}
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls(), dashMedia: &view})
	r.zmu.Lock()
	zones, speakerPages := r.mediaZones, r.mediaSpeakerPages
	r.zmu.Unlock()
	if speakerPages < 2 {
		t.Fatalf("12 speakers on %d page(s)", speakerPages)
	}
	var fav0, spk image.Rectangle
	for _, z := range zones {
		switch {
		case z.kind == mediaPartFavorite && z.index == 0:
			fav0 = z.r
		case z.kind == mediaPartSpeaker && spk.Empty():
			spk = z.r
		}
	}
	if fav0.Empty() || spk.Empty() {
		t.Fatalf("favorite %v, speaker %v", fav0, spk)
	}
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	d.dashMedia = &sheet
	cx := (fav0.Min.X + fav0.Max.X) / 2
	d.drawnHold(cx, (fav0.Min.Y+fav0.Max.Y)/2)
	d.drawnMove(cx, (spk.Min.Y+spk.Max.Y)/2)
	d.drawnRelease(cx, (spk.Min.Y+spk.Max.Y)/2)
	if len(asked) != 0 || len(d.dashMedia.grouped) != 0 {
		t.Errorf("a drag from a favorite to a speaker asked for %v, grouped %v", asked, d.dashMedia.grouped)
	}

	sy := (spk.Min.Y + spk.Max.Y) / 2
	d.drawnHold(spk.Max.X-5, sy)
	d.drawnMove(spk.Max.X-155, sy)
	d.drawnRelease(spk.Max.X-155, sy)
	if d.dashMedia.speakerPage != 1 || len(asked) != 0 {
		t.Errorf("a swipe along the speakers: page %d, asked %v; want page 1 and nothing asked", d.dashMedia.speakerPage, asked)
	}
}

// A token that may not read Music Assistant's library is told so in words.
func TestAnUnauthorizedListIsSaidInWords(t *testing.T) {
	if got := problemText(errors.New("hass: GET /api/config/config_entries/entry: 401 Unauthorized")); strings.Contains(got, "401") {
		t.Errorf("said %q", got)
	}
	if got := problemText(errors.New("hass: GET /api/states: 502 Bad Gateway")); !strings.Contains(got, "502") {
		t.Errorf("another problem said %q, want it as it is", got)
	}
}

// A favorite put on from the sheet is the one marked while Music Assistant has music there, even when
// a pause has stopped it; Music Assistant says which track plays, not which playlist it came from.
func TestTheFavoritePutOnIsMarked(t *testing.T) {
	prev := sheetTap
	sheetTap = func(dashboard.Action) {}
	t.Cleanup(func() { sheetTap = prev; putOn.Delete("media_player.ma_kitchen") })

	lists := dashboard.MediaLists{MusicAssistant: true, Target: "media_player.ma_kitchen", Group: "media_player.ma_kitchen",
		Elsewhere: true, Favorites: []dashboard.MediaChoice{
			{Name: "Morning", URI: "library://playlist/1", Kind: "playlist"},
			{Name: "Evening", URI: "library://playlist/2", Kind: "playlist"}}}
	sheet := mediaSheet{entity: "media_player.sonos_kitchen", volume: -1, lists: lists}
	view := mediaView{sheet: sheet, now: dashboard.MediaNow{Entity: sheet.entity, Name: "Kitchen", State: "idle", Volume: 0.4}}
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls(), dashMedia: &view})
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	d.dashMedia = &sheet
	r.zmu.Lock()
	zones := r.mediaZones
	r.zmu.Unlock()
	for _, z := range zones {
		if z.kind == mediaPartFavorite && z.index == 1 {
			d.mediaTap((z.r.Min.X+z.r.Max.X)/2, (z.r.Min.Y+z.r.Max.Y)/2)
		}
	}
	if on, _ := putOn.Load("media_player.ma_kitchen"); on != "library://playlist/2" {
		t.Errorf("put on %v, want Evening's URI", on)
	}
	if !d.dashMedia.lists.Queued || d.dashMedia.lists.Elsewhere {
		t.Errorf("after putting music on: queued %v, elsewhere %v; want Music Assistant's music there", d.dashMedia.lists.Queued, d.dashMedia.lists.Elsewhere)
	}
}
