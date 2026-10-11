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
// asked and answering as told: at once, or, with hold, once the test lets it (answer).
type sender struct {
	asked chan Transport
	hold  chan struct{}
	mu    sync.Mutex
	takes bool
}

func newSender(t *testing.T, name string, takes bool) *sender {
	return addSender(t, name, &sender{asked: make(chan Transport, 16), takes: takes})
}

// newSlowSender is a sender that answers each ask only when the test lets it, as a phone slow to answer
// does, or one that has gone until the ask runs out.
func newSlowSender(t *testing.T, name string, takes bool) *sender {
	s := addSender(t, name, &sender{asked: make(chan Transport, 16), hold: make(chan struct{}), takes: takes})
	t.Cleanup(func() { close(s.hold) })
	return s
}

func addSender(t *testing.T, name string, s *sender) *sender {
	ReceiverControl(name, func(tr Transport) func() bool {
		return func() bool {
			s.asked <- tr
			if s.hold != nil {
				<-s.hold
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			return s.takes
		}
	})
	t.Cleanup(func() { receiverControls.Delete(name) })
	return s
}

// answer lets a slow sender answer the ask it has.
func (s *sender) answer() { s.hold <- struct{}{} }

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

	// One after the other, as a finger presses them: a skip still waiting gives way to the next one
	// (senderQueue).
	p.Transport(TransportNext)
	if got := snd.next(t); got != TransportNext {
		t.Errorf("asked %v, want next", got)
	}
	p.Transport(TransportPrevious)
	if got := snd.next(t); got != TransportPrevious {
		t.Errorf("asked %v, want previous", got)
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
	stopped := p.stoppedAt.Load()
	p.ReceiverPaused(name, false)
	if playing, _ := s.Playing(); !playing {
		t.Fatal("the sender playing again did not play the track")
	}
	if p.stoppedAt.Load() == stopped {
		t.Error("the sender playing again left a stop from before it to end the track (stoppedFor)")
	}
	snd.none(t)
}

// A sender slow to answer is asked only the latest of what was pressed meanwhile: the last play or
// pause, which is where the track here already is, and the last skip, not the ten.
func TestASlowSenderIsAskedOnlyTheLatest(t *testing.T) {
	const name = "AirPlay (slow to answer)"
	snd := newSlowSender(t, name, true)
	p, s := receivedPlayer(name)

	p.Transport(TransportToggle)
	if got := snd.next(t); got != TransportPause {
		t.Fatalf("asked %v, want a pause", got)
	}
	for range 2 { // play, pause
		p.Transport(TransportToggle)
	}
	for range 10 {
		p.Transport(TransportNext)
	}
	if _, paused := s.Playing(); !paused {
		t.Fatal("the track here is not where the last press left it")
	}
	snd.answer()
	if got := snd.next(t); got != TransportPause {
		t.Fatalf("asked %v, want the last pause, not the play before it", got)
	}
	snd.answer()
	if got := snd.next(t); got != TransportNext {
		t.Fatalf("asked %v, want one next", got)
	}
	snd.answer()
	snd.none(t)
}

// A skip the other way takes back the one waiting, while the phone is slow to answer: next, next and
// previous, or next, previous and next, leave the phone on the song the first next went to.
func TestASkipTheOtherWayTakesBackTheOneWaiting(t *testing.T) {
	const name = "AirPlay (skips both ways)"
	snd := newSlowSender(t, name, true)
	p, _ := receivedPlayer(name)

	for _, presses := range [][]Transport{
		{TransportNext, TransportNext, TransportPrevious},
		{TransportNext, TransportPrevious, TransportNext},
	} {
		p.Transport(presses[0])
		if got := snd.next(t); got != TransportNext {
			t.Fatalf("asked %v, want next", got)
		}
		p.Transport(presses[1])
		p.Transport(presses[2])
		snd.answer()
		snd.none(t)
	}
}

// A skip never takes the place of a play waiting: a pause, then a play and a next pressed while the
// pause is under way, leave the phone playing the next song.
func TestAPlayWaitingIsNotOvertakenByASkip(t *testing.T) {
	const name = "AirPlay (a play then a skip)"
	snd := newSlowSender(t, name, true)
	p, _ := receivedPlayer(name)

	p.Transport(TransportToggle)
	if got := snd.next(t); got != TransportPause {
		t.Fatalf("asked %v, want a pause", got)
	}
	p.Transport(TransportToggle)
	p.Transport(TransportNext)
	for _, want := range []Transport{TransportPlay, TransportNext} {
		snd.answer()
		if got := snd.next(t); got != want {
			t.Fatalf("asked %v, want %v", got, want)
		}
	}
	snd.answer()
	snd.none(t)
}

// A pause the sender does not take leaves a track that was played again meanwhile: the phone slow to
// answer, the play here came before the pause was given up on.
func TestARefusedPauseLeavesATrackPlayedAgain(t *testing.T) {
	const name = "AirPlay (a play while the pause is asked)"
	snd := newSlowSender(t, name, false)
	p, s := receivedPlayer(name)

	p.Transport(TransportToggle)
	if got := snd.next(t); got != TransportPause {
		t.Fatalf("asked %v, want a pause", got)
	}
	p.Transport(TransportToggle)
	snd.answer()
	if got := snd.next(t); got != TransportPlay {
		t.Fatalf("asked %v, want a play", got)
	}
	snd.answer()
	snd.none(t)
	if playing, _ := s.Playing(); !playing || p.Receiving() != name {
		t.Error("the refused pause ended a track that was playing again")
	}
}

// A pause on a track already paused, from Home Assistant say, asks the sender nothing: it has paused,
// or been asked to, or has gone on to play somewhere else, which a pause from here would stop.
func TestAPauseOnAPausedTrackAsksNothing(t *testing.T) {
	const name = "AirPlay (paused already)"
	snd := newSender(t, name, false)
	p, s := receivedPlayer(name)

	p.ReceiverPaused(name, true)
	p.Pause()
	snd.none(t)
	if _, paused := s.Playing(); !paused || p.Receiving() != name {
		t.Error("a pause on a paused track did something to it")
	}
}

// A track left paused for a sender that has gone ends; one playing, or another receiver's, is left,
// and the sender is told nothing.
func TestATrackPausedForASenderThatHasGoneEnds(t *testing.T) {
	const name = "AirPlay (the phone has gone)"
	snd := newSender(t, name, true)
	p, s := receivedPlayer(name)

	p.ReceiverGone(name)
	if playing, _ := s.Playing(); !playing {
		t.Fatal("a playing track was ended")
	}
	p.Pause()
	snd.next(t)
	p.ReceiverGone("Spotify")
	if p.Receiving() != name {
		t.Fatal("another receiver's sender going ended the track")
	}
	p.ReceiverGone(name)
	if p.Receiving() != "" {
		t.Fatal("the paused track was left")
	}
	snd.none(t)
	p.ReceiverGone(name) // nothing loaded: nothing to do
}

// A paused track ended for a sender that has gone lets go of its source, which is how the receiver's
// audio (streaming's pump) hears the track is over.
func TestAPausedTrackEndedForAGoneSenderLetsGoOfItsSource(t *testing.T) {
	const name = "AirPlay (gone while paused)"
	newSender(t, name, true)
	s := NewStream(speaker.NewDriver(speaker.New()), speaker.New(), func() {}, func(string) {})
	p := &Player{stream: s}
	src := newQuietSrc()
	s.PlayPCM(name, src, speaker.Rate, 2)
	<-src.reads
	s.Pause()
	src.quiet <- struct{}{} // the paused sender sends nothing; the track waits on the pause
	p.ReceiverGone(name)
	select {
	case <-src.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the source was not let go of")
	}
	if s.Receiving() != "" {
		t.Error("the track was left")
	}
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
