package musicassistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The queue's commands and the favorites' go as Music Assistant takes them; a queue's entries carry
// their queue item ids and whether each is a favorite; a refusal is told as one.
func TestQueueEditsAndFavorites(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Command string          `json:"command"`
			Args    json.RawMessage `json:"args"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		got = append(got, body.Command+" "+string(body.Args))
		switch body.Command {
		case "player_queues/play_index":
			w.WriteHeader(http.StatusForbidden)
		case "player_queues/get":
			w.Write([]byte(`{"current_index":2}`))
		case "player_queues/items":
			w.Write([]byte(`[{"queue_item_id":"qa","name":"A","duration":61,"media_item":{"uri":"library://track/7","name":"A","favorite":true}},
				{"queue_item_id":"qb","name":"B","media_item":{"uri":"x://track/b","name":"B","favorite":"yes"}}]`))
		default:
			w.Write([]byte(`null`))
		}
	}))
	defer srv.Close()
	c := Client{URL: srv.URL, Token: "tok"}
	ctx := context.Background()

	q, err := c.Queue(ctx, "up1", 10)
	if err != nil || len(q) != 2 || q[0].QueueItemID != "qa" || !q[0].Favorite || q[1].QueueItemID != "qb" || q[1].Favorite {
		t.Fatalf("queue = %+v, %v", q, err)
	}
	if err := c.PlayQueueItem(ctx, "up1", "qa"); !IsForbidden(err) {
		t.Errorf("a refused play_index gave %v", err)
	}
	if err := c.MoveNext(ctx, "up1", "qb"); err != nil || IsForbidden(nil) {
		t.Errorf("move: %v", err)
	}
	if err := c.RemoveQueueItem(ctx, "up1", "qb"); err != nil {
		t.Errorf("remove: %v", err)
	}
	if err := c.SetFavorite(ctx, q[1], "track", true); err != nil {
		t.Errorf("add favorite: %v", err)
	}
	if err := c.SetFavorite(ctx, q[0], "track", false); err != nil {
		t.Errorf("remove favorite: %v", err)
	}
	if err := c.SetFavorite(ctx, q[1], "track", false); err == nil {
		t.Error("an item not in the library was unmarked")
	}
	want := []string{
		`player_queues/get {"queue_id":"up1"}`,
		`player_queues/items {"limit":10,"offset":2,"queue_id":"up1"}`,
		`player_queues/play_index {"index":"qa","queue_id":"up1"}`,
		`player_queues/move_item {"pos_shift":0,"queue_id":"up1","queue_item_id":"qb"}`,
		`player_queues/delete_item {"item_id_or_index":"qb","queue_id":"up1"}`,
		`music/favorites/add_item {"item":"x://track/b"}`,
		`music/favorites/remove_item {"library_item_id":"7","media_type":"track"}`,
	}
	if len(got) != len(want) {
		t.Fatalf("sent %d commands, want %d:\n%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("command %d: %s, want %s", i, got[i], want[i])
		}
	}
}

// An item's library id comes from its library URI, or its own id when the library is its provider.
func TestLibraryID(t *testing.T) {
	for _, c := range []struct {
		e    Entry
		want string
		ok   bool
	}{
		{Entry{URI: "library://album/12"}, "12", true},
		{Entry{URI: "x://album/12", Provider: "library", ItemID: "12"}, "12", true},
		{Entry{URI: "ytmusic--a://track/abc", Provider: "ytmusic--a", ItemID: "abc"}, "", false},
		{Entry{URI: "library://album/"}, "", false},
	} {
		if id, ok := LibraryID(c.e); id != c.want || ok != c.ok {
			t.Errorf("%+v: %q %v, want %q %v", c.e, id, ok, c.want, c.ok)
		}
	}
}
