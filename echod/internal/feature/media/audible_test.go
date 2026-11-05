package media

import "testing"

// The stop word and the action button stop what the room hears. A remote's track (Music Assistant over
// Sendspin) never goes through this player's stream, so it counts only by what the remote says, and it
// is paused by asking the remote, as the screen does; a station of this device's is paused here.
func TestTheStopWordReachesARemotesTrack(t *testing.T) {
	for _, tc := range []struct {
		name       string
		claimed    bool
		remoteSays string
		local      string // "", "playing" or "paused"
		audible    bool
		asked      bool // the pause goes to the remote
	}{
		{"nothing at all", false, "", "", false, false},
		{"a station of this device's", false, "", "playing", true, false},
		{"a remote playing", true, "playing", "", true, true},
		{"a remote paused, holding the speaker", true, "paused", "", false, true},
		{"a remote paused over a station playing", true, "paused", "playing", true, false},
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
			var sent []Transport
			p.OnTransport.Listen(func(t Transport) { sent = append(sent, t) })

			if got := p.Audible(); got != tc.audible {
				t.Errorf("Audible() = %v, want %v", got, tc.audible)
			}
			if !tc.audible {
				return
			}
			p.PauseAudible()
			if tc.asked {
				if len(sent) != 1 || sent[0] != TransportPause {
					t.Errorf("the remote was asked %v, want a pause", sent)
				}
				return
			}
			if len(sent) != 0 {
				t.Errorf("the remote was asked %v for this player's own track", sent)
			}
			if playing, _ := s.Playing(); playing {
				t.Error("this player's own track still plays")
			}
		})
	}
}
