//go:build !dot && !spot

package display

import (
	"context"
	"encoding/json"
	"image"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/musicassistant"
)

// libBooks are made-up audiobooks for the listening tests and previews: the first part way through, the
// second finished, the rest not started, each an hour long, with a series of the first three.
func libBooks(n int) []musicassistant.Entry {
	books := libEntries("audiobook", n)
	for i := range books {
		books[i].Duration, books[i].Authors = 3600+i*600, []string{"Ann Author"}
	}
	if n > 1 {
		books[0].ResumeMS = 1_500_000
		books[1].FullyPlayed = true
	}
	return books
}

// libSeriesOf is a series entry of parts, as AudiobooksBySeries gives one.
func libSeriesOf(name string, parts []musicassistant.Entry) musicassistant.Entry {
	return musicassistant.SeriesFilled(musicassistant.Entry{ItemID: "c-" + name, Provider: "library", MediaType: "collection",
		URI: "library://collection/" + name, Name: name, Items: parts})
}

// listenFakeMA is a Music Assistant that answers each command with answers' JSON for it, and keeps the
// arguments of each command asked.
func listenFakeMA(t *testing.T, answer func(command string, args map[string]any) (int, string)) (musicassistant.Client, func() map[string][]map[string]any) {
	t.Helper()
	var mu sync.Mutex
	asked := map[string][]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var c struct {
			Command string         `json:"command"`
			Args    map[string]any `json:"args"`
		}
		_ = json.Unmarshal(body, &c)
		mu.Lock()
		asked[c.Command] = append(asked[c.Command], c.Args)
		mu.Unlock()
		status, out := answer(c.Command, c.Args)
		w.WriteHeader(status)
		io.WriteString(w, out)
	}))
	t.Cleanup(srv.Close)
	return musicassistant.Client{URL: srv.URL, Token: "tok"}, func() map[string][]map[string]any {
		mu.Lock()
		defer mu.Unlock()
		out := make(map[string][]map[string]any, len(asked))
		for k, v := range asked {
			out[k] = slices.Clone(v)
		}
		return out
	}
}

// The audiobooks tab asks for the books folded by series and for what is in progress: a Continue
// listening row of the audiobooks part way through (an episode there is the podcasts tab's), each with
// its length and place from the grid, over the grid of series and books.
func TestTheAudiobooksTabFoldsSeriesAndContinues(t *testing.T) {
	c, asked := listenFakeMA(t, func(command string, _ map[string]any) (int, string) {
		switch command {
		case "music/audiobooks/library_items":
			return 200, `[{"item_id":"c1","provider":"library","name":"Die drei ???","media_type":"collection",
				"items":[{"item_id":"1","provider":"library","name":"Folge 1","media_type":"audiobook","uri":"library://audiobook/1","duration":3000,"resume_position_ms":600000},
				         {"item_id":"2","provider":"library","name":"Folge 2","media_type":"audiobook","uri":"library://audiobook/2"}]},
				{"item_id":"7","provider":"library","name":"Alone","media_type":"audiobook","uri":"library://audiobook/7"}]`
		case "music/in_progress_items":
			return 200, `[{"item_id":"e1","provider":"rss","name":"Ep","media_type":"podcast_episode","uri":"rss://podcast_episode/e1"},
				{"item_id":"1","provider":"library","name":"Folge 1","media_type":"audiobook","uri":"library://audiobook/1"}]`
		}
		return 500, ""
	})
	secs, _, err := fetchLibPage(context.Background(), c, newLibPage(libAudiobooks, musicassistant.Entry{}), 48)
	if err != nil {
		t.Fatal(err)
	}
	if len(secs) != 2 || !secs[0].resume || secs[0].word != libWordContinue || secs[0].show != libShowRow {
		t.Fatalf("sections = %+v; want a Continue listening row over the grid", secs)
	}
	if row := secs[0].entries; len(row) != 1 || row[0].URI != "library://audiobook/1" || row[0].Duration != 3000 || row[0].ResumeMS != 600000 {
		t.Errorf("continue row = %+v; want the book part way, its length and place from its series", row)
	}
	grid := secs[1].entries
	series := 0
	for _, e := range grid {
		if e.MediaType == "collection" && len(e.Items) == 2 && e.Items[0].Name == "Folge 1" {
			series++
		}
	}
	if len(grid) != 2 || series != 1 {
		t.Errorf("grid = %+v; want the series, its parts in order, and the book", grid)
	}
	if a := asked()["music/audiobooks/library_items"]; len(a) != 1 || a[0]["collapse_collections"] != true {
		t.Errorf("listing asked %v; want the series folded", a)
	}
	if a := asked()["music/in_progress_items"]; len(a) != 1 || a[0]["all_users"] != false {
		t.Errorf("in progress asked %v; want this user's only", a)
	}
}

// A server that cannot fold the books by series has them listed one by one; one that will not say what
// is in progress has the grid alone.
func TestTheAudiobooksTabWithoutSeriesOrProgress(t *testing.T) {
	c, _ := listenFakeMA(t, func(command string, args map[string]any) (int, string) {
		if command == "music/audiobooks/library_items" && args["collapse_collections"] == nil {
			return 200, `[{"item_id":"7","provider":"library","name":"Alone","media_type":"audiobook","uri":"library://audiobook/7"}]`
		}
		return 500, ""
	})
	secs, _, err := fetchLibPage(context.Background(), c, newLibPage(libAudiobooks, musicassistant.Entry{}), 48)
	if err != nil {
		t.Fatal(err)
	}
	if len(secs) != 1 || len(secs[0].entries) != 1 || secs[0].entries[0].Name != "Alone" {
		t.Errorf("sections = %+v; want the grid of the one book", secs)
	}
}

// The podcasts tab has its episodes in progress over its grid; the next of it goes on from the grid's
// end and is added to the grid, the row left as it was.
func TestThePodcastsTabContinuesAndPages(t *testing.T) {
	noLibrary(t)
	c, asked := listenFakeMA(t, func(command string, args map[string]any) (int, string) {
		switch command {
		case "music/podcasts/library_items":
			return 200, `[{"item_id":"p","provider":"library","name":"Pod","media_type":"podcast","uri":"library://podcast/p"}]`
		case "music/in_progress_items":
			return 200, `[{"item_id":"e1","provider":"rss","name":"Ep","media_type":"podcast_episode","uri":"rss://podcast_episode/e1"},
				{"item_id":"1","provider":"library","name":"Book","media_type":"audiobook","uri":"library://audiobook/1"}]`
		}
		return 500, ""
	})
	libClient = func() (musicassistant.Client, bool) { return c, true }
	d := &Display{poke: make(chan struct{}, 1)}
	sheet := libSheetWith(libPodcasts, nil, "media_player.ma_kitchen")
	sheet.roots[libPodcasts].loaded = false
	setLibUp(d, &sheet)
	id := sheet.roots[libPodcasts].id
	d.libLoad(id)
	waitUntil(t, "the podcasts", func() bool { p := d.libraryViewNow().sheet.roots[libPodcasts]; return p.loaded && !p.loading })
	p := d.libraryViewNow().sheet.roots[libPodcasts]
	if len(p.sections) != 2 || p.sections[0].entries[0].MediaType != "podcast_episode" || len(p.sections[0].entries) != 1 {
		t.Fatalf("sections = %+v; want the episode in progress over the grid", p.sections)
	}
	d.editLibrary(func(n *librarySheet) { n.roots[libPodcasts].more = true })
	d.libLoad(id)
	waitUntil(t, "the next of the podcasts", func() bool {
		p := d.libraryViewNow().sheet.roots[libPodcasts]
		return !p.loading && len(p.sections[1].entries) == 2
	})
	if a := asked()["music/podcasts/library_items"]; len(a) != 2 || a[1]["offset"] != 1.0 {
		t.Errorf("listing asked %v; want the second from the grid's end", a)
	}
	if a := asked()["music/in_progress_items"]; len(a) != 1 {
		t.Errorf("in progress asked %d times; want once, with the first of the tab", len(a))
	}
	if p := d.libraryViewNow().sheet.roots[libPodcasts]; len(p.sections[0].entries) != 1 {
		t.Errorf("the row became %+v", p.sections[0].entries)
	}
}

// The episodes of the Continue listening row are read in full, for their length and place: the list
// of what is in progress names them only.
func TestContinueListeningReadsEpisodesInFull(t *testing.T) {
	c, asked := listenFakeMA(t, func(command string, args map[string]any) (int, string) {
		switch command {
		case "music/podcasts/library_items":
			return 200, `[]`
		case "music/in_progress_items":
			return 200, `[{"item_id":"e1","provider":"rss","name":"Ep","media_type":"podcast_episode","uri":"rss://podcast_episode/e1"},
				{"item_id":"e2","provider":"rss","name":"Ep 2","media_type":"podcast_episode","uri":"rss://podcast_episode/e2"}]`
		case "music/podcasts/podcast_episode":
			if args["item_id"] == "e2" {
				return 500, ""
			}
			return 200, `{"item_id":"e1","provider":"rss","name":"Ep","media_type":"podcast_episode","uri":"rss://podcast_episode/e1",
				"duration":1800,"resume_position_ms":900000}`
		}
		return 500, ""
	})
	secs, _, err := fetchLibPage(context.Background(), c, newLibPage(libPodcasts, musicassistant.Entry{}), 48)
	if err != nil {
		t.Fatal(err)
	}
	if len(secs) != 2 || len(secs[0].entries) != 2 {
		t.Fatalf("sections = %+v; want both episodes in the row", secs)
	}
	if e := secs[0].entries[0]; e.Duration != 1800 || e.ResumeMS != 900000 {
		t.Errorf("episode = %+v; want its length and place", e)
	}
	if e := secs[0].entries[1]; e.Name != "Ep 2" || e.Duration != 0 {
		t.Errorf("an episode that could not be read became %+v", e)
	}
	if n := len(asked()["music/podcasts/podcast_episode"]); n != 2 {
		t.Errorf("episodes read %d times; want each once", n)
	}
}

// A series' tile opens its parts, in order, with nothing to ask for; a book in the Continue listening
// row plays at a tap rather than opening its page.
func TestASeriesOpensAndContinueListeningPlays(t *testing.T) {
	noLibrary(t)
	asked := recordTaps(t)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, show8Wide, show8High))) // tall enough for the grid under the row
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	books := libBooks(5)
	sheet := libSheetWith(libAudiobooks, nil, "media_player.ma_kitchen")
	sheet.roots[libAudiobooks].sections = []libSection{
		{word: libWordContinue, show: libShowRow, kind: "audiobook", entries: books[:1], resume: true},
		{word: -1, show: libShowGrid, kind: "audiobook", entries: []musicassistant.Entry{libSeriesOf("Saga", books[:3]), books[3]}}}
	setLibUp(d, &sheet)
	zones, _ := libDraw(r, sheet)
	d.libraryTap(libMiddle(libFind(t, zones, libPartItem, 1*libSecStride+0)))
	l := d.libraryViewNow().sheet
	if len(l.stack) != 1 || l.stack[0].what != libSeries || !l.stack[0].loaded || len(l.stack[0].sections[0].entries) != 3 || l.stack[0].sections[0].entries[2].URI != books[2].URI {
		t.Fatalf("a tap on the series opened %+v", l.stack)
	}
	zones, _ = libDraw(r, l)
	d.libraryTap(libMiddle(libFind(t, zones, libPartBack, -1)))
	zones, _ = libDraw(r, d.libraryViewNow().sheet)
	d.libraryTap(libMiddle(libFind(t, zones, libPartItem, 0)))
	waitUntil(t, "the book's play", func() bool { return len(asked()) == 1 })
	if a := asked()[0]; a.Service != "music_assistant.play_media" || a.Data["media_id"] != books[0].URI || a.Data["media_type"] != "audiobook" {
		t.Errorf("asked %+v; want the book played", a)
	}
	if l := d.libraryViewNow().sheet; len(l.stack) != 0 {
		t.Errorf("the tap opened a page as well: %+v", l.stack)
	}
}
