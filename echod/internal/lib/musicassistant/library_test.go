package musicassistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// Each library call is the command the server has for it, with its arguments as the server names
// them: a page of a kind by name (artists only those with albums), an artist's albums and top tracks,
// an album's and a playlist's tracks by item and provider, and a search of every kind. What comes back
// is read as entries.
func TestLibraryCalls(t *testing.T) {
	type call struct {
		Command string         `json:"command"`
		Args    map[string]any `json:"args"`
	}
	var seen []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api" || r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var c call
		json.NewDecoder(r.Body).Decode(&c)
		seen = append(seen, c)
		if c.Command == "music/search" {
			w.Write([]byte(`{"artists":[{"item_id":"1","provider":"library","uri":"library://artist/1","name":"Eagles","media_type":"artist"}],
				"albums":[],"tracks":[{"item_id":"7","provider":"library","uri":"library://track/7","name":"Take It Easy","media_type":"track",
				"artists":[{"name":"Eagles"}],"album":{"name":"Eagles"}}],"playlists":[],"radio":[{"uri":"library://radio/2","name":"KEXP","media_type":"radio"}]}`))
			return
		}
		w.Write([]byte(`[{"item_id":"5","provider":"library","uri":"library://album/5","name":"Hotel California","media_type":"album",
			"artists":[{"name":"Eagles"}],"metadata":{"images":[{"type":"thumb","path":"/x.jpg","provider":"file","proxy_id":"abc"}]}}]`))
	}))
	defer srv.Close()
	c := Client{URL: srv.URL, Token: "tok"}
	ctx := context.Background()
	artist := Entry{ItemID: "1", Provider: "library"}
	album := Entry{ItemID: "5", Provider: "library"}
	playlist := Entry{ItemID: "9", Provider: "spotify--x"}

	got, err := c.Library(ctx, KindAlbums, 48, 24, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "Hotel California" || got[0].By() != "Eagles" || got[0].Metadata.Images[0].ProxyID != "abc" {
		t.Errorf("albums came back as %+v", got)
	}
	steps := []func() error{
		func() error { _, err := c.Library(ctx, KindArtists, 0, 12, "eag"); return err },
		func() error { _, err := c.Library(ctx, KindRadios, 0, 12, ""); return err },
		func() error { _, err := c.ArtistAlbums(ctx, artist); return err },
		func() error { _, err := c.TopTracks(ctx, artist); return err },
		func() error { _, err := c.AlbumTracks(ctx, album); return err },
		func() error { _, err := c.PlaylistTracks(ctx, playlist); return err },
	}
	for _, s := range steps {
		if err := s(); err != nil {
			t.Fatal(err)
		}
	}
	f, err := c.SearchAll(ctx, "eagles", 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Artists) != 1 || len(f.Tracks) != 1 || f.Tracks[0].Album == nil || f.Tracks[0].Album.Name != "Eagles" || len(f.Radio) != 1 {
		t.Errorf("search came back as %+v", f)
	}

	want := []call{
		{"music/albums/library_items", map[string]any{"limit": 24.0, "offset": 48.0, "order_by": "sort_name"}},
		{"music/artists/library_items", map[string]any{"limit": 12.0, "offset": 0.0, "order_by": "sort_name", "search": "eag", "album_artists_only": true}},
		{"music/radios/library_items", map[string]any{"limit": 12.0, "offset": 0.0, "order_by": "sort_name"}},
		{"music/artists/artist_albums", map[string]any{"item_id": "1", "provider_instance_id_or_domain": "library"}},
		{"music/artists/top_tracks", map[string]any{"item_id": "1", "provider_instance_id_or_domain": "library"}},
		{"music/albums/album_tracks", map[string]any{"item_id": "5", "provider_instance_id_or_domain": "library"}},
		{"music/playlists/playlist_tracks", map[string]any{"item_id": "9", "provider_instance_id_or_domain": "spotify--x"}},
		{"music/search", map[string]any{"search_query": "eagles", "limit": 12.0}},
	}
	if len(seen) != len(want) {
		t.Fatalf("%d calls, want %d: %+v", len(seen), len(want), seen)
	}
	for i := range want {
		if !reflect.DeepEqual(seen[i], want[i]) {
			t.Errorf("call %d was %+v, want %+v", i, seen[i], want[i])
		}
	}
}

// A picture is the server's image proxy by its proxy id, at a size or the original, and its thumb
// before any other; a service's own address when that is all there is and it can be reached; and
// nothing for an entry without a picture, or one only the server can open.
func TestImageURL(t *testing.T) {
	c := Client{URL: "http://ma.local:8095/"}
	thumb := Entry{}
	thumb.Metadata.Images = []Image{{Type: "fanart", ProxyID: "fan"}, {Type: "thumb", ProxyID: "a b/c"}}
	short := Entry{Image: &Image{Type: "thumb", ProxyID: "short"}}
	remote := Entry{Image: &Image{Type: "thumb", Path: "https://i.scdn.co/image/x", Remote: true}}
	local := Entry{Image: &Image{Type: "thumb", Path: "/music/cover.jpg"}}
	for _, c2 := range []struct {
		name string
		e    Entry
		size int
		want string
	}{
		{"thumb", thumb, 256, "http://ma.local:8095/imageproxy/a%20b%2Fc?size=256"},
		{"original", thumb, 0, "http://ma.local:8095/imageproxy/a%20b%2Fc"},
		{"short item", short, 256, "http://ma.local:8095/imageproxy/short?size=256"},
		{"remote", remote, 256, "https://i.scdn.co/image/x"},
		{"local only", local, 256, ""},
		{"none", Entry{}, 256, ""},
	} {
		if got := c.ImageURL(c2.e, c2.size); got != c2.want {
			t.Errorf("%s: %q, want %q", c2.name, got, c2.want)
		}
	}
}
