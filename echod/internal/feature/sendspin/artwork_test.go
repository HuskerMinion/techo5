package sendspin

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/Sendspin/sendspin-go/pkg/protocol"
	ssync "github.com/Sendspin/sendspin-go/pkg/sync"
)

// Which messages are the player's. Music Assistant ends its artwork stream with stream/end naming only
// "artwork", and read as the player's that stopped the music along with the picture. No roles at all
// is the spec's "every role".
func TestNamesRoles(t *testing.T) {
	for _, tc := range []struct {
		name  string
		roles []string
		role  string
		want  bool
	}{
		{"no roles is every role: player", nil, "player", true},
		{"no roles is every role: artwork", nil, "artwork", true},
		{"the player's end", []string{"player"}, "player", true},
		{"the player's end is not the picture's", []string{"player"}, "artwork", false},
		{"the picture's end is not the player's", []string{"artwork"}, "player", false},
		{"the picture's end", []string{"artwork"}, "artwork", true},
		{"a visualizer's clear is not the player's", []string{"visualizer"}, "player", false},
		{"both named", []string{"artwork", "player"}, "player", true},
	} {
		if got := names(tc.roles, tc.role); got != tc.want {
			t.Errorf("%s: names(%v, %q) = %v, want %v", tc.name, tc.roles, tc.role, got, tc.want)
		}
	}
}

// The transfer as a 1.0 server sends it, built the way aiosendspin's models/artwork.py packs it: an
// announce of type, flags 0x02, the int64 timestamp and the uint32 size (">BBqI", 14 bytes), then
// parts of type, flags 0 and at most 65517 bytes of the image, and a cancel of type and flags 0x01.
const wirePartMost = 65519 - 2

func wireAnnounce(at int64, size uint32) []byte {
	m := []byte{8, artworkAnnounce}
	m = binary.BigEndian.AppendUint64(m, uint64(at))
	return binary.BigEndian.AppendUint32(m, size)
}

func wireParts(image []byte) [][]byte {
	var parts [][]byte
	for off := 0; off < len(image); off += wirePartMost {
		parts = append(parts, append([]byte{8, 0}, image[off:min(off+wirePartMost, len(image))]...))
	}
	return parts
}

func wireCancel() []byte { return []byte{8, artworkCancel} }

// throughLibrary is a message as the session gets it: the bridge puts 8 bytes of padding after the
// type, and the library cuts the type and 8 bytes of "timestamp" off.
func throughLibrary(wire []byte) []byte {
	padded := append(append([]byte{wire[0]}, make([]byte, 8)...), wire[1:]...)
	return padded[1+8:]
}

func picture(n int) []byte {
	image := make([]byte, n)
	for i := range image {
		image[i] = byte(i * 7)
	}
	return image
}

// The sizes #708's log showed: a picture of 65517+6194 bytes came as an announce and two parts, and
// each was read as a picture of its own. Put back together, it is the image the server sent, at the
// time the announce gave.
func TestAssemblyPutsPartsBackTogether(t *testing.T) {
	for _, size := range []int{1, 6, 7, wirePartMost, wirePartMost + 1, wirePartMost + 6194, 3*wirePartMost + 3} {
		image := picture(size)
		var a assembly
		kind, _, _ := a.take(throughLibrary(wireAnnounce(1234567, uint32(size))))
		if kind != artworkDiscard {
			t.Fatalf("size %d: the announce came to %v, want the pending picture dropped", size, kind)
		}
		parts := wireParts(image)
		for i, p := range parts {
			kind, at, got := a.take(throughLibrary(p))
			if i < len(parts)-1 {
				if kind != artworkNothing {
					t.Fatalf("size %d: part %d of %d came to %v", size, i+1, len(parts), kind)
				}
				continue
			}
			if kind != artworkWhole || at != 1234567 || !bytes.Equal(got, image) {
				t.Fatalf("size %d: the last part came to %v at %d with %d bytes, want the whole picture at 1234567", size, kind, at, len(got))
			}
		}
		if a.on {
			t.Errorf("size %d: still in flight after the last part", size)
		}
	}
}

// An announce of size 0 is a picture cleared, at its time, with no parts.
func TestAssemblyEmptyClears(t *testing.T) {
	var a assembly
	kind, at, image := a.take(throughLibrary(wireAnnounce(42, 0)))
	if kind != artworkWhole || at != 42 || len(image) != 0 || a.on {
		t.Errorf("came to %v at %d with %d bytes, in flight %v; want an empty picture at 42", kind, at, len(image), a.on)
	}
}

// A cancel drops the picture in flight, and a new announce drops one not yet complete: the parts
// that follow are the new picture's alone.
func TestAssemblyCancelAndReannounce(t *testing.T) {
	first, second := picture(wirePartMost+100), picture(300)

	var a assembly
	a.take(throughLibrary(wireAnnounce(1, uint32(len(first)))))
	a.take(throughLibrary(wireParts(first)[0]))
	if kind, _, _ := a.take(throughLibrary(wireCancel())); kind != artworkDiscard || a.on {
		t.Fatalf("a cancel came to %v, in flight %v", kind, a.on)
	}
	if kind, _, _ := a.take(throughLibrary(wireParts(first)[1])); kind != artworkNothing {
		t.Fatalf("a part after the cancel came to %v", kind)
	}

	a.take(throughLibrary(wireAnnounce(1, uint32(len(first)))))
	a.take(throughLibrary(wireParts(first)[0]))
	a.take(throughLibrary(wireAnnounce(2, uint32(len(second)))))
	kind, at, got := a.take(throughLibrary(wireParts(second)[0]))
	if kind != artworkWhole || at != 2 || !bytes.Equal(got, second) {
		t.Errorf("after a new announce came to %v at %d with %d bytes, want the second picture", kind, at, len(got))
	}
}

// Parts past the announced size drop the picture; a picture too big is counted past, not kept; and
// a malformed message changes nothing.
func TestAssemblyMismatches(t *testing.T) {
	var a assembly
	a.take(throughLibrary(wireAnnounce(1, 10)))
	if kind, _, _ := a.take(throughLibrary(append([]byte{8, 0}, picture(11)...))); kind != artworkNothing || a.on {
		t.Errorf("parts past the size came to %v, in flight %v", kind, a.on)
	}

	a.take(throughLibrary(wireAnnounce(1, artworkMost+1)))
	if !a.on || a.keep {
		t.Fatalf("too big: in flight %v, kept %v; want counted past", a.on, a.keep)
	}
	for _, p := range wireParts(picture(artworkMost + 1)) {
		if kind, _, _ := a.take(throughLibrary(p)); kind != artworkNothing {
			t.Fatalf("a part of a picture too big came to %v", kind)
		}
	}
	if a.on || a.data != nil {
		t.Errorf("too big: in flight %v after its last part, %d bytes held", a.on, len(a.data))
	}

	a.take(throughLibrary(wireAnnounce(1, 3)))
	for _, bad := range [][]byte{
		{},                         // no flags
		{0x04, 1, 2},               // a reserved flag
		{0x03},                     // cancel and announce both
		{artworkAnnounce, 0, 0, 0}, // an announce of the wrong length
	} {
		if kind, _, _ := a.take(bad); kind != artworkNothing || !a.on {
			t.Errorf("% x came to %v, in flight %v; want it ignored", bad, kind, a.on)
		}
	}
	if kind, _, got := a.take(throughLibrary([]byte{8, 0, 1, 2, 3})); kind != artworkWhole || !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Errorf("the picture in flight came to %v with % x after the malformed messages", kind, got)
	}
}

// A picture for later waits for its time, and a cancel drops it, as the server asks for one it no
// longer wants shown (a skip, or the queue changed under it). The unencrypted wire is as before.
func TestPicturedSchedulesAndCancels(t *testing.T) {
	s := &session{clock: ssync.NewClockSync(), encrypted: true}
	later := s.clock.ServerMicrosNow() + (10 * time.Second).Microseconds()
	image := picture(20)
	for _, m := range append([][]byte{wireAnnounce(later, uint32(len(image)))}, wireParts(image)...) {
		s.pictured(protocol.ArtworkChunk{Channel: 0, Data: throughLibrary(m)})
	}
	if s.pending == nil {
		t.Fatal("a picture for 10 s from now is not waiting for its time")
	}
	shown := s.picture.Load()
	s.pictured(protocol.ArtworkChunk{Channel: 0, Data: throughLibrary(wireCancel())})
	if s.pending != nil {
		t.Error("the cancel left the picture waiting")
	}
	if s.picture.Load() != shown {
		t.Error("the cancel changed the current picture")
	}

	// A new announce drops one waiting, too.
	for _, m := range append([][]byte{wireAnnounce(later, uint32(len(image)))}, wireParts(image)...) {
		s.pictured(protocol.ArtworkChunk{Channel: 0, Data: throughLibrary(m)})
	}
	s.pictured(protocol.ArtworkChunk{Channel: 0, Data: throughLibrary(wireAnnounce(later, 5))})
	if s.pending != nil {
		t.Error("a new announce left the last picture waiting")
	}

	plain := &session{clock: ssync.NewClockSync()}
	plain.pictured(protocol.ArtworkChunk{Channel: 0, Timestamp: later, Data: image})
	if plain.pending == nil {
		t.Error("unencrypted: a picture for 10 s from now is not waiting for its time")
	}
	plain.unpend()
}
