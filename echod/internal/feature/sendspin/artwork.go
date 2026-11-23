package sendspin

import (
	"encoding/binary"
	"log/slog"
	"slices"
	"time"

	"github.com/Sendspin/sendspin-go/pkg/protocol"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// The track's picture, on artwork channel 0. It comes one of two ways, and which is down to the wire,
// not to the server's version:
//
//   - On the unencrypted wire the device says its artwork channels in client/hello, as a client from
//     before the 1.0 specification does, and the server sends each picture whole, as one message: the
//     time it belongs to, then the encoded image, and an empty image for a track without one.
//   - On the encrypted wire the channels are said in client/state instead, and a server that is told
//     them there takes the device for a 1.0 client and sends each picture as a transfer: an announce
//     with the time and the image's size, then the image in parts of up to 64 KiB, and a cancel for a
//     picture it no longer wants shown. Music Assistant 2.10.13 does this (#708). The library knows
//     only the first way and cut the first 9 bytes off every message as a timestamp, so the announce
//     reached the decoder as 5 bytes and every part with its start missing, and no picture was ever
//     shown. The bridge puts 8 bytes of padding after each artwork message's type so the library's cut
//     takes only those, and the parts are put back together here, in assembly.

// artworkSide is the square the server is asked to fit the picture in. The page draws it behind the
// words, toned down, so it does not need to be sharp; a bigger one is only more to decode on this CPU.
const artworkSide = 600

// artworkLead bounds how long a picture sent ahead of its track waits. The server times it to the
// audible change, and on the encrypted wire announces it up to 20 seconds early, but a clock not yet
// synced can put that anywhere, and a picture shown early is better than one that never is.
const artworkLead = 30 * time.Second

// artworkMost is the largest picture put back together. A 600-pixel square JPEG is a few hundred KiB
// at the very most; one announced as bigger than this is not read into memory, only counted past.
const artworkMost = 4 << 20

// The flags byte that follows the type of an artwork message on the encrypted wire.
const (
	artworkCancel   = 0x01
	artworkAnnounce = 0x02
	// The rest are reserved, and must be zero.
	artworkReserved = 0xFC
	// An announce after its type: flags, the int64 timestamp and the uint32 size.
	artworkAnnounceLen = 1 + 8 + 4
)

// pictured takes an artwork message from the server.
func (s *session) pictured(a protocol.ArtworkChunk) {
	if a.Channel != 0 {
		return
	}
	if !s.encrypted {
		s.pend(a.Timestamp, a.Data)
		return
	}
	switch kind, at, image := s.parts.take(a.Data); kind {
	case artworkWhole:
		s.pend(at, image)
	case artworkDiscard:
		s.unpend()
	}
}

// pend makes image the pending picture, shown once the server's clock reaches at: at once if that has
// passed, or is too far off to trust. It replaces a picture still waiting for its time. An empty image
// clears the picture.
func (s *session) pend(at int64, image []byte) {
	s.unpend()
	wait := time.Duration(at-s.clock.ServerMicrosNow()) * time.Microsecond
	if wait > 0 && wait < artworkLead {
		s.pending = time.AfterFunc(wait, func() { s.show(image) })
		return
	}
	s.show(image)
}

// show makes image the current picture. One still being decoded is dropped once a newer one is
// current, so the order they became current in is the order they are shown in.
//
// One picture is decoded at a time, off the read loop, which carries the audio too: a server that sends
// pictures faster than this board decodes them would otherwise start a full-size decode for each, all
// at once, and run the device out of memory. A picture that waits is replaced by a newer one, which is
// the only one worth showing.
func (s *session) show(image []byte) {
	n := s.picture.Add(1)
	s.art.mu.Lock()
	s.art.next, s.art.n = image, n
	start := !s.art.busy
	s.art.busy = true
	s.art.mu.Unlock()
	if start {
		safe.Go("sendspin artwork", s.decodePictures)
	}
}

// decodePictures decodes the waiting picture, then the next one to have come meanwhile, until none
// is waiting.
func (s *session) decodePictures() {
	for {
		s.art.mu.Lock()
		image, n := s.art.next, s.art.n
		if n == 0 {
			s.art.busy = false
			s.art.mu.Unlock()
			return
		}
		s.art.next, s.art.n = nil, 0
		s.art.mu.Unlock()
		current := func() bool { return s.picture.Load() == n }
		if current() {
			home.RemoteArt(image, current)
		}
	}
}

// unpend drops the picture waiting for its time, if there is one. The current picture stays. A
// picture whose time came as this ran is already current, which is as the server would have it.
func (s *session) unpend() {
	if s.pending != nil {
		s.pending.Stop()
		s.pending = nil
	}
}

// unpictured clears the picture and drops any still on its way.
func (s *session) unpictured() {
	s.unpend()
	s.parts = assembly{}
	n := s.picture.Add(1)
	home.RemoteArt(nil, func() bool { return s.picture.Load() == n })
}

// What one artwork message on the encrypted wire comes to.
type artworkKind int

const (
	artworkNothing artworkKind = iota // nothing to do yet, or a message dropped
	artworkWhole                      // a whole picture, empty to clear it
	artworkDiscard                    // the pending picture is dropped: a new one is announced, or it is canceled
)

// assembly puts back together a picture the server sends in parts on the encrypted wire. The server
// sends one at a time: an announce, then parts in order until they add up to its size. The
// specification has a client close the connection over a message out of that order; a picture is not
// worth stopping the music for, so here the picture is dropped and the next announce starts again.
// Read and written on the session's own goroutine only.
type assembly struct {
	on   bool   // a transfer is in flight
	keep bool   // and its parts are kept: false for one too big to read in
	at   int64  // when it is to be shown, on the server's clock
	size int    // its size, from the announce
	got  int    // the bytes of it received so far
	data []byte // those bytes, when kept
}

// take reads one artwork message, m being what follows its type byte: the flags, then the rest.
func (t *assembly) take(m []byte) (kind artworkKind, at int64, image []byte) {
	if len(m) == 0 || m[0]&artworkReserved != 0 {
		slog.Info("sendspin picture: a malformed message", "bytes", len(m))
		return artworkNothing, 0, nil
	}
	switch m[0] {
	case artworkAnnounce:
		if len(m) != artworkAnnounceLen {
			slog.Info("sendspin picture: a malformed announce", "bytes", len(m))
			return artworkNothing, 0, nil
		}
		if t.on {
			slog.Info("sendspin picture: announced before the last was complete", "had", t.got, "of", t.size)
		}
		at = int64(binary.BigEndian.Uint64(m[1:9]))
		size := binary.BigEndian.Uint32(m[9:13])
		*t = assembly{}
		if size == 0 {
			return artworkWhole, at, nil
		}
		*t = assembly{on: true, keep: size <= artworkMost, at: at, size: int(size)}
		if t.keep {
			t.data = make([]byte, 0, size)
		} else {
			slog.Info("sendspin picture: too big, not shown", "bytes", size)
		}
		return artworkDiscard, 0, nil

	case artworkCancel:
		*t = assembly{}
		return artworkDiscard, 0, nil

	case 0:
		part := m[1:]
		if !t.on {
			slog.Info("sendspin picture: a part with no announce", "bytes", len(part))
			return artworkNothing, 0, nil
		}
		if len(part) > t.size-t.got {
			slog.Info("sendspin picture: parts past the announced size", "size", t.size, "past", t.got+len(part))
			*t = assembly{}
			return artworkNothing, 0, nil
		}
		t.got += len(part)
		if t.keep {
			t.data = append(t.data, part...)
		}
		if t.got < t.size {
			return artworkNothing, 0, nil
		}
		at, image, keep := t.at, t.data, t.keep
		*t = assembly{}
		if !keep {
			return artworkNothing, 0, nil
		}
		return artworkWhole, at, image
	}
	// Cancel and announce both set.
	slog.Info("sendspin picture: a malformed message", "flags", m[0])
	return artworkNothing, 0, nil
}

// names says whether a stream/clear or stream/end is for role. No roles at all means every role.
func names(roles []string, role string) bool {
	return len(roles) == 0 || slices.Contains(roles, role)
}
