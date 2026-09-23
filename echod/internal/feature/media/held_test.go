package media

import (
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
)

// A remote's track paused from here stays on the screen, paused, after the remote lets it go - Music
// Assistant ends its stream on a pause and clears the track - and play on it asks for it back rather
// than going to a connection that will ignore it.
func TestAPausedRemoteTrackIsHeldAndResumed(t *testing.T) {
	p := &Player{stream: &Stream{}, mp: &esphome.MediaPlayer{}}
	p.remoteLast.Store(true)
	resumed, passed := 0, 0
	p.OnResumeRemote.Listen(func(struct{}) { resumed++ })
	p.OnTransport.Listen(func(Transport) { passed++ })

	p.extTrack.Store(remoteTrack{Title: "I Am a Stone", Artist: "Demon Hunter"})
	p.HoldRemote()
	p.extTrack.Store(remoteTrack{}) // the server clears the track
	p.remoteState.Store("stopped")

	title, _, _, ok := p.Held()
	if !ok || title != "I Am a Stone" {
		t.Fatalf("held = %q %v, want the track paused from here", title, ok)
	}
	if playing, paused := p.ScreenState(); playing || !paused {
		t.Errorf("screen state playing %v paused %v, want paused", playing, paused)
	}

	p.Transport(TransportToggle)
	if resumed != 1 || passed != 0 {
		t.Errorf("play on a held track: resumed %d, passed on %d; want asked for back, not passed on", resumed, passed)
	}

	// Playing again ends the hold; so does a stop; and so does time.
	p.RemoteState("playing")
	if _, _, _, ok := p.Held(); ok {
		t.Error("still held once the remote said it was playing")
	}
	p.held.Store(heldTrack{remoteTrack: remoteTrack{Title: "x"}, at: time.Now()})
	p.Transport(TransportStop)
	if _, _, _, ok := p.Held(); ok {
		t.Error("still held after a stop")
	}
	p.held.Store(heldTrack{remoteTrack: remoteTrack{Title: "x"}, at: time.Now().Add(-stoppedFor - time.Second)})
	if _, _, _, ok := p.Held(); ok {
		t.Error("still held after stoppedFor")
	}
}
