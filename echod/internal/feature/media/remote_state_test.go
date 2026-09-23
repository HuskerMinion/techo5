package media

import "testing"

// A carried stream is what the remote last said it was doing, and playing until it has said anything.
// A remote that said it stopped is not playing, however long it goes on holding the speaker: that is
// what left the screen and Home Assistant saying "playing" after a pause and a stop from the app.
func TestCarriedStateIsWhatTheRemoteSaid(t *testing.T) {
	p := &Player{}
	for _, c := range []struct {
		said            string
		playing, paused bool
	}{
		{"", true, false},
		{"playing", true, false},
		{"paused", false, true},
		{"stopped", false, false},
	} {
		p.remoteState.Store(c.said)
		if playing, paused := p.CarriedState(); playing != c.playing || paused != c.paused {
			t.Errorf("said %q: playing %v paused %v, want %v %v", c.said, playing, paused, c.playing, c.paused)
		}
	}

	// The next session starts from nothing, not from what the last one said.
	p.remoteState.Store("paused")
	p.RemoteGone()
	if playing, paused := p.CarriedState(); !playing || paused {
		t.Errorf("after the remote went: playing %v paused %v, want the first stream taken as playing", playing, paused)
	}
}
