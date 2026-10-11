package media

import (
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// sender is a received track's sender the device can reach (ReceiverControl), recording what it is
// asked and answering as told.
type sender struct {
	asked chan Transport
	mu    sync.Mutex
	takes bool
}

func newSender(t *testing.T, name string, takes bool) *sender {
	s := &sender{asked: make(chan Transport, 16), takes: takes}
	ReceiverControl(name, func(tr Transport) bool {
		s.asked <- tr
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.takes
	})
	t.Cleanup(func() { receiverControls.Delete(name) })
	return s
}

func (s *sender) next(t *testing.T) Transport {
	t.Helper()
	select {
	case tr := <-s.asked:
		return tr
	case <-time.After(5 * time.Second):
		t.Fatal("the sender was asked nothing")
		return 0
	}
}

func (s *sender) none(t *testing.T) {
	t.Helper()
	select {
	case tr := <-s.asked:
		t.Fatalf("the sender was asked %v", tr)
	case <-time.After(50 * time.Millisecond):
	}
}

// receivedPlayer is a player with a received track named name loaded and playing.
func receivedPlayer(name string) (*Player, *Stream) {
	s := NewStream(speaker.NewDriver(speaker.New()), speaker.New(), func() {}, func(string) {})
	s.start(&track{item: name, received: true})
	return &Player{stream: s}, s
}

// A pause on the device reaches a sender it can: the phone is told, and the track stays here, paused,
// with play on it, rather than being stopped. Play and a skip go to the phone the same way.
func TestAPauseReachesTheSender(t *testing.T) {
	const name = "AirPlay (a pause reaches it)"
	snd := newSender(t, name, true)
	p, s := receivedPlayer(name)

	p.Transport(TransportToggle)
	if got := snd.next(t); got != TransportPause {
		t.Fatalf("asked %v, want a pause", got)
	}
	if _, paused := s.Playing(); !paused || p.Receiving() != name {
		t.Fatal("the track was not left paused")
	}
	snd.none(t)

	p.Transport(TransportToggle)
	if got := snd.next(t); got != TransportPlay {
		t.Fatalf("asked %v, want a play", got)
	}
	if playing, _ := s.Playing(); !playing {
		t.Fatal("the track did not play again")
	}

	p.Transport(TransportNext)
	p.Transport(TransportPrevious)
	if a, b := snd.next(t), snd.next(t); a != TransportNext || b != TransportPrevious {
		t.Errorf("asked %v then %v, want next then previous", a, b)
	}
	if playing, _ := s.Playing(); !playing {
		t.Error("a skip did something to the track here")
	}

	// A stop here pauses the phone as well: it would play on to a speaker that drops what it sends.
	p.Transport(TransportStop)
	if got := snd.next(t); got != TransportPause {
		t.Errorf("a stop asked %v, want a pause", got)
	}
	if p.Receiving() != "" {
		t.Error("the stop left the track")
	}
}

// A pause the sender does not take ends the track, as for a sender no pause reaches: held, it would
// hold the sender up while the phone plays on.
func TestAPauseTheSenderDoesNotTakeStopsTheTrack(t *testing.T) {
	const name = "AirPlay (the phone is gone)"
	snd := newSender(t, name, false)
	p, _ := receivedPlayer(name)

	p.Pause()
	snd.next(t)
	waitUntil(t, "the track to stop", func() bool { return p.Receiving() == "" })
}

// The sender pausing or playing again on its own is followed here, and not told back to it; a track
// by another name is left alone.
func TestASenderPausingOnItsOwnIsNotToldBack(t *testing.T) {
	const name = "AirPlay (the phone paused)"
	snd := newSender(t, name, true)
	p, s := receivedPlayer(name)

	p.ReceiverPaused("Spotify", true)
	if playing, _ := s.Playing(); !playing {
		t.Fatal("another sender's pause paused the track")
	}
	p.ReceiverPaused(name, true)
	if _, paused := s.Playing(); !paused {
		t.Fatal("the sender's pause did not pause the track")
	}
	p.ReceiverPaused(name, false)
	if playing, _ := s.Playing(); !playing {
		t.Fatal("the sender playing again did not play the track")
	}
	snd.none(t)
}

// Without a way to the sender, a skip has nothing to go to and changes nothing.
func TestASkipWithNoSenderToAskDoesNothing(t *testing.T) {
	p, s := receivedPlayer("Bluetooth (no way back)")
	p.Transport(TransportNext)
	if playing, _ := s.Playing(); !playing || p.Receiving() == "" {
		t.Error("a skip with nowhere to go changed the track")
	}
}

// A paused track whose sender took the pause waits through the quiet that follows, however long,
// and reads on when it plays again. Any other received track that goes quiet ends, paused or not.
func TestAPausedTrackWaitsForItsSender(t *testing.T) {
	for _, tc := range []struct {
		name  string
		waits bool
	}{{"AirPlay (waits)", true}, {"Bluetooth (ends)", false}} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.waits {
				newSender(t, tc.name, true)
			}
			s := NewStream(speaker.NewDriver(speaker.New()), speaker.New(), func() {}, func(string) {})
			src := newQuietSrc()
			s.PlayPCM(tc.name, src, speaker.Rate, 2)
			<-src.reads // the first read, waiting
			s.Pause()
			src.quiet <- struct{}{} // the sender, paused, sends nothing for as long as a read waits
			if !tc.waits {
				waitUntil(t, "the track to end", func() bool { return s.Receiving() == "" })
				return
			}
			// The quiet is taken in, and the track waits on the pause, before the sender plays again.
			time.Sleep(100 * time.Millisecond)
			if s.Receiving() != tc.name {
				t.Fatal("the paused track ended on the quiet")
			}
			s.Unpause()
			select {
			case <-src.reads:
			case <-time.After(5 * time.Second):
				t.Fatal("the track did not read on after the quiet")
			}
			if s.Receiving() != tc.name {
				t.Error("the paused track ended on the quiet")
			}
			s.Stop()
		})
	}
}

// quietSrc is a received track's source whose reads wait until the test says the sender was quiet for
// the whole of one, or the source is closed.
type quietSrc struct {
	reads  chan struct{}
	quiet  chan struct{}
	closed chan struct{}
	once   sync.Once
}

func newQuietSrc() *quietSrc {
	return &quietSrc{reads: make(chan struct{}, 16), quiet: make(chan struct{}), closed: make(chan struct{})}
}

func (q *quietSrc) Read([]byte) (int, error) {
	q.reads <- struct{}{}
	select {
	case <-q.quiet:
		return 0, os.ErrDeadlineExceeded
	case <-q.closed:
		return 0, io.EOF
	}
}

func (q *quietSrc) Close() error {
	q.once.Do(func() { close(q.closed) })
	return nil
}

func (q *quietSrc) SetReadDeadline(time.Time) error { return nil }
