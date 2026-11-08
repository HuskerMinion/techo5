package musicassistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The services behind a server do not agree on types, and the server passes on what they say: a year
// as a number or as text, a track's album as an object, a bare name or null, artists as objects or
// names, a length as text. A search mixing them is read whole, each field as far as it can be, and an
// item that is not an item at all is left empty rather than failing the rest.
func TestEntriesAreReadLeniently(t *testing.T) {
	answer := `{
	  "albums": [
	    {"item_id": "1", "provider": "library", "uri": "library://album/1", "name": "Numbered", "year": 1999},
	    {"item_id": 2, "provider": "spotify--a", "uri": "spotify://album/2", "name": "Texted", "year": "2001",
	     "artists": ["Moss", {"name": "Alder", "item_id": "a7", "provider": "spotify--a", "uri": "spotify://artist/a7"}]},
	    {"uri": "library://album/3", "name": "Dated", "year": "2004-05-06", "metadata": null},
	    {"uri": "library://album/4", "name": "Blank", "year": "", "artists": null},
	    {"uri": "library://album/5", "name": "Null", "year": null, "version": 2},
	    "not an item"
	  ],
	  "tracks": [
	    {"uri": "x://t/1", "name": "Object album", "duration": 215, "track_number": "3", "disc_number": 1,
	     "album": {"name": "Parent", "item_id": "p1", "provider": "x", "uri": "x://album/p1",
	               "image": {"type": "thumb", "path": "/p.jpg", "proxy_id": "pp", "remotely_accessible": false}}},
	    {"uri": "x://t/2", "name": "Bare album", "duration": "187.0", "album": "Just a name"},
	    {"uri": "x://t/3", "name": "No album", "duration": null, "album": null, "image": "not an image",
	     "metadata": {"images": [{"type": "thumb", "path": "https://i/x.jpg", "remotely_accessible": true}, 5]}}
	  ],
	  "artists": null,
	  "playlists": [{"uri": "x://p/1", "name": "Mix", "owner": 12}],
	  "radio": []
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(answer)) }))
	defer srv.Close()
	f, err := Client{URL: srv.URL, Token: "tok"}.SearchAll(context.Background(), "x", 12)
	if err != nil {
		t.Fatal(err)
	}
	years := []int{1999, 2001, 2004, 0, 0}
	if len(f.Albums) != 6 {
		t.Fatalf("%d albums", len(f.Albums))
	}
	for i, want := range years {
		if f.Albums[i].Year != want {
			t.Errorf("album %d: year %d, want %d", i, f.Albums[i].Year, want)
		}
	}
	if a := f.Albums[1]; a.ItemID != "2" || a.By() != "Moss, Alder" || a.Artists[1].ItemID != "a7" {
		t.Errorf("an album with a number for an id and mixed artists: %+v", a)
	}
	if f.Albums[4].Version != "2" || f.Albums[5].Usable() || !f.Albums[0].Usable() {
		t.Errorf("version %q, the string item usable %v", f.Albums[4].Version, f.Albums[5].Usable())
	}
	t1, t2, t3 := f.Tracks[0], f.Tracks[1], f.Tracks[2]
	if t1.Duration != 215 || t1.TrackNumber != 3 || t1.DiscNumber != 1 || t1.Album == nil || t1.Album.ItemID != "p1" ||
		t1.Album.Image == nil || t1.Album.Image.ProxyID != "pp" {
		t.Errorf("a track with its album: %+v", t1)
	}
	if t2.Duration != 187 || t2.Album == nil || t2.Album.Name != "Just a name" || t2.Album.ItemID != "" {
		t.Errorf("a track with a bare album: %+v", t2)
	}
	if t3.Album != nil || t3.Image != nil || len(t3.Metadata.Images) != 1 || !t3.Metadata.Images[0].Remote {
		t.Errorf("a track without an album: %+v", t3)
	}
	if len(f.Playlists) != 1 || f.Playlists[0].Owner != "12" {
		t.Errorf("playlists %+v", f.Playlists)
	}
}

// An answer that cannot be read at all is said in words a screen can show, not as the decoder's.
func TestAnUnreadableAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"albums": 5}`)) }))
	defer srv.Close()
	_, err := Client{URL: srv.URL, Token: "tok"}.SearchAll(context.Background(), "x", 12)
	if !errors.Is(err, ErrUnreadable) || err.Error() != "Music Assistant sent something this device couldn't read" {
		t.Errorf("got %v", err)
	}
}

// A queue's shuffle is set with the queue's own command.
func TestSetShuffle(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`null`))
	}))
	defer srv.Close()
	if err := (Client{URL: srv.URL, Token: "tok"}).SetShuffle(context.Background(), "up0a1b2c3d", true); err != nil {
		t.Fatal(err)
	}
	args, _ := body["args"].(map[string]any)
	if body["command"] != "player_queues/shuffle" || args["queue_id"] != "up0a1b2c3d" || args["shuffle_enabled"] != true {
		t.Errorf("sent %v", body)
	}
}
