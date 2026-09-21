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

// A stream this player is only carrying is somebody else's, so a transport goes to whoever is playing it
// and never to the stream underneath: pausing this player's own stream would silence the wrong thing.
func TestATransportForACarriedStreamGoesToTheRemote(t *testing.T) {
	for _, tc := range []struct {
		name string
		t    Transport
		want string
	}{
		{"play or pause", TransportToggle, "pause"},
		{"next", TransportNext, "next"},
		{"the stop the screen's row asks for", TransportStop, "stop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := localStream(false)
			p := &Player{stream: s}
			// What External() sets. The claim itself is claim_test.go's; this is only about where a
			// command goes once somebody else has the speaker, and this player is playing its own
			// stream throughout.
			p.remoteLast.Store(true)

			var got []Transport
			cancel := p.OnTransport.Listen(func(tr Transport) { got = append(got, tr) })
			defer cancel()

			p.Transport(tc.t)

			if len(got) != 1 {
				t.Fatalf("the remote heard %d commands, want 1", len(got))
			}
			if name := got[0].Command(); name != tc.want {
				t.Errorf("the remote heard %q, want %q", name, tc.want)
			}
			if _, paused := s.Playing(); paused {
				t.Error("a command for a carried stream paused this player's own stream")
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
