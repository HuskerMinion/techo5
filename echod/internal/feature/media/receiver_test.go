package media

import (
	"io"
	"os"
	"sync"
	"sync/atomic"
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

// A phone over Bluetooth told of a pause plays on from where it stopped, so what this end set aside to
// play first on resuming is dropped; an AirPlay track keeps it as before. (The speaker's queue itself
// needs a playback device; flush and keep are what empty it, and this is which one runs.)
func TestAPausedPhoneLeavesNoTailToReplay(t *testing.T) {
	for name, wantKept := range map[string]bool{
		BluetoothPrefix + "Phone (its tail dropped)": false,
		"AirPlay (its tail kept)":                    true,
	} {
		newSender(t, name, true)
		_, s := receivedPlayer(name)
		s.mu.Lock()
		s.rewind = make([]int16, speaker.Rate) // set aside by a reply that took the speaker
		s.mu.Unlock()

		s.Pause()
		s.mu.Lock()
		kept := len(s.rewind) > 0
		s.mu.Unlock()
		if kept != wantKept {
			t.Errorf("%s: kept a tail %v, want %v", name, kept, wantKept)
		}
		s.Stop()
	}
}

// flowSrc is a sender that keeps sending, a little at a time, counting the reads: music when loud,
// and else the silence a paused phone sends while it holds its stream open.
type flowSrc struct {
	reads  atomic.Int64
	closed chan struct{}
	once   sync.Once
	loud   bool
}

func (f *flowSrc) Read(b []byte) (int, error) {
	select {
	case <-f.closed:
		return 0, io.EOF
	case <-time.After(5 * time.Millisecond):
	}
	f.reads.Add(1)
	frame := []byte{0, 0, 0, 0}
	if f.loud {
		frame = []byte{0x00, 0x40, 0x00, 0x40} // 16384 a channel
	}
	return copy(b, frame), nil
}

func (f *flowSrc) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

func (f *flowSrc) SetReadDeadline(time.Time) error { return nil }

// A phone's track paused here goes on reading what the phone sends, and drops it, so a phone playing
// again does not overflow bluez-alsa before the track plays on; an AirPlay track waits unread.
func TestAPausedPhoneIsReadOn(t *testing.T) {
	for name, readsOn := range map[string]bool{
		BluetoothPrefix + "Phone (read on)": true,
		"AirPlay (left unread)":             false,
	} {
		newSender(t, name, true)
		s := NewStream(speaker.NewDriver(speaker.New()), speaker.New(), func() {}, func(string) {})
		src := &flowSrc{closed: make(chan struct{})}
		s.PlayPCM(name, src, speaker.Rate, 2)
		waitUntil(t, "a first read", func() bool { return src.reads.Load() > 0 })

		s.Pause()
		time.Sleep(50 * time.Millisecond) // a read under way when it paused ends
		before := src.reads.Load()
		time.Sleep(200 * time.Millisecond)
		if got := src.reads.Load() > before+5; got != readsOn {
			t.Errorf("%s: read on while paused %v, want %v", name, got, readsOn)
		}
		s.Stop()
	}
}

// After the phone took a pause, a phone that sends on without a break is taken not to have paused; one
// that stops, even briefly, did, and its playing again later is not counted against it.
func TestAPhoneSendingOnAfterAPause(t *testing.T) {
	at := time.Unix(1000, 0)
	step := 20 * time.Millisecond
	run := func(on *sendingOn, taken time.Time, from, to time.Duration, audio bool) bool {
		ignored := false
		for d := from; d <= to; d += step {
			ignored = on.heard(true, taken, at.Add(d), audio) || ignored
		}
		return ignored
	}

	var on sendingOn
	if run(&on, at, 0, sendingIgnored-step, true) {
		t.Error("taken as ignored before sendingIgnored had gone by")
	}
	if !run(&on, at, sendingIgnored, sendingIgnored+step, true) {
		t.Error("a phone sending on without a break was not taken as ignoring the pause")
	}

	on = sendingOn{}
	run(&on, at, 0, 300*time.Millisecond, true) // the last of what it had in flight
	if run(&on, at, 300*time.Millisecond+sendingBreak, 4*time.Second, true) {
		t.Error("a phone that stopped, then played again, was taken as ignoring the pause")
	}

	on = sendingOn{}
	run(&on, at, 0, 100*time.Millisecond, true)
	on.heard(true, at, at.Add(time.Second), false) // a read that waited out its deadline
	if run(&on, at, time.Second, 4*time.Second, true) {
		t.Error("a phone that went quiet was taken as ignoring the pause")
	}

	if on.heard(false, at, at.Add(5*time.Second), true) || !on.since.IsZero() {
		t.Error("playing again here did not start over")
	}

	// The pause waited behind another command, and the phone took it 3 s after the pause here: it plays
	// on until then, which is not ignoring it, and is measured from the take.
	on = sendingOn{}
	if run(&on, time.Time{}, 0, 3*time.Second, true) {
		t.Error("taken as ignored before the phone took the pause")
	}
	took := at.Add(3 * time.Second)
	if run(&on, took, 3*time.Second, 3*time.Second+sendingIgnored-step, true) {
		t.Error("taken as ignored before sendingIgnored had gone by since the phone took the pause")
	}
	if !run(&on, took, 3*time.Second+sendingIgnored, 3*time.Second+sendingIgnored+step, true) {
		t.Error("a phone sending on after it took the pause was not taken as ignoring it")
	}
	on = sendingOn{}
	run(&on, time.Time{}, 0, 3*time.Second, true)
	if run(&on, took, 3*time.Second+sendingBreak, 6*time.Second, true) {
		t.Error("a phone that stopped as it took the pause was taken as ignoring it")
	}
}

// A phone's track paused here ends when the phone plays on regardless; one whose phone stops, or
// sends silence while it holds its stream open, waits.
func TestAPhoneThatIgnoresAPauseEndsItsTrack(t *testing.T) {
	was := sendingIgnored
	sendingIgnored = 200 * time.Millisecond
	t.Cleanup(func() { sendingIgnored = was })

	name := BluetoothPrefix + "Phone (plays on)"
	newSender(t, name, true)
	s := NewStream(speaker.NewDriver(speaker.New()), speaker.New(), func() {}, func(string) {})
	p := &Player{stream: s}
	src := &flowSrc{closed: make(chan struct{}), loud: true}
	s.PlayPCM(name, src, speaker.Rate, 2)
	waitUntil(t, "a first read", func() bool { return src.reads.Load() > 0 })
	p.Pause()
	waitUntil(t, "the track to end", func() bool { return s.Receiving() == "" })

	name = BluetoothPrefix + "Phone (sends silence)"
	newSender(t, name, true)
	s = NewStream(speaker.NewDriver(speaker.New()), speaker.New(), func() {}, func(string) {})
	p = &Player{stream: s}
	src = &flowSrc{closed: make(chan struct{})}
	s.PlayPCM(name, src, speaker.Rate, 2)
	waitUntil(t, "a first read", func() bool { return src.reads.Load() > 0 })
	p.Pause()
	time.Sleep(400 * time.Millisecond)
	if s.Receiving() != name {
		t.Error("the track of a phone sending silence after the pause ended")
	}
	s.Stop()

	name = BluetoothPrefix + "Phone (stops)"
	newSender(t, name, true)
	s = NewStream(speaker.NewDriver(speaker.New()), speaker.New(), func() {}, func(string) {})
	p = &Player{stream: s}
	quiet := newQuietSrc()
	s.PlayPCM(name, quiet, speaker.Rate, 2)
	<-quiet.reads
	p.Pause()
	quiet.quiet <- struct{}{} // it stopped: the read waits out its deadline
	time.Sleep(300 * time.Millisecond)
	if s.Receiving() != name {
		t.Error("the track of a phone that took the pause ended")
	}
	s.Stop()
}

// A phone slow to take a pause plays on until it does: its track is not ended for that, and the time
// it is given to stop runs from when it took the pause.
func TestAPhoneSlowToTakeAPauseIsGivenItsTime(t *testing.T) {
	was := sendingIgnored
	sendingIgnored = 200 * time.Millisecond
	t.Cleanup(func() { sendingIgnored = was })

	name := BluetoothPrefix + "Phone (slow to take it)"
	snd := newSlowSender(t, name, true)
	s := NewStream(speaker.NewDriver(speaker.New()), speaker.New(), func() {}, func(string) {})
	p := &Player{stream: s}
	src := &flowSrc{closed: make(chan struct{}), loud: true}
	s.PlayPCM(name, src, speaker.Rate, 2)
	waitUntil(t, "a first read", func() bool { return src.reads.Load() > 0 })
	p.Pause()
	if got := snd.next(t); got != TransportPause {
		t.Fatalf("asked %v, want a pause", got)
	}
	time.Sleep(4 * sendingIgnored) // the phone plays on, the pause not reached it yet
	if s.Receiving() != name {
		t.Fatal("the track ended before the phone took the pause")
	}
	snd.answer() // it takes the pause, and still plays on
	waitUntil(t, "the track to end", func() bool { return s.Receiving() == "" })
}

// A sender that paused on its own and is heard playing again without saying so (senderPlaysAgain)
// plays again here, rather than waiting paused, and listeners are told; a pause from here stays, even
// once the phone says it paused.
func TestASenderSendingAgainAfterItsOwnPausePlays(t *testing.T) {
	const name = BluetoothPrefix + "Pixel (sends again)"
	snd := newSender(t, name, true)
	p, s := receivedPlayer(name)

	p.ReceiverPaused(name, true)
	p.senderPlaysAgain("Spotify")
	if _, paused := s.Playing(); !paused {
		t.Fatal("another sender sending played the track")
	}
	var told []string
	p.OnReceiverPlays.Listen(func(item string) { told = append(told, item) })
	stopped := p.stoppedAt.Load()
	p.senderPlaysAgain(name)
	if playing, _ := s.Playing(); !playing {
		t.Fatal("the sender sending again after its own pause left the track paused")
	}
	if len(told) != 1 || told[0] != name {
		t.Errorf("told %v, want the track's name once", told)
	}
	if p.stoppedAt.Load() == stopped {
		t.Error("the sender playing again left a stop from before it to end the track (stoppedFor)")
	}
	snd.none(t)

	p.Pause()
	if got := snd.next(t); got != TransportPause {
		t.Fatalf("asked %v, want a pause", got)
	}
	p.ReceiverPaused(name, true) // the phone saying it took the pause from here
	p.senderPlaysAgain(name)
	if _, paused := s.Playing(); !paused {
		t.Fatal("a pause from here was undone by the phone sending")
	}
	p.senderPlaysAgain(name) // nothing new: still the listener's pause
	if _, paused := s.Playing(); !paused {
		t.Fatal("a pause from here was undone by the phone sending")
	}
	if len(told) != 1 {
		t.Errorf("told %v, want nothing more for a pause from here", told)
	}
}

// A phone that paused on its own is taken as playing again only after sendingAgain of audible audio
// without a break: a click or a notification sound, which starts its stream too, is not music.
func TestAPhonePlaysAgainOnlyAfterSustainedAudio(t *testing.T) {
	t0 := time.Unix(1000, 0)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	var a playingAgain

	// A notification: a second of sound, then silence.
	for ms := 0; ms <= 1000; ms += 20 {
		if a.heard(true, at(ms), true) {
			t.Fatalf("a second of sound at %d ms was taken as playing", ms)
		}
	}
	for ms := 1020; ms <= 1600; ms += 20 {
		a.heard(true, at(ms), false)
	}
	// Music: sound without a break, past sendingAgain from its own start.
	start := 1620
	played := -1
	for ms := start; ms <= start+2000; ms += 20 {
		if a.heard(true, at(ms), true) {
			played = ms
			break
		}
	}
	if played < start+int(sendingAgain/time.Millisecond) {
		t.Fatalf("taken as playing at %d ms, want %v after %d", played, sendingAgain, start)
	}
	// Short quiet reads inside the music are not a break.
	var b playingAgain
	for ms := 0; ms <= 1600; ms += 20 {
		quiet := ms%200 == 100
		if b.heard(true, at(ms), !quiet) && ms < 1500 {
			t.Fatalf("taken as playing at %d ms", ms)
		}
	}
	if !b.heard(true, at(1620), true) {
		t.Error("music with short quiet reads in it was not taken as playing")
	}
	// Not paused by the phone: nothing is watched.
	var c playingAgain
	for ms := 0; ms <= 3000; ms += 20 {
		if c.heard(false, at(ms), true) {
			t.Fatal("a track not paused by the phone was taken as played again")
		}
	}
}

// A sender's late answer to an earlier pause is not taken as its answer to a later one: the pause
// here, a play and another pause while the first was being asked, and the first answered then.
func TestALateAnswerIsNotALaterPausesAnswer(t *testing.T) {
	const name = BluetoothPrefix + "Pixel (slow to answer)"
	snd := newSlowSender(t, name, true)
	p, s := receivedPlayer(name)
	taken := func() time.Time {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.pauseTaken
	}

	p.Transport(TransportToggle)
	if got := snd.next(t); got != TransportPause {
		t.Fatalf("asked %v, want a pause", got)
	}
	p.Transport(TransportToggle) // play, waiting
	p.Transport(TransportToggle) // pause, in its place
	snd.answer()                 // the first pause, answered late
	if got := snd.next(t); got != TransportPause {
		t.Fatalf("asked %v, want the second pause", got)
	}
	if !taken().IsZero() {
		t.Fatal("the first pause's answer was taken as the second's")
	}
	snd.answer()
	waitUntil(t, "the second pause to be taken", func() bool { return !taken().IsZero() })
}

// A phone whose stream starts while something else plays is heard as music only after sendingAgain of
// audible audio without a break: half a minute of silence from an app that opened the phone's audio is
// not, nor is a sound of a second and the silence after it.
func TestMusicStartsOnlyAfterSustainedAudio(t *testing.T) {
	t0 := time.Unix(1000, 0)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	read := func(level int16) []byte {
		pcm := make([]byte, 0, 2*64)
		for range 64 {
			pcm = append(pcm, byte(uint16(level)), byte(uint16(level)>>8))
		}
		return pcm
	}
	var s MusicStarts

	// An app that opened the phone's audio and plays nothing.
	for ms := 0; ms <= 30000; ms += 20 {
		if s.Heard(read(0), at(ms)) {
			t.Fatalf("silence was taken as music at %d ms", ms)
		}
	}
	// A second of sound, then silence.
	for ms := 30020; ms <= 34000; ms += 20 {
		level := int16(0)
		if ms <= 31020 {
			level = -4000
		}
		if s.Heard(read(level), at(ms)) {
			t.Fatalf("a sound of a second was taken as music at %d ms", ms)
		}
	}
	// Silence just under the floor is still silence.
	for ms := 34020; ms <= 36000; ms += 20 {
		if s.Heard(read(audibleFloor), at(ms)) {
			t.Fatalf("near silence was taken as music at %d ms", ms)
		}
	}
	// Music: past sendingAgain from its own start.
	start := 36020
	played := -1
	for ms := start; ms <= start+2000; ms += 20 {
		if s.Heard(read(3000), at(ms)) {
			played = ms
			break
		}
	}
	if played < start+int(sendingAgain/time.Millisecond) {
		t.Fatalf("taken as music at %d ms, want %v after %d", played, sendingAgain, start)
	}
}
