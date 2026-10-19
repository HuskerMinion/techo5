package media

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// PCMSource is raw interleaved S16_LE arriving from somewhere else: a phone playing to the device over
// Bluetooth, through bluez-alsa. Close ends a read that is waiting.
type PCMSource interface {
	io.ReadCloser
	SetReadDeadline(time.Time) error
}

// BluetoothPrefix starts the name a phone's audio over Bluetooth is played under, before the phone's own
// name (feature/btaudio), so whoever names what plays can tell it from the other received tracks.
const BluetoothPrefix = "Bluetooth: "

// keepWhileHeld names the received tracks whose audio is kept, not dropped, when it arrives while
// the track is held up (KeepWhileHeld).
var keepWhileHeld sync.Map

// KeepWhileHeld marks the received track named name as one whose audio is never dropped for arriving
// while it is paused or a reply has the speaker: the video's (feature/video), whose sound is its
// picture's clock. Bluetooth, AirPlay, Spotify Connect and DLNA music keep the old way.
func KeepWhileHeld(name string) { keepWhileHeld.Store(name, true) }

func keepsWhileHeld(name string) bool {
	_, ok := keepWhileHeld.Load(name)
	return ok
}

// offeredHook, when set, hears how each offer of received audio went; tests wait on it.
var offeredHook atomic.Pointer[func(item string, ok bool)]

// offered is offer, for received audio, told to offeredHook.
func (m *Stream) offered(t *track, samples []int16) bool {
	ok := m.offer(t, samples)
	if h := offeredHook.Load(); h != nil {
		(*h)(t.item, ok)
	}
	return ok
}

// receiveQuiet is how long a remote may send nothing before its track ends. A phone that is paused
// stops sending; ending the track lets Home Assistant's media, or the next thing asked for, play, and
// the phone takes the speaker again the moment it starts once more.
const receiveQuiet = 5 * time.Second

// PlayPCM plays audio a remote is sending, replacing whatever was playing: the last thing started
// wins, as on any speaker. Everything else about it is a track, so a turn ducks or pauses it, a reply
// waits it out, and Home Assistant sees the speaker playing.
func (m *Stream) PlayPCM(name string, src PCMSource, rate, channels int) {
	if m == nil {
		_ = src.Close()
		return
	}
	if rate <= 0 || channels < 1 || channels > 2 {
		slog.Warn("cannot play received audio", "from", name, "rate", rate, "channels", channels)
		_ = src.Close()
		return
	}

	t, ctx := m.start(&track{item: name, received: true})
	slog.Info("playing received audio", "from", name, "rate", rate, "channels", channels)

	safe.Go("received audio", func() {
		// A phone that stopped sending is not a stream to put back on: it is somebody walking away.
		// Deferred, so a source that panics (a decoder fed a lying stream) still lets the speaker go.
		defer m.finished(t, false)
		stop := context.AfterFunc(ctx, func() { _ = src.Close() })
		defer stop()
		defer src.Close()

		err := m.receive(ctx, t, src, newToSpeaker(rate, channels))
		if err != nil && ctx.Err() == nil {
			slog.Warn("received audio ended", "from", name, "err", err)
		}
	})
}

// Receiving is the name of what is being played from a remote, empty when the track is not one.
func (m *Stream) Receiving() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.track == nil || !m.track.received {
		return ""
	}
	return m.track.item
}

// pausedOn is whether t is the track loaded and the listener has it paused, and when its sender took
// that pause: zero until it has (pauseTook).
func (m *Stream) pausedOn(t *track) (paused bool, taken time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.track != t || !m.paused {
		return false, time.Time{}
	}
	return true, m.pauseTaken
}

// senderPausedOn is whether t is the track loaded and its sender paused it on its own (ReceiverPaused).
func (m *Stream) senderPausedOn(t *track) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.track == t && m.paused && m.bySender
}

// SenderPaused is whether the track loaded is a received one its sender paused on its own
// (ReceiverPaused), rather than one paused from here.
func (m *Stream) SenderPaused() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.track != nil && m.track.received && m.paused && m.bySender
}

// pauseSeq is which pause is in effect, or was last (pauses), for pauseTook to tell its answer by.
func (m *Stream) pauseSeq() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pauses
}

// pauseTook is the sender of the received track named name taking the listener's pause seq (pauseSeq
// as it was asked), now. Only the first word of it counts, and only while that track is loaded and
// paused by that same pause: an answer to an earlier pause, late behind a play and another pause, is
// not this one's.
func (m *Stream) pauseTook(name string, seq uint64, now time.Time) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.track != nil && m.track.received && m.track.item == name && m.paused && m.pauses == seq &&
		m.pauseTaken.IsZero() {
		m.pauseTaken = now
	}
}

// receive reads what the remote sends and queues it, until the remote goes quiet or the track is
// replaced.
func (m *Stream) receive(ctx context.Context, t *track, src PCMSource, conv *toSpeaker) error {
	buf := make([]byte, chunk-chunk%(conv.channels*2))
	var carry []byte
	phone := strings.HasPrefix(t.item, BluetoothPrefix)
	var on sendingOn
	var again playingAgain
	for {
		// A phone is read on while the track is held up, and what it sends goes (offered): left unread,
		// what a phone that plays again sends piles up in bluez-alsa until it overflows, and the track
		// plays again with a piece of it missing, half a second in. The same goes for a hold that is not
		// a pause (a reply, a timer, an announcement): the phone plays on through it, so what it sends
		// meanwhile is dropped and the music comes back where the phone has got to, as live audio does.
		// Left unread instead (as before), it came back late by however much bluez-alsa held, after a
		// gap where the rest overflowed, and stayed that far behind the phone for the rest of the track.
		if !phone || !m.held() {
			if err := m.wait(ctx); err != nil {
				return err
			}
		}

		// Armed only around the read, as for a url: waiting for a reply to finish is not the remote
		// going quiet.
		_ = src.SetReadDeadline(time.Now().Add(receiveQuiet))
		n, err := src.Read(buf)
		var samples []int16
		in, whole := carry, 0
		if n > 0 {
			in = append(carry, buf[:n]...)
			whole = len(in) - len(in)%(conv.channels*2)
			samples = conv.run(in[:whole])
		}
		if paused, taken := m.pausedOn(t); phone && receiverControl(t.item) != nil &&
			on.heard(paused, taken, time.Now(), audible(samples)) {
			// It took the pause and went on playing: the wrong player was asked, or an app that does not
			// listen. Paused here it would play to nobody for good; it ends, as for a pause not taken.
			if m.stopPaused(t.item) {
				slog.Info("a phone went on playing after it took a pause; its track here ends", "item", t.item)
				return nil
			}
		}
		if phone && m.senderPlays != nil && again.heard(m.senderPausedOn(t), time.Now(), audible(samples)) {
			m.senderPlays(t.item)
		}
		if n > 0 {
			if keepsWhileHeld(t.item) {
				// Held up between the read and the queue (a pause, or a reply taking the speaker), what was
				// read waits for the track to go on rather than being dropped: a video's picture follows
				// its sound (Stream.Heard), and dropped, the sound would run ahead of it.
				for !m.offered(t, samples) {
					if err := m.wait(ctx); err != nil {
						return err
					}
				}
			} else {
				// Every other received track as it always was: what arrives while it is held up goes.
				m.offered(t, samples)
			}
			carry = append(carry[:0], in[whole:]...)
		}
		switch {
		case err == nil:
		case errors.Is(err, os.ErrDeadlineExceeded):
			// A sender paused along with the track sends nothing until it plays again (ReceiverControl):
			// the track waits for it, paused, rather than ending, until its receiver says the sender has
			// gone (ReceiverGone).
			if paused, _ := m.pausedOn(t); receiverControl(t.item) != nil && paused {
				continue
			}
			slog.Info("received audio went quiet", "item", t.item)
			return nil
		case errors.Is(err, io.EOF), errors.Is(err, os.ErrClosed):
			return nil
		default:
			return err
		}
	}
}

// sendingBreak is the shortest break in what a phone sends that says it stopped: it sends a packet
// every few tens of milliseconds while it plays. sendingIgnored is how long after the phone took a
// pause one that sends on without such a break is taken not to have paused; a variable for the tests.
const sendingBreak = 500 * time.Millisecond

var sendingIgnored = 2 * time.Second

// audibleFloor is the level a sample has to reach to be the phone playing, about -42 dBFS: a phone
// that paused holds its stream open for a few seconds sending silence before it lets it go.
const audibleFloor = 256

// audible is whether samples carry anything over audibleFloor.
func audible(samples []int16) bool {
	for _, v := range samples {
		if v > audibleFloor || v < -audibleFloor {
			return true
		}
	}
	return false
}

// sendingOn watches a phone's track paused here for the phone going on playing as if it had not
// paused. One that paused stops within moments, or sends silence until it lets its stream go (a Pixel
// does for three seconds or so), and anything audible after a break is it playing again, which its
// Status follows. It is measured from when the phone took the pause, not from the pause here: the
// pause waits its turn behind whatever the phone was asked before (senderQueue) and can take seconds
// to reach it, and a phone plays on until it does. A pause the phone never takes ends the track
// anyway (tellSender), and one from the phone itself (ReceiverPaused) is not doubted.
type sendingOn struct {
	paused time.Time // the pause, as first seen here
	since  time.Time // the phone taking it, zero until it has
	last   time.Time // the last audio since the pause
	broke  bool      // a break since the phone took it: the phone did stop
}

// heard takes one read, at now, audible or not, while the track is paused here or not, with when the
// phone took the pause (zero until it has), and says whether the phone has played on without a break
// for sendingIgnored since it took it.
func (s *sendingOn) heard(paused bool, taken, now time.Time, audio bool) bool {
	if !paused {
		*s = sendingOn{}
		return false
	}
	if s.paused.IsZero() {
		s.paused, s.last = now, now
	}
	if taken.IsZero() {
		if audio {
			s.last = now
		}
		return false
	}
	if !s.since.Equal(taken) {
		s.since, s.broke = taken, false
	}
	if !audio || now.Sub(s.last) >= sendingBreak {
		s.broke = true
	}
	if audio {
		s.last = now
	}
	return !s.broke && now.Sub(s.since) >= sendingIgnored
}

// sendingAgain is how long a phone that paused on its own has to send audible audio, without a break
// of sendingBreak, to be taken as playing again: longer than the sounds a phone sends between songs
// with its music paused (an iPhone's keyboard clicks and notification sounds go over A2DP too, and
// start its stream), shorter than anyone waits for music they started. A variable for the tests.
var sendingAgain = 1500 * time.Millisecond

// playingAgain watches a phone's track the phone paused on its own for the phone playing again
// without its Status saying so: sendingAgain of audible audio with no break of sendingBreak in it. A
// pause from here is not watched: only a play from the phone says it means to play here again.
type playingAgain struct {
	since time.Time // the start of the audible run, zero with none
	last  time.Time // the last audible read in it
}

// heard takes one read, at now, audible or not, while the track is paused by the phone or not, and
// says whether the phone has now played for sendingAgain without a break.
func (a *playingAgain) heard(senderPaused bool, now time.Time, audio bool) bool {
	if !senderPaused {
		*a = playingAgain{}
		return false
	}
	if !audio {
		if !a.last.IsZero() && now.Sub(a.last) >= sendingBreak {
			*a = playingAgain{}
		}
		return false
	}
	if a.since.IsZero() || now.Sub(a.last) >= sendingBreak {
		a.since = now
	}
	a.last = now
	return now.Sub(a.since) >= sendingAgain
}

// PhonePlays follows what a phone sends for whether it is playing music. Its stream says nothing of
// that: a phone starts it for any app that opens its audio, playing or not (a browser coming back
// to the screen sends nothing but silence, for seconds or for half a minute), and holds it open for
// seconds after a pause, or for as long as it likes. It starts playing with sendingAgain of audible
// audio with no break of sendingBreak, as a phone that paused on its own plays again
// (playingAgain), and stops with receiveQuiet of nothing audible, as its track here would end.
type PhonePlays struct {
	again   playingAgain
	last    time.Time // the last audible read while it plays
	on      bool
	asked   bool      // asked to pause from here (Asked)
	askedAt time.Time // the first read after the ask, zero until then
}

// Asked says whether the phone has been asked to pause from here. For receiveQuiet after that, a
// break of sendingBreak with nothing audible ends its play, as a phone that takes the pause stops
// within moments, so its music after that is heard starting again: played again moments later, a
// phone whose play had not yet ended went on playing to nobody. One that plays on through those
// seconds ignored the pause, and the gaps between its songs are not taken for it stopping.
func (p *PhonePlays) Asked(pause bool) { p.asked, p.askedAt = pause, time.Time{} }

// Hear takes one read of what the phone sends, raw S16_LE, none for a read that waited for nothing,
// at now, and says whether the phone started or stopped playing with it.
func (p *PhonePlays) Hear(pcm []byte, now time.Time) (changed bool) {
	audio := audibleS16(pcm)
	if p.asked && p.askedAt.IsZero() {
		p.askedAt = now // measured on the reader's clock, which the ask is not read on
	}
	if p.on {
		quiet := now.Sub(p.last)
		if audio {
			p.last = now
			if p.asked && now.Sub(p.askedAt) >= receiveQuiet {
				p.asked = false // it played on through the pause
			}
		} else if quiet >= receiveQuiet || (p.asked && quiet >= sendingBreak) {
			*p = PhonePlays{}
			return true
		}
		return false
	}
	if p.again.heard(true, now, audio) {
		*p = PhonePlays{on: true, last: now}
		return true
	}
	return false
}

// Playing is whether the phone is playing music, as last heard.
func (p *PhonePlays) Playing() bool { return p.on }

// audibleS16 is audible for raw S16_LE.
func audibleS16(pcm []byte) bool {
	for i := 0; i+1 < len(pcm); i += 2 {
		if v := int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8); v > audibleFloor || v < -audibleFloor {
			return true
		}
	}
	return false
}

// toSpeaker turns what a remote sends (mono or stereo, at its own rate: phones pick 44.1 or 48 kHz)
// into the speaker's interleaved stereo at its rate. Linear interpolation, carried across calls so a
// stream read in pieces comes out continuous.
type toSpeaker struct {
	channels int
	step     float64 // input frames per output frame
	pos      float64 // where the next output frame falls between prev and the next input frame
	prevL    int16
	prevR    int16
	primed   bool
}

func newToSpeaker(rate, channels int) *toSpeaker {
	return &toSpeaker{channels: channels, step: float64(rate) / speaker.Rate}
}

// run converts whole input frames. The result is freshly allocated, since queue scales it in place.
func (c *toSpeaker) run(pcm []byte) []int16 {
	frames := len(pcm) / (c.channels * 2)
	if c.step == 1 && c.channels == speaker.Channels {
		out := make([]int16, frames*speaker.Channels)
		for i := range out {
			out[i] = int16(uint16(pcm[i*2]) | uint16(pcm[i*2+1])<<8)
		}
		return out
	}

	out := make([]int16, 0, int(float64(frames)/c.step+2)*speaker.Channels)
	for f := 0; f < frames; f++ {
		o := f * c.channels * 2
		l := int16(uint16(pcm[o]) | uint16(pcm[o+1])<<8)
		r := l
		if c.channels == 2 {
			r = int16(uint16(pcm[o+2]) | uint16(pcm[o+3])<<8)
		}
		if !c.primed {
			c.prevL, c.prevR, c.primed = l, r, true
			continue
		}
		for c.pos < 1 {
			out = append(out,
				int16(float64(c.prevL)+(float64(l)-float64(c.prevL))*c.pos),
				int16(float64(c.prevR)+(float64(r)-float64(c.prevR))*c.pos))
			c.pos += c.step
		}
		c.pos -= 1
		c.prevL, c.prevR = l, r
	}
	return out
}
