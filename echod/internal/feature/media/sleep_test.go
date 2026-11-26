package media

import (
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// When the sleep timer fires during a remote (Music Assistant / Sendspin) stream, it must route
// through Transport(TransportStop) so OnTransport carries stop to the remote instead of only
// stopping the local stream.
func TestSleepTimerStopsRemoteAndLocalStreams(t *testing.T) {
	newTestPlayer := func() *Player {
		d := speaker.NewDriver(speaker.New())
		return &Player{stream: NewStream(d, speaker.New(), func() {}, func(string) {}), mp: &esphome.MediaPlayer{}}
	}

	t.Run("remote playing", func(t *testing.T) {
		p := newTestPlayer()
		p.remoteLast.Store(true)
		s := newSleeper()
		s.player = p

		got := make(chan Transport, 1)
		p.OnTransport.Listen(func(tr Transport) { got <- tr })
		p.RemoteState("playing")

		s.Set(10 * time.Millisecond)
		select {
		case tr := <-got:
			if tr != TransportStop {
				t.Fatalf("sleep timer emitted %v, want TransportStop", tr)
			}
		case <-time.After(time.Second):
			t.Fatal("sleep timer did not emit TransportStop for a remote stream")
		}
	})

	t.Run("remote held", func(t *testing.T) {
		p := newTestPlayer()
		p.remoteLast.Store(true)
		s := newSleeper()
		s.player = p

		got := make(chan Transport, 1)
		p.OnTransport.Listen(func(tr Transport) { got <- tr })
		p.ExternalTrack("Take It Easy", "Eagles", "YES 933")
		p.RemoteState("playing")
		p.HoldRemote()
		p.RemoteState("stopped")

		s.Set(10 * time.Millisecond)
		select {
		case tr := <-got:
			if tr != TransportStop {
				t.Fatalf("sleep timer emitted %v for held track, want TransportStop", tr)
			}
		case <-time.After(time.Second):
			t.Fatal("sleep timer did not emit TransportStop for a held remote track")
		}
		if _, _, _, ok := p.Held(); ok {
			t.Error("held track was not cleared when sleep timer fired")
		}
	})

	t.Run("local stream", func(t *testing.T) {
		p := newTestPlayer()
		s := newSleeper()
		s.player = p

		stopped := make(chan struct{}, 1)
		p.OnStop.Listen(func(struct{}) { stopped <- struct{}{} })

		s.Set(10 * time.Millisecond)
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Fatal("sleep timer did not stop local player")
		}
	})
}
