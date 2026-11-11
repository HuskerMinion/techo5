//go:build !dot && !spot

package display

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/musicassistant"
)

// The favorites page has a section for each kind the server has favorites of, the tracks as tracks;
// the recent page one list of what was played; the queue page what its queue plays from now on, and
// nothing at all with no queue to read.
func TestFavoritesRecentAndQueuePages(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var c struct {
			Command string         `json:"command"`
			Args    map[string]any `json:"args"`
		}
		_ = json.Unmarshal(body, &c)
		asked = append(asked, c.Command)
		switch {
		case strings.HasSuffix(c.Command, "/library_items"):
			kind := strings.TrimSuffix(strings.TrimPrefix(c.Command, "music/"), "/library_items")
			io.WriteString(w, `[{"item_id":"1","provider":"library","name":"Fav `+kind+`","uri":"library://x/1"}]`)
		case c.Command == "music/recently_played_items":
			io.WriteString(w, `[{"item_id":"9","provider":"library","name":"Lately","uri":"library://album/9","media_type":"album"}]`)
		case c.Command == "player_queues/get":
			io.WriteString(w, `{"current_index":0}`)
		case c.Command == "player_queues/items":
			io.WriteString(w, `[{"name":"Now","duration":200,"media_item":{"name":"Now","uri":"library://track/1","media_type":"track"}}]`)
		}
	}))
	defer srv.Close()
	c := musicassistant.Client{URL: srv.URL}
	ctx := context.Background()

	fav, _, err := fetchLibPage(ctx, c, newLibPage(libFavorites, musicassistant.Entry{}), 48)
	if err != nil {
		t.Fatal(err)
	}
	if len(fav) != 7 || fav[0].kind != "track" || fav[0].show != libShowTracks || fav[1].show != libShowRow {
		t.Fatalf("favorites sections = %+v; want seven, the tracks first as tracks", fav)
	}

	recent, _, err := fetchLibPage(ctx, c, newLibPage(libRecent, musicassistant.Entry{}), 48)
	if err != nil || len(recent) != 1 || len(recent[0].entries) != 1 || recent[0].entries[0].Name != "Lately" {
		t.Fatalf("recent = %+v, %v", recent, err)
	}

	q := newLibPage(libQueue, musicassistant.Entry{})
	none, _, err := fetchLibPage(ctx, c, q, 48)
	if err != nil || len(none) != 0 {
		t.Fatalf("a queue page with no queue = %+v, %v; want nothing", none, err)
	}
	q.queue = "q1"
	queued, _, err := fetchLibPage(ctx, c, q, 48)
	if err != nil || len(queued) != 1 || queued[0].entries[0].Name != "Now" {
		t.Fatalf("queue = %+v, %v", queued, err)
	}
}

// Each second-row icon tab has an icon of its own.
func TestSecondRowIconTabsHaveIcons(t *testing.T) {
	seen := map[string]bool{}
	for i := libFavorites; i < libTabs; i++ {
		ic := libTabIcon(i)
		if ic == "" || ic == "music-note" || seen[ic] {
			t.Errorf("tab %d icon = %q; want its own", i, ic)
		}
		seen[ic] = true
	}
}

// The browse tab reads the top of the tree; a folder page reads its own path.
func TestBrowsePagesReadTheirPath(t *testing.T) {
	var paths []any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var c struct {
			Args map[string]any `json:"args"`
		}
		_ = json.Unmarshal(body, &c)
		paths = append(paths, c.Args["path"])
		io.WriteString(w, `[{"name":"..","path":"root","media_type":"folder"},{"name":"Rock","path":"fs://Rock","media_type":"folder"}]`)
	}))
	defer srv.Close()
	c := musicassistant.Client{URL: srv.URL}
	top, _, err := fetchLibPage(context.Background(), c, newLibPage(libBrowse, musicassistant.Entry{}), 48)
	if err != nil || len(top) != 1 || len(top[0].entries) != 1 || top[0].entries[0].Path != "fs://Rock" {
		t.Fatalf("browse = %+v, %v", top, err)
	}
	folder := newLibPage(libFolder, top[0].entries[0])
	folder.path = top[0].entries[0].Path
	if _, _, err := fetchLibPage(context.Background(), c, folder, 48); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != nil || paths[1] != "fs://Rock" {
		t.Errorf("paths asked = %v; want the top, then fs://Rock", paths)
	}
}

// Start radio plays the item now, replacing the queue, with Music Assistant's radio mode on; the other
// choices never turn it on.
func TestStartRadioPlaysWithRadioMode(t *testing.T) {
	taps := recordTaps(t)
	d := &Display{poke: make(chan struct{}, 1)}
	track := musicassistant.Entry{Name: "Song", URI: "library://track/1", MediaType: "track"}
	d.libPlay("media_player.ma_kitchen", track, "track", false, libMenuEnqueue[libMenuRadio])
	d.libPlay("media_player.ma_kitchen", track, "track", false, libMenuEnqueue[libMenuNext])
	got := taps()
	if len(got) != 2 {
		t.Fatalf("asked %d times, want 2: %+v", len(got), got)
	}
	if got[0].Data["radio_mode"] != true || got[0].Data["enqueue"] != libEnqueueReplace {
		t.Errorf("start radio sent %v; want radio_mode on and the queue replaced", got[0].Data)
	}
	if _, on := got[1].Data["radio_mode"]; on || got[1].Data["enqueue"] != libEnqueueNext {
		t.Errorf("play next sent %v; want no radio mode, enqueue next", got[1].Data)
	}
}
