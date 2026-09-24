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

// Music Assistant clears the track just before it says it stopped, so the hold takes the last track
// it named.
func TestAHoldTakesTheLastTrackNamed(t *testing.T) {
	p := &Player{stream: &Stream{}, mp: &esphome.MediaPlayer{}}
	p.ExternalTrack("Africa", "Toto", "Toto IV")
	p.ExternalTrack("", "", "")
	p.HoldRemote()
	if title, artist, _, ok := p.Held(); !ok || title != "Africa" || artist != "Toto" {
		t.Errorf("held %q by %q (%v), want the last track named", title, artist, ok)
	}
}

// A stop on a remote's track paused from here reaches the remote as well as emptying the hold. The row
// means the music, and the queue is what a play would start again: dropping the hold alone left Music
// Assistant's queue waiting to be picked up, which is not what the person pressing stop asked for.
func TestAStopOnAHeldTrackAsksTheRemoteToo(t *testing.T) {
	p := &Player{stream: &Stream{}, mp: &esphome.MediaPlayer{}}
	p.remoteLast.Store(true)
	p.remote.take() // the session still holds the speaker
	var passed []Transport
	p.OnTransport.Listen(func(t Transport) { passed = append(passed, t) })

	p.extTrack.Store(remoteTrack{Title: "I Am a Stone"})
	p.HoldRemote()
	p.Transport(TransportStop)

	if len(passed) != 1 || passed[0] != TransportStop {
		t.Errorf("the remote was asked %v, want a stop", passed)
	}
	if _, _, _, ok := p.Held(); ok {
		t.Error("the hold outlived the stop")
	}
}

// And a remote that has gone is asked nothing: its listener went with the connection, so the hold is all
// that is left to drop.
func TestAStopOnAHeldTrackWithNoRemoteAsksNothing(t *testing.T) {
	p := &Player{stream: &Stream{}, mp: &esphome.MediaPlayer{}}
	var passed []Transport
	p.OnTransport.Listen(func(t Transport) { passed = append(passed, t) })

	p.extTrack.Store(remoteTrack{Title: "I Am a Stone"})
	p.HoldRemote()
	p.Transport(TransportStop)

	if len(passed) != 0 {
		t.Errorf("the remote was asked %v with no remote there", passed)
	}
	if _, _, _, ok := p.Held(); ok {
		t.Error("the hold outlived the stop")
	}
}

// A stop drops this player's hold before the remote is asked, not after it. A server that will only take
// a pause answers a stop by holding the track again for the screen, and dropping this player's hold
// afterwards threw that away: play on the page then had nowhere to go, and the track somebody had just
// stopped could not be picked up from Home Assistant either, because the hold is what play asks for the
// remote's queue back through.
func TestAStopDropsTheHoldBeforeItAsks(t *testing.T) {
	p := &Player{stream: &Stream{}, mp: &esphome.MediaPlayer{}}
	p.remoteLast.Store(true)

	p.extTrack.Store(remoteTrack{Title: "I Am a Stone", Artist: "Demon Hunter"})
	p.HoldRemote()
	// What asks does when the server lists pause and not stop: the track is held for the screen.
	p.OnTransport.Listen(func(Transport) { p.HoldRemote() })

	p.Transport(TransportStop)

	title, _, _, ok := p.Held()
	if !ok || title != "I Am a Stone" {
		t.Errorf("held = %q %v after a stop answered with a pause, want the track kept", title, ok)
	}
}
