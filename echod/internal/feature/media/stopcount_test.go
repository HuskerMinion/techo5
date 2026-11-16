package media

import (
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// Only a definite stop moves StopCount: a play, and a server's gap between tracks, do not.
func TestStopCount(t *testing.T) {
	was := stopSettle
	stopSettle = 100 * time.Millisecond
	defer func() { stopSettle = was }()
	d := speaker.NewDriver(speaker.New())
	p := &Player{stream: NewStream(d, speaker.New(), func() {}, func(string) {}), mp: &esphome.MediaPlayer{}}
	p.RemoteState("playing")
	p.Transport(TransportToggle)
	if p.StopCount() != 0 {
		t.Fatal("a play or a toggle counted as a stop")
	}
	p.RemoteState("stopped")
	p.RemoteState("playing") // the gap while the server switches track
	time.Sleep(stopSettle + 300*time.Millisecond)
	if p.StopCount() != 0 {
		t.Error("a gap between tracks counted as a stop")
	}
	p.RemoteState("stopped")
	time.Sleep(stopSettle + 300*time.Millisecond)
	if p.StopCount() != 1 {
		t.Errorf("a server that stayed stopped: count %d, want 1", p.StopCount())
	}
	p.Transport(TransportStop)
	if p.StopCount() != 2 {
		t.Errorf("the Stop button: count %d, want 2", p.StopCount())
	}
	p.command(esphome.MediaCommand{HasCommand: true, Command: esphome.MediaPlayerStop})
	if p.StopCount() != 2 {
		t.Errorf("Home Assistant's stop with nothing playing or held: count %d, want 2", p.StopCount())
	}
	p.held.Store(heldTrack{remoteTrack: remoteTrack{Title: "x"}, at: time.Now()})
	p.command(esphome.MediaCommand{HasCommand: true, Command: esphome.MediaPlayerStop})
	if p.StopCount() != 3 {
		t.Errorf("Home Assistant's stop of a held track: count %d, want 3", p.StopCount())
	}
	p.StopByUser()
	if p.StopCount() != 4 {
		t.Errorf("a stop by the user: count %d, want 4", p.StopCount())
	}
	time.Sleep(stopSettle + 100*time.Millisecond)
}

// A play asked for while a server's "stopped" is settling cancels that stop: the server was only slow to
// say "playing".
func TestAPlayWhileAStopSettlesIsNotAStop(t *testing.T) {
	was := stopSettle
	stopSettle = 100 * time.Millisecond
	defer func() { stopSettle = was }()
	d := speaker.NewDriver(speaker.New())
	p := &Player{stream: NewStream(d, speaker.New(), func() {}, func(string) {}), mp: &esphome.MediaPlayer{}}
	p.RemoteState("stopped")
	p.Transport(TransportPlay)
	time.Sleep(stopSettle + 200*time.Millisecond)
	if p.StopCount() != 0 {
		t.Errorf("a play within the settling time left the stop counted: %d", p.StopCount())
	}
	p.RemoteState("stopped")
	p.ours()
	time.Sleep(stopSettle + 200*time.Millisecond)
	if p.StopCount() != 0 {
		t.Errorf("a track started within the settling time left the stop counted: %d", p.StopCount())
	}
}
