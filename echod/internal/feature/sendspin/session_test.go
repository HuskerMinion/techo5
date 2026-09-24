package sendspin

import (
	"testing"
	"time"
)

// What an end gives back, and when. A stream ending is what a skip and a pause both look like from here,
// so a release inside a live session is held back far enough that a skip does not flash the clock, and
// it is not made at all for a pause the room asked for - that one keeps the track on the screen with
// play offered. The connection going is the one case that takes the room back whatever was asked for.
//
// The bug this replaces zeroed the claim before the pause branch returned, so the room was held for the
// life of the daemon: the idle screen stuck on the now-playing page, the media player entity reading
// Paused forever, every transport press going to a session that was gone, and only a restart clearing it.
func TestWhatAnEndGivesBack(t *testing.T) {
	for _, tc := range []struct {
		name      string
		claim     uint64
		asked     string
		now       bool
		wantClaim uint64
		wantAfter time.Duration
	}{
		{"nothing held gives nothing back", 0, "", false, 0, 0},
		{"a stream ending gives the room back after the grace", 1, "", false, 1, changeGrace},
		{"a skip the room asked for still gives it back after the grace", 2, "next", false, 2, changeGrace},
		{"a pause the room asked for holds the room", 3, "pause", false, 0, 0},
		{"the connection going gives it back however it was left", 4, "pause", true, 4, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &session{}
			s.claim = tc.claim
			s.asked.Store(tc.asked)

			claim, after := s.released(tc.now)

			if claim != tc.wantClaim || after != tc.wantAfter {
				t.Errorf("released(now = %v) = (%d, %v), want (%d, %v)",
					tc.now, claim, after, tc.wantClaim, tc.wantAfter)
			}
		})
	}
}

// A stop the room asked for is given back at once, on its own request rather than on the server's answer
// to it. Music Assistant has usually ended the stream already — that is what a pause on its side is — and
// it sends only what changes, so it answers a stop with silence. A release waiting for that answer left
// the room held, showing a track nobody could get rid of, until the connection dropped.
//
// At once, and not after the grace, because the grace is for a handover: it is there so a skip does not
// flash the clock on the way to the next stream, and nothing follows a stop. Held back, it showed the
// stopped track for two seconds after the press, which is the defect finish() had and was measured with.
// The claim is written on the session's own goroutine, so what is tested here is the hand-over of the
// ask; what the run loop then does with it is stopRelease's, tested below.
func TestAStopAsksTheSessionForTheRoom(t *testing.T) {
	s := &session{releaseAsked: make(chan struct{}, 1)}

	s.askRelease()
	select {
	case <-s.releaseAsked:
	default:
		t.Fatal("a stop did not reach the session")
	}

	// A second ask while one is pending is the same ask, and the goroutine that asked is never waited on.
	s.askRelease()
	s.askRelease()
	select {
	case <-s.releaseAsked:
	default:
		t.Fatal("the pending ask was lost")
	}
	select {
	case <-s.releaseAsked:
		t.Error("a second ask was queued behind the first")
	default:
	}
}

// A stop with nothing decoding gives the room back at once; one that arrives while a stream is still
// arriving does not, because what is already buffered would go on playing with the room showing nothing
// and no Stop row left to press. It is remembered instead, and the stream's own end is where it goes back
// — at once rather than after the grace, which ended does when stopAsked is set.
func TestAStopWhileAStreamIsArrivingWaitsForIt(t *testing.T) {
	s := &session{}
	if !s.stopRelease() {
		t.Error("a stop with nothing being decoded waited for a stream")
	}
	if s.stopAsked {
		t.Error("a stop with nothing being decoded was remembered for later")
	}

	s.dec = fakeDecoder{}
	if s.stopRelease() {
		t.Error("a stop went back at once while a stream was still being decoded")
	}
	if !s.stopAsked {
		t.Fatal("the stop was not remembered for the stream's end")
	}
}

// fakeDecoder is a decoder that does nothing, for the tests that only ask whether one is there.
type fakeDecoder struct{}

func (fakeDecoder) decode([]byte) ([]int16, error) { return nil, nil }
func (fakeDecoder) close() error                   { return nil }
