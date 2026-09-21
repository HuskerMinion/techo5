package sendspin

import "github.com/Sendspin/sendspin-go/pkg/protocol"

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
func (m *metadata) merge(next *protocol.MetadataState) bool {
	before := *m

	if next.HasField("title") {
		m.title = asText(next.Title)
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
