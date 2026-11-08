//go:build !dot && !spot

package display

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/musicassistant"
)

// A library of saved tracks alone: Music Assistant keeps a track saved from a service in the library
// without its album or artist, so the Artists and Albums tabs would be empty while the Tracks tab is
// full. The tabs are made from the tracks instead, from the album and artists each track names. Those
// carry their own id and provider, so an album opens on its tracks and an artist on its albums from the
// service they are on.

const (
	// libTracksMost is the most saved tracks read to make the tabs from, and libTracksAsk how many are
	// asked for at a time.
	libTracksMost = 2000
	libTracksAsk  = 500
	// libDerivedFor is how long the tabs made are kept before the tracks are read again.
	libDerivedFor = 10 * time.Minute
)

var libDerived struct {
	sync.Mutex
	server         string
	at             time.Time
	artists, album []musicassistant.Entry
}

// libFromTracks is the artists and the albums of the library's saved tracks, each once, by name. They
// are kept a while for the server they came from: both tabs are made from one reading.
func libFromTracks(ctx context.Context, c musicassistant.Client) ([]musicassistant.Entry, []musicassistant.Entry, error) {
	libDerived.Lock()
	defer libDerived.Unlock()
	if libDerived.server == c.URL && time.Since(libDerived.at) < libDerivedFor {
		return libDerived.artists, libDerived.album, nil
	}
	var tracks []musicassistant.Entry
	for len(tracks) < libTracksMost {
		got, err := c.Library(ctx, musicassistant.KindTracks, len(tracks), libTracksAsk, "")
		if err != nil {
			return nil, nil, err
		}
		tracks = append(tracks, got...)
		if len(got) < libTracksAsk {
			break
		}
	}
	artists, albums := libArtistsAndAlbums(tracks)
	libDerived.server, libDerived.at, libDerived.artists, libDerived.album = c.URL, time.Now(), artists, albums
	return artists, albums, nil
}

// libArtistsAndAlbums is the distinct artists and albums the tracks name, by name. An album without a
// picture of its own takes its track's, which is the album's cover for nearly every service, and names
// the track's artists for the line under it. Only what can be opened is kept: an artist or album named
// by name alone has no id to ask for.
func libArtistsAndAlbums(tracks []musicassistant.Entry) (artists, albums []musicassistant.Entry) {
	key := func(e musicassistant.Entry) string {
		if e.URI != "" {
			return e.URI
		}
		return e.Provider + "\x00" + e.ItemID
	}
	seenArtist, seenAlbum := map[string]bool{}, map[string]bool{}
	for _, t := range tracks {
		for _, a := range t.Artists {
			if a.ItemID == "" || a.Name == "" || seenArtist[key(a)] {
				continue
			}
			seenArtist[key(a)] = true
			if a.MediaType == "" {
				a.MediaType = "artist"
			}
			artists = append(artists, a)
		}
		if t.Album == nil || t.Album.ItemID == "" || t.Album.Name == "" || seenAlbum[key(*t.Album)] {
			continue
		}
		al := *t.Album
		seenAlbum[key(al)] = true
		if al.MediaType == "" {
			al.MediaType = "album"
		}
		if al.Image == nil && len(al.Metadata.Images) == 0 {
			al.Image, al.Metadata = t.Image, t.Metadata
		}
		if len(al.Artists) == 0 {
			al.Artists = t.Artists
		}
		albums = append(albums, al)
	}
	byName := func(list []musicassistant.Entry) {
		sort.SliceStable(list, func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) })
	}
	byName(artists)
	byName(albums)
	return artists, albums
}

// usable is the entries that name something: an answer's item the server sent as something other than
// an item is left out rather than drawn as an empty tile.
func usable(list []musicassistant.Entry) []musicassistant.Entry {
	out := list[:0:0]
	for _, e := range list {
		if e.Usable() {
			out = append(out, e)
		}
	}
	return out
}
