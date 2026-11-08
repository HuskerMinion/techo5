package musicassistant

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// The library, for browsing it on a screen: what the server holds of each kind, an artist's albums
// and top tracks, an album's or a playlist's tracks, and the pictures of them all.

// Entry is one thing a library page lists: an artist, album, track, playlist or radio station.
type Entry struct {
	ItemID    string `json:"item_id"`
	Provider  string `json:"provider"`
	URI       string `json:"uri"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Version   string `json:"version"`
	Year      int    `json:"year"`
	Artists   []struct {
		Name string `json:"name"`
	} `json:"artists"`
	Album *struct {
		Name string `json:"name"`
	} `json:"album"`
	Metadata struct {
		Images []Image `json:"images"`
	} `json:"metadata"`
	// Image is a short item's picture (an album named on a track, an artist on an album): the server
	// sends those with one picture of their own rather than the full metadata.
	Image *Image `json:"image"`
}

// Image is one of an item's pictures as the server describes it.
type Image struct {
	Type     string `json:"type"` // "thumb", "fanart", ...
	Path     string `json:"path"`
	Provider string `json:"provider"`
	Remote   bool   `json:"remotely_accessible"`
	ProxyID  string `json:"proxy_id"` // filled in by the server; what its image proxy serves it by
}

// By is who it is by: the artists' names, or nothing.
func (e Entry) By() string {
	var n []string
	for _, a := range e.Artists {
		if a.Name != "" {
			n = append(n, a.Name)
		}
	}
	return strings.Join(n, ", ")
}

// Kinds a library holds, as the API names them in its commands (music/<kind>/library_items).
const (
	KindArtists   = "artists"
	KindAlbums    = "albums"
	KindTracks    = "tracks"
	KindPlaylists = "playlists"
	KindRadios    = "radios"
)

// Library is a page of the library's items of kind (KindArtists ...), by name, starting at offset.
// With search set, only the ones whose name has it in it.
func (c Client) Library(ctx context.Context, kind string, offset, limit int, search string) ([]Entry, error) {
	args := map[string]any{"limit": limit, "offset": offset, "order_by": "sort_name"}
	if search != "" {
		args["search"] = search
	}
	if kind == KindArtists {
		// Artists only credited on someone else's tracks crowd out the ones with albums to open.
		args["album_artists_only"] = true
	}
	var out []Entry
	err := c.do(ctx, "music/"+kind+"/library_items", args, &out)
	return out, err
}

// ArtistAlbums are an artist's albums, from where the artist is held (its provider, or "library").
func (c Client) ArtistAlbums(ctx context.Context, artist Entry) ([]Entry, error) {
	var out []Entry
	err := c.do(ctx, "music/artists/artist_albums", itemArgs(artist), &out)
	return out, err
}

// TopTracks are an artist's best-known tracks.
func (c Client) TopTracks(ctx context.Context, artist Entry) ([]Entry, error) {
	var out []Entry
	err := c.do(ctx, "music/artists/top_tracks", itemArgs(artist), &out)
	return out, err
}

// AlbumTracks are an album's tracks, in order.
func (c Client) AlbumTracks(ctx context.Context, album Entry) ([]Entry, error) {
	var out []Entry
	err := c.do(ctx, "music/albums/album_tracks", itemArgs(album), &out)
	return out, err
}

// PlaylistTracks are a playlist's tracks, all of them: the server does not page them.
func (c Client) PlaylistTracks(ctx context.Context, playlist Entry) ([]Entry, error) {
	var out []Entry
	err := c.do(ctx, "music/playlists/playlist_tracks", itemArgs(playlist), &out)
	return out, err
}

// SearchAll is a search of the library and every service for query, limit of each kind.
func (c Client) SearchAll(ctx context.Context, query string, limit int) (Found, error) {
	var r Found
	err := c.do(ctx, "music/search", map[string]any{"search_query": query, "limit": limit}, &r)
	return r, err
}

// Found is a search's findings by kind, as library entries.
type Found struct {
	Artists   []Entry `json:"artists"`
	Albums    []Entry `json:"albums"`
	Tracks    []Entry `json:"tracks"`
	Playlists []Entry `json:"playlists"`
	Radio     []Entry `json:"radio"`
}

func itemArgs(e Entry) map[string]any {
	return map[string]any{"item_id": e.ItemID, "provider_instance_id_or_domain": e.Provider}
}

// ImageURL is the address of an entry's picture at about size pixels across, through the server's
// image proxy (which needs no token), or a service's own address when that is all there is; empty
// when it has none.
func (c Client) ImageURL(e Entry, size int) string {
	img := e.Image
	for i := range e.Metadata.Images {
		if e.Metadata.Images[i].Type == "thumb" {
			img = &e.Metadata.Images[i]
			break
		}
	}
	if img == nil {
		return ""
	}
	if img.ProxyID != "" {
		u := strings.TrimRight(c.URL, "/") + "/imageproxy/" + url.PathEscape(img.ProxyID)
		if size > 0 {
			u += "?size=" + strconv.Itoa(size)
		}
		return u
	}
	if img.Remote && (strings.HasPrefix(img.Path, "https://") || strings.HasPrefix(img.Path, "http://")) {
		return img.Path
	}
	return ""
}
