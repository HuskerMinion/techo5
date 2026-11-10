package musicassistant

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// Favorites ask a kind's library for its favorites; recently played asks for the latest; a queue is
// read from what is playing now on, each item its media item named and timed as the queue has it.
func TestFavoritesRecentAndQueueCalls(t *testing.T) {
	type call struct {
		Command string         `json:"command"`
		Args    map[string]any `json:"args"`
	}
	var got []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var c call
		_ = json.Unmarshal(body, &c)
		got = append(got, c)
		switch c.Command {
		case "music/tracks/library_items":
			io.WriteString(w, `[{"item_id":"1","provider":"library","name":"Fav","uri":"library://track/1","media_type":"track"}]`)
		case "music/recently_played_items":
			io.WriteString(w, `[{"item_id":"9","provider":"library","name":"Lately","uri":"library://album/9","media_type":"album"}]`)
		case "player_queues/get":
			io.WriteString(w, `{"queue_id":"q1","current_index":3}`)
		case "player_queues/items":
			io.WriteString(w, `[{"queue_item_id":"a","name":"Band - Song","duration":245,
				"media_item":{"item_id":"5","provider":"library","name":"Song","uri":"library://track/5","media_type":"track"}},
				{"queue_item_id":"b","name":"Loose item","duration":60,"media_item":null}]`)
		default:
			t.Errorf("unexpected command %q", c.Command)
		}
	}))
	defer srv.Close()
	c := Client{URL: srv.URL}
	ctx := context.Background()

	fav, err := c.Favorites(ctx, KindTracks, 20)
	if err != nil || len(fav) != 1 || fav[0].Name != "Fav" {
		t.Fatalf("favorites = %+v, %v", fav, err)
	}
	recent, err := c.RecentlyPlayed(ctx, 30)
	if err != nil || len(recent) != 1 || recent[0].MediaType != "album" {
		t.Fatalf("recent = %+v, %v", recent, err)
	}
	q, err := c.Queue(ctx, "q1", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(q) != 2 || q[0].Name != "Song" || q[0].Duration != 245 || q[1].Name != "Loose item" {
		t.Fatalf("queue = %+v", q)
	}

	want := []call{
		{"music/tracks/library_items", map[string]any{"favorite": true, "limit": 20.0, "offset": 0.0, "order_by": "sort_name"}},
		{"music/recently_played_items", map[string]any{"limit": 30.0}},
		{"player_queues/get", map[string]any{"queue_id": "q1"}},
		{"player_queues/items", map[string]any{"queue_id": "q1", "limit": 50.0, "offset": 3.0}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %+v\nwant  %+v", got, want)
	}
}
