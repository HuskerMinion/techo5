package sendspin

import (
	"slices"
	"time"

	"github.com/Sendspin/sendspin-go/pkg/protocol"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// The track's picture. Music Assistant sends it whole, as one message on artwork channel 0: the time it
// belongs to, then the encoded image, and an empty image for a track without one. (A later draft of the
// spec splits a picture into parts; no released server sends that yet.)

// artworkSide is the square the server is asked to fit the picture in. The page draws it behind the
// words, toned down, so it does not need to be sharp; a bigger one is only more to decode on this CPU.
const artworkSide = 600

// artworkLead bounds how long a picture sent ahead of its track waits. The server times it to the
// audible change, but a clock not yet synced can put that anywhere, and a picture shown early is
// better than one that never is.
const artworkLead = 30 * time.Second

// pictured takes a picture from the server, shown when the track it belongs to is heard.
func (s *session) pictured(a protocol.ArtworkChunk) {
	if a.Channel != 0 {
		return
	}
	n := s.picture.Add(1)
	current := func() bool { return s.picture.Load() == n }
	show := func() { home.RemoteArt(a.Data, current) }

	wait := time.Duration(a.Timestamp-s.clock.ServerMicrosNow()) * time.Microsecond
	if wait > 0 && wait < artworkLead {
		time.AfterFunc(wait, func() { safe.Go("sendspin artwork", show) })
		return
	}
	// Decoded off the read loop, which carries the audio too.
	safe.Go("sendspin artwork", show)
}

// unpictured clears the picture and drops any still on its way.
func (s *session) unpictured() {
	n := s.picture.Add(1)
	home.RemoteArt(nil, func() bool { return s.picture.Load() == n })
}

// names says whether a stream/clear or stream/end is for role. No roles at all means every role.
func names(roles []string, role string) bool {
	return len(roles) == 0 || slices.Contains(roles, role)
}
