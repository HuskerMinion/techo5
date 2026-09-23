package media

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// localStream is a stream of this player's own with nothing behind it: no arbiter, no queue, no track
// playing beyond the one this puts on it. What the transport does to it is its own state, which is all
// these tests are about.
func localStream(paused bool) *Stream {
	return &Stream{out: &speaker.Player{}, changed: func() {}, track: &track{item: "test"}, paused: paused}
}

// A tap on the now-playing screen is a toggle, and it has to be settled against what is playing. The
// local branch used to pause whatever it was handed, so a second tap paused a paused station again and
// there was no way back to playing from the screen at all.
func TestAToggleOnThisPlayersOwnStreamKnowsWhichWayItIsGoing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		paused bool
	}{
		{"a playing stream pauses", false},
		{"a paused stream plays", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := localStream(tc.paused)
			p := &Player{stream: s}

			p.Transport(TransportToggle)

			if _, paused := s.Playing(); paused == tc.paused {
				t.Errorf("a toggle left paused = %v, want %v", paused, !tc.paused)
			}
		})
	}
}

// Where a transport goes is not "who holds the speaker". A claim outlives a pause by design, and reading
// it as ownership is how a paused Music Assistant took the radio's own play button: a tap on the
// station's page was resolved against the remote's state, so it asked Music Assistant to play while the
// station underneath went on playing, and the two handed the speaker back and forth. What decides is
// what the room is hearing - and, when a remote has stopped with a queue still to resume, which of them
// this player has anything of its own to come back to.
func TestATransportGoesToWhoeverHasTheTrack(t *testing.T) {
	for _, tc := range []struct {
		name       string
		claimed    bool
		remoteSays string
		remoteLast bool
		local      string // "", "playing" or "paused"
		wantRemote bool
	}{
		{"a remote playing over a station", true, "playing", false, "playing", true},
		{"a remote playing alone", true, "playing", false, "", true},
		{"a paused remote over a station this device is playing", true, "paused", false, "playing", false},
		{"a paused remote with nothing of this device's playing", true, "paused", false, "", true},
		{"a remote that stopped, with its queue to resume", false, "", true, "", true},
		{"a remote that stopped over a station this device is playing", false, "", true, "playing", false},
		{"a remote that stopped over a station paused here", false, "", true, "paused", false},
		{"nothing remote, a station playing", false, "", false, "playing", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s *Stream
			switch tc.local {
			case "playing":
				s = localStream(false)
			case "paused":
				s = localStream(true)
			default:
				s = quietStream()
			}
			p := &Player{stream: s}
			if tc.claimed {
				p.remote.take()
			}
			if tc.remoteLast {
				p.remoteLast.Store(true)
			}
			p.remoteState.Store(tc.remoteSays)

			var heard []Transport
			cancel := p.OnTransport.Listen(func(tr Transport) { heard = append(heard, tr) })
			defer cancel()

			p.Transport(TransportToggle)

			if got := len(heard) > 0; got != tc.wantRemote {
				t.Fatalf("the remote heard %v, want the command to be the remote's: %v", heard, tc.wantRemote)
			}
			if tc.wantRemote {
				return
			}
			// The local branch, and the station here is what the button is for: a playing one pauses and
			// a paused one plays.
			wantPaused := tc.local == "playing"
			if _, paused := s.Playing(); paused != wantPaused {
				t.Errorf("after the toggle paused = %v, want %v", paused, wantPaused)
			}
		})
	}
}

// A session going away takes its claim with it, and the last thing it played with it too: with nothing
// left to ask, a transport belongs to this player's own stream again.
//
// Without that it was reachable: a station paused underneath, Music Assistant plays and then drops.
// `finish()` had already removed the only OnTransport listener, so every button — the screen's, Home
// Assistant's, the Spot's — went out to a hook with nobody listening on it and did nothing at all, and
// the station could not be resumed from anywhere until something started a local stream again.
func TestATransportGoesBackToThisPlayerWhenTheSessionIsGone(t *testing.T) {
	for _, tc := range []struct {
		name       string
		session    bool
		station    bool // a station of this player's paused underneath, to resume
		wantRemote bool
	}{
		// With nothing of this player's to resume, a remote that played and is still there owns the
		// buttons. (With a station paused underneath it does not: the station is what the screen
		// offers to resume, since "a remote holding the speaker is not the remote playing".)
		{"a remote that played, with its session still there", true, false, true},
		{"the same remote, with the session gone", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Stream{out: &speaker.Player{}, changed: func() {}}
			if tc.station {
				// A station paused underneath is the state that bites: there is something here to resume.
				s = localStream(true)
			}
			p := &Player{stream: s}
			p.remoteLast.Store(true)
			if !tc.session {
				p.RemoteGone()
			}

			var heard []Transport
			cancel := p.OnTransport.Listen(func(tr Transport) { heard = append(heard, tr) })
			defer cancel()

			p.Transport(TransportToggle)

			if got := len(heard) > 0; got != tc.wantRemote {
				t.Fatalf("the remote heard %v, want the command to be the remote's: %v", heard, tc.wantRemote)
			}
			if tc.wantRemote {
				return
			}
			if _, paused := s.Playing(); paused {
				t.Error("the station underneath was not resumed")
			}
		})
	}
}

// Home Assistant's stop is not the screen's stop, and the difference is who is asking rather than what
// the stream is: an automation with nobody in the room gets a pause, which keeps the track.
func TestTheScreensStopIsAStop(t *testing.T) {
	if got := TransportStop.Command(); got != "stop" {
		t.Errorf("the screen's stop asks the remote for %q, want %q", got, "stop")
	}
	if got := TransportPause.Command(); got != "pause" {
		t.Errorf("Home Assistant's stop asks the remote for %q, want %q", got, "pause")
	}
}
