package media

import (
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// Only a definite stop moves StopCount: a play, and a server's gap between tracks, do not.
func TestStopCount(t *testing.T) {
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
	if p.StopCount() != 3 {
		t.Errorf("Home Assistant's stop: count %d, want 3", p.StopCount())
	}
}
