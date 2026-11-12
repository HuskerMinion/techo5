package musicassistant

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// The library, for browsing it on a screen: what the server holds of each kind, an artist's albums
// and top tracks, an album's or a playlist's tracks, and the pictures of them all.

// Entry is one thing a library page lists: an artist, album, track, playlist or radio station.
//
// It is read leniently (UnmarshalJSON): the server passes on what each music service says, and they do
// not agree on types. A year comes as 2001 from one and "2001" from another, a track's album as an
// object, a bare name or null. A field that is only shown never fails the whole answer: what cannot be
// read is left empty, and an item that is not an object at all is an empty Entry (Usable says so).
type Entry struct {
	ItemID    string
	Provider  string
	URI       string
	Name      string
	MediaType string
	Version   string
	Year      int
	// Duration is a track's length in seconds, TrackNumber and DiscNumber its place on its album, and
	// Owner a playlist's owner; zero or empty when the server does not say.
	Duration int
	// Start is where a chapter of an audiobook begins, in seconds; set from the chapters, not the server.
	Start       float64
	TrackNumber int
	DiscNumber  int
	Owner       string
	// Artists and Album are the items a track or an album names, as short as the server sends them:
	// their id, provider, name, URI and perhaps a picture, or a bare name.
	Artists []Entry
	// Authors and Narrators are an audiobook's authors' and readers' names, as the server gives them
	// (a name alone or an artist with one), and Publisher who published it or a podcast.
	Authors   []string
	Narrators []string
	Publisher string
	// Description is what the item says about itself (its metadata's description), as plain text.
	Description string
	// ResumeMS is where this token's user left an audiobook or a podcast episode, in milliseconds, and
	// FullyPlayed whether they finished it; zero and false when the server does not say.
	ResumeMS    int
	FullyPlayed bool
	// Items are a series' parts in order, for an entry of media type "collection": the audiobooks a
	// library listing folds into one row.
	Items []Entry
	// Path is where a folder is in the server's browse tree ("filesystem_local--x://Artist/Album"),
	// for opening it; empty for anything that is not a folder there.
	Path string
	// DateAdded is when the server added the item to the library, as it sends it (an ISO time), if it does.
	DateAdded string
	// Favorite is whether the item is one of the library's favorites, as the server says.
	Favorite bool
	// QueueItemID is the item's id in a player's queue, for an entry read from one (Queue); empty
	// otherwise.
	QueueItemID string
	Album       *Entry
	Metadata    struct {
		Images []Image
	}
	// Image is a short item's picture (an album named on a track, an artist on an album): the server
	// sends those with one picture of their own rather than the full metadata.
	Image *Image
}

// Image is one of an item's pictures as the server describes it.
type Image struct {
	Type     string // "thumb", "fanart", ...
	Path     string
	Provider string
	Remote   bool   // remotely_accessible
	ProxyID  string // filled in by the server; what its image proxy serves it by
}

// UnmarshalJSON reads an entry field by field, each as leniently as it can be (see Entry).
func (e *Entry) UnmarshalJSON(b []byte) error {
	*e = Entry{}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil || m == nil {
		return nil // not an item: nothing of it to show, and the rest of the list still is
	}
	*e = Entry{
		ItemID: looseText(m["item_id"]), Provider: looseText(m["provider"]), URI: looseText(m["uri"]),
		Name: looseText(m["name"]), MediaType: looseText(m["media_type"]), Version: looseText(m["version"]),
		Owner: looseText(m["owner"]), Year: looseInt(m["year"]), Duration: looseInt(m["duration"]),
		TrackNumber: looseInt(m["track_number"]), DiscNumber: looseInt(m["disc_number"]),
		Artists: looseNames(m["artists"]), Authors: looseStrings(m["authors"]), DateAdded: looseText(m["date_added"]), Path: looseText(m["path"]), Album: looseNamed(m["album"]),
		Narrators: looseStrings(m["narrators"]), Publisher: looseText(m["publisher"]), ResumeMS: looseInt(m["resume_position_ms"]),
	}
	_ = json.Unmarshal(m["favorite"], &e.Favorite)        // anything but true or false leaves it false
	_ = json.Unmarshal(m["fully_played"], &e.FullyPlayed) // null, for not known, too
	var meta struct {
		Images      []json.RawMessage `json:"images"`
		Description json.RawMessage   `json:"description"`
		ReleaseDate json.RawMessage   `json:"release_date"`
	}
	if json.Unmarshal(m["metadata"], &meta) == nil {
		for _, raw := range meta.Images {
			if img, ok := looseImage(raw); ok {
				e.Metadata.Images = append(e.Metadata.Images, img)
			}
		}
		e.Description = shortDescription(PlainText(looseText(meta.Description)))
		if e.Year == 0 {
			e.Year = looseInt(meta.ReleaseDate) // an audiobook or an episode has its year only here
		}
	}
	var items []Entry
	if json.Unmarshal(m["items"], &items) == nil {
		e.Items = usableOnly(items)
	}
	if img, ok := looseImage(m["image"]); ok {
		e.Image = &img
	}
	return nil
}

// Usable is whether the entry names something: one the server sent as something other than an item
// does not.
func (e Entry) Usable() bool { return e.URI != "" || e.Name != "" }

// looseText is a string, a number written out, or empty for anything else.
func looseText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

// looseInt is a number, or the number a string begins with ("2001", "2001-05-04"), or 0.
func looseInt(raw json.RawMessage) int {
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return int(f)
	}
	s := strings.TrimSpace(looseText(raw))
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}

// looseNamed is an item named on another, read as an entry, or a bare name; nil for null or anything
// else.
func looseNamed(raw json.RawMessage) *Entry {
	var e Entry
	if json.Unmarshal(raw, &e) == nil && e.Usable() {
		return &e
	}
	if s := looseText(raw); s != "" {
		return &Entry{Name: s}
	}
	return nil
}

// looseNames is a list of named items, or one alone; what cannot be read is left out.
func looseNames(raw json.RawMessage) []Entry {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		list = []json.RawMessage{raw}
	}
	var out []Entry
	for _, x := range list {
		if n := looseNamed(x); n != nil && n.Name != "" {
			out = append(out, *n)
		}
	}
	return out
}

// looseStrings is a list of names, or one name alone; a name may come as an item with a name (Music
// Assistant 2.10 sends an audiobook's authors as artists). What is not a name is left out.
func looseStrings(raw json.RawMessage) []string {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		list = []json.RawMessage{raw}
	}
	var out []string
	for _, x := range list {
		s := looseText(x)
		if s == "" {
			var named struct {
				Name json.RawMessage `json:"name"`
			}
			if json.Unmarshal(x, &named) == nil {
				s = looseText(named.Name)
			}
		}
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func usableOnly(list []Entry) []Entry {
	out := list[:0]
	for _, e := range list {
		if e.Usable() {
			out = append(out, e)
		}
	}
	return out
}

// looseImage is a picture's description, if raw is one.
func looseImage(raw json.RawMessage) (Image, bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return Image{}, false
	}
	var remote bool
	_ = json.Unmarshal(m["remotely_accessible"], &remote)
	img := Image{Type: looseText(m["type"]), Path: looseText(m["path"]), Provider: looseText(m["provider"]),
		Remote: remote, ProxyID: looseText(m["proxy_id"])}
	return img, img.Path != "" || img.ProxyID != ""
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
	KindArtists    = "artists"
	KindAlbums     = "albums"
	KindTracks     = "tracks"
	KindPlaylists  = "playlists"
	KindRadios     = "radios"
	KindPodcasts   = "podcasts"
	KindAudiobooks = "audiobooks"
)

// Chapter is one part of an audiobook, in order: its name, and where it starts and ends in seconds.
type Chapter struct {
	Position int     `json:"position"`
	Name     string  `json:"name"`
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
}

// Audiobook is an audiobook read in full: its entry, and its chapters in order when the server sends
// them. A list of audiobooks does not carry the chapters, so they come from here.
func (c Client) Audiobook(ctx context.Context, book Entry) (Entry, []Chapter, error) {
	var raw json.RawMessage
	if err := c.do(ctx, "music/audiobooks/get", itemArgs(book), &raw); err != nil {
		return Entry{}, nil, err
	}
	var e Entry
	if err := e.UnmarshalJSON(raw); err != nil {
		return Entry{}, nil, err
	}
	// The server keeps an audiobook's chapters in its metadata, not at the top of the item.
	var withChapters struct {
		Metadata struct {
			Chapters []Chapter `json:"chapters"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &withChapters); err != nil {
		return e, nil, nil // the entry is usable without its chapters
	}
	return e, withChapters.Metadata.Chapters, nil
}

// ChapterEntry is one chapter of an audiobook as a library entry: its name, its place in the book, and
// where it starts, with the book's own address to play it from.
func ChapterEntry(book Entry, ch Chapter) Entry {
	return Entry{
		Name: ch.Name, MediaType: "chapter", URI: book.URI, Provider: book.Provider, ItemID: book.ItemID,
		TrackNumber: ch.Position + 1, Duration: int(ch.End - ch.Start), Start: ch.Start,
	}
}

// PodcastEpisodes are a podcast's episodes, newest first as the server lists them.
func (c Client) PodcastEpisodes(ctx context.Context, podcast Entry) ([]Entry, error) {
	var out []Entry
	err := c.do(ctx, "music/podcasts/podcast_episodes", itemArgs(podcast), &out)
	return out, err
}

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
	Artists    []Entry `json:"artists"`
	Albums     []Entry `json:"albums"`
	Tracks     []Entry `json:"tracks"`
	Playlists  []Entry `json:"playlists"`
	Radio      []Entry `json:"radio"`
	Podcasts   []Entry `json:"podcasts"`
	Audiobooks []Entry `json:"audiobooks"`
}

// SetShuffle turns the shuffle of the queue queueID on or off. Some players refuse Home Assistant's
// shuffle_set on Music Assistant's entity ("Invalid or unsupported command"); the queue's own takes.
func (c Client) SetShuffle(ctx context.Context, queueID string, on bool) error {
	return c.do(ctx, "player_queues/shuffle", map[string]any{"queue_id": queueID, "shuffle_enabled": on}, nil)
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
