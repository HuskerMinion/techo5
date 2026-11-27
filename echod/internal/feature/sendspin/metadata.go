package sendspin

import (
	"regexp"
	"strings"

	"github.com/Sendspin/sendspin-go/pkg/protocol"
)

// metadata is the track the server last described, kept across the messages that only change part of
// it rather than replaced by them.
//
// The wire encoding is tristate: a key the server omits means "unchanged, preserve the prior value",
// a JSON null means "clear it", and a value means "set it". The first two both arrive as a nil
// pointer, so the only thing that says which was meant is whether the key was there at all — HasField.
// Merging the whole struct on every message would clear whatever that message left out, and most of
// them leave out most of it: the track is sent once and the progress ticks follow on their own.
type metadata struct {
	title  string
	artist string
	album  string
}

// merge applies one metadata message and reports whether the track changed. Fields the message does
// not mention are left as they were.
//
// Workaround for Music Assistant: aiosendspin pushes full SessionUpdateMetadata snapshots, but
// omits None fields from JSON instead of sending explicit nulls. When switching to a title-only
// stream (such as a live radio station with no song/artist tags), a new title arrives with neither
// artist nor album keys; clearing both then avoids showing the previous station's artist and album.
func (m *metadata) merge(next *protocol.MetadataState) bool {
	before := *m

	if next.HasField("title") {
		nextTitle := cleanTitle(asText(next.Title))
		if nextTitle != m.title && !next.HasField("artist") && !next.HasField("album") {
			m.artist = ""
			m.album = ""
		}
		m.title = nextTitle
	}
	if next.HasField("artist") {
		m.artist = asText(next.Artist)
	}
	if next.HasField("album") {
		m.album = asText(next.Album)
	}

	return *m != before
}

// asText is what a tristate string field carries: what it points at, or empty for a cleared one.
func asText(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// taggedTitle is a title as iHeart's stations send it: nothing but tags, key="value" after key="value",
// the song in text when it is a song (text="Static" song_spot="M" MediaBaseId="3097657" ...), and an
// advert's markers when it is not (adContext="..."). Music Assistant passes it on as it came.
var (
	taggedTitle = regexp.MustCompile(`^\s*[A-Za-z_]+="[^"]*"(\s+[A-Za-z_]+="[^"]*")*\s*$`)
	textTag     = regexp.MustCompile(`(?:^|\s)text="([^"]*)"`)
)

// cleanTitle is a title fit for the screen: a tagged one's song, or none for one that has no song (the
// screen then names the station); anything else as it is.
func cleanTitle(s string) string {
	if !taggedTitle.MatchString(s) {
		// Not split yet ("Artist - text=..." as the station sent it): the song is still its text.
		if strings.Contains(s, `song_spot="`) || strings.Contains(s, `MediaBaseId="`) {
			if m := textTag.FindStringSubmatch(s); m != nil {
				return m[1]
			}
		}
		return s
	}
	if m := textTag.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}
