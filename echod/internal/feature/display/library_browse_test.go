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
