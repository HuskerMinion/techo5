package media

import (
	"net/http"
	"net/http/httptest"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// quietStream is this player with no track of its own, which is what it has when a remote is the only
// thing playing.
func quietStream() *Stream {
	return &Stream{out: (&speaker.Player{}).Bed(), changed: func() {}}
}

// Whether the room is somebody else's is about what is being heard, not about who holds the speaker.
// Music Assistant leaves a track it paused holding the speaker while its queue waits, and a station this
// device plays underneath it is the sound in the room: reading the held claim as "the room is Music
// Assistant's" hid the station's own now-playing page, left its Stop row stopping the wrong stream, and
// told Home Assistant the radio was paused while it played.
func TestTheRoomIsCarriedOnlyWhenTheRemoteIsWhatIsHeard(t *testing.T) {
	for _, tc := range []struct {
		name       string
		claimed    bool
		remoteSays string
		local      string // "", "playing" or "paused"
		want       bool
	}{
		{"nothing claimed, a station playing", false, "", "playing", false},
		{"nothing claimed, nothing playing", false, "", "", false},
		{"a remote playing", true, "playing", "", true},
		{"a remote playing over a station of this device's", true, "playing", "playing", true},
		{"a remote paused over a station this device is playing", true, "paused", "playing", false},
		{"a remote paused with nothing of this device's playing", true, "paused", "", true},
		{"a remote paused over a station paused underneath it", true, "paused", "paused", true},
		{"a claim with nothing said yet, nothing local", true, "", "", true},
		{"a claim with nothing said yet over a station playing", true, "", "playing", false},
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
				p.remoteLast.Store(true)
				p.remote.take()
			}
			p.remoteState.Store(tc.remoteSays)

			if got := p.Carried(); got != tc.want {
				t.Errorf("Carried() = %v with a claim %v, the remote saying %q and this player %q, want %v",
					got, tc.claimed, tc.remoteSays, tc.local, tc.want)
			}
		})
	}
}

// A remote starting to play ends this player's own track rather than standing it down to carry on
// underneath. Two pieces of music take turns: a station stood down by a remote came back when the remote
// stopped, so whichever of the two was stopped the other resurfaced, and in the room that looked like a
// speaker that could not be turned off.
func TestARemoteStartingEndsThisPlayersTrack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // a station: it never ends
	}))
	defer srv.Close()

	d := speaker.NewDriver(speaker.New())
	p := &Player{stream: NewStream(d, speaker.New(), func() {}, func(string) {}), mp: &esphome.MediaPlayer{}}

	p.stream.Play(srv.URL)
	if playing, _ := p.Playing(); !playing {
		t.Fatal("the station is not playing to begin with")
	}

	p.External()

	if playing, paused := p.Playing(); playing || paused {
		t.Errorf("the station outlived the remote starting: playing %v paused %v", playing, paused)
	}
	if !p.remote.playing() {
		t.Error("the remote did not take the speaker")
	}
}
