//go:build linux

package btaudio

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/bluealsa"
	"github.com/HuskerMinion/techo5/echod/internal/lib/bluez"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// A phone's stream is read for as long as the phone is connected, whoever has the speaker: what it
// plays is known from what it sends (media.PhonePlays), and the speaker goes by that (choose). Its
// stream starting says nothing: a phone starts it for any app that opens its audio, playing or not,
// and holds it open after a pause, or while another phone plays. When only the phone with the
// speaker was read, a phone sending silence took the speaker from Music Assistant, and a phone
// playing again after another had taken the speaker from it went unheard.
//
// The stream is opened once and kept: bluez-alsa lets one client open it, keeps it open while the
// phone's audio is idle, and sends on it again when the phone plays (measured on an Echo Dot: no
// end of stream through a pause). The player gets what it sends through a phoneOut while the phone
// has the speaker.

// inStream is a connected phone's received stream, read by watch.
type inStream struct {
	item string
	pcm  bluealsa.PCM
	dev  bluez.Device
	stop context.CancelFunc
	// ended is the reading over, the stream closed: refresh opens it afresh rather than hand the
	// speaker a stream nothing reads any more.
	ended atomic.Bool

	mu    sync.Mutex
	out   *phoneOut // the player's, while the phone has the speaker
	plays media.PhonePlays
	since time.Time // when it started playing, zero while it is not
	fresh bool      // it started playing since the last look (look)
}

// look is what the speaker is chosen by: whether the phone is playing, since when, and whether it
// started since the last look, which is then taken.
func (s *inStream) look() heard {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := heard{item: s.item, plays: s.plays.Playing(), since: s.since, fresh: s.fresh}
	s.fresh = false
	return l
}

// watch opens p's stream and reads it until the phone goes (Close, or stop from refresh). When the
// stream does not open, refresh tries again at its next look.
func (f *Feature) watch(al *bluealsa.Client, p bluealsa.PCM, d bluez.Device) (*inStream, error) {
	src, err := al.OpenRead(p)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &inStream{item: media.BluetoothPrefix + displayName(d), pcm: p, dev: d, stop: cancel}
	stop := context.AfterFunc(ctx, func() { _ = src.Close() })
	safe.Go("bluetooth phone stream", func() {
		defer f.wake()
		defer stop()
		defer src.Close()
		err := s.read(ctx, src, 2*p.Channels, time.Now, f.wake)
		if err != nil && ctx.Err() == nil {
			slog.Info("bluetooth: a phone's stream ended", "device", displayName(d), "err", err)
		}
	})
	return s, nil
}

// readSource is what read reads: a phone's stream, raw S16_LE.
type readSource interface {
	Read([]byte) (int, error)
	SetReadDeadline(time.Time) error
}

// readWait bounds each read, so a phone that sends nothing is still heard to have stopped playing.
const readWait = time.Second

// read reads src, frames of frame bytes, until it ends or ctx is done: it follows whether the phone
// plays, calling changed when it starts or stops, and hands what it sends to the player while the
// phone has the speaker. Only whole frames are handed on; a frame split across reads is carried to
// the next. The player's source ends with the stream.
func (s *inStream) read(ctx context.Context, src readSource, frame int, now func() time.Time,
	changed func()) error {
	defer s.ended.Store(true)
	defer func() {
		s.mu.Lock()
		out := s.out
		s.out = nil
		s.mu.Unlock()
		if out != nil {
			out.end()
		}
	}()
	buf := make([]byte, 4096-4096%frame+frame)
	have := 0
	for {
		_ = src.SetReadDeadline(now().Add(readWait))
		n, err := src.Read(buf[have:])
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
			return err
		}
		have += n
		whole := have - have%frame
		at := now()
		s.mu.Lock()
		turned := s.plays.Hear(buf[:whole], at)
		if turned && s.plays.Playing() {
			s.since, s.fresh = at, true
		} else if turned {
			s.since, s.fresh = time.Time{}, false
		}
		out := s.out
		s.mu.Unlock()
		if out != nil && whole > 0 && !out.put(buf[:whole]) {
			s.mu.Lock()
			if s.out == out {
				s.out = nil // the player let it go
			}
			s.mu.Unlock()
		}
		have = copy(buf, buf[whole:have])
		if turned {
			changed()
		}
	}
}

// askedToPause is the phone asked to pause from here (pauseAway): once it stops, its music after
// that is heard starting again (media.PhonePlays.Asked).
func (s *inStream) askedToPause() {
	s.mu.Lock()
	s.plays.Asked(true)
	s.mu.Unlock()
}

// stale is whether s is not the stream to read p by any more: its reading ended (an error, the
// stream closed), or the phone came back with another format.
func (s *inStream) stale(p bluealsa.PCM) bool {
	return s.ended.Load() || s.pcm.Rate != p.Rate || s.pcm.Channels != p.Channels
}

// hand gives the player a new source on what the phone sends, the one it had let go.
func (s *inStream) hand() *phoneOut {
	out := newPhoneOut()
	s.mu.Lock()
	old := s.out
	s.out = out
	s.plays.Asked(false) // it has the speaker: not asked to pause any more
	s.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	return out
}

// phoneOut is what a phone sends, from its inStream to the player while the phone has the speaker:
// a media.PCMSource that ends when the stream does and that the player closes to let go.
type phoneOut struct {
	data  chan []byte
	done  chan struct{} // the player let go (Close)
	ended chan struct{} // the stream ended (end)

	closing, ending sync.Once

	mu       sync.Mutex
	deadline time.Time
	rest     []byte // what a read had no room for
}

// phoneOutQueue is how many reads wait for the player, about a second and a half of audio: it reads
// a phone even while its track is held up (media's receive), so more waiting is a player gone.
const phoneOutQueue = 64

func newPhoneOut() *phoneOut {
	return &phoneOut{
		data:  make(chan []byte, phoneOutQueue),
		done:  make(chan struct{}),
		ended: make(chan struct{}),
	}
}

// put queues b for the player, dropped if the player is that far behind, and says whether the
// player still has it.
func (o *phoneOut) put(b []byte) bool {
	select {
	case <-o.done:
		return false
	default:
	}
	select {
	case o.data <- append([]byte(nil), b...):
	default:
	}
	return true
}

// end is the stream ending: the player reads what is queued, then the end.
func (o *phoneOut) end() { o.ending.Do(func() { close(o.ended) }) }

func (o *phoneOut) Read(b []byte) (int, error) {
	o.mu.Lock()
	if len(o.rest) > 0 {
		n := copy(b, o.rest)
		o.rest = o.rest[n:]
		o.mu.Unlock()
		return n, nil
	}
	deadline := o.deadline
	o.mu.Unlock()
	// What has arrived is read before anything else is looked at: with a deadline already past, a
	// select would pick between the two at random.
	select {
	case c := <-o.data:
		return o.take(b, c), nil
	default:
	}
	var timeout <-chan time.Time
	if !deadline.IsZero() {
		t := time.NewTimer(time.Until(deadline))
		defer t.Stop()
		timeout = t.C
	}
	select {
	case c := <-o.data:
		return o.take(b, c), nil
	case <-o.done:
		return 0, os.ErrClosed
	case <-o.ended:
		select {
		case c := <-o.data:
			return o.take(b, c), nil
		default:
			return 0, io.EOF
		}
	case <-timeout:
		return 0, os.ErrDeadlineExceeded
	}
}

func (o *phoneOut) take(b, c []byte) int {
	n := copy(b, c)
	if n < len(c) {
		o.mu.Lock()
		o.rest = c[n:]
		o.mu.Unlock()
	}
	return n
}

func (o *phoneOut) SetReadDeadline(t time.Time) error {
	o.mu.Lock()
	o.deadline = t
	o.mu.Unlock()
	return nil
}

func (o *phoneOut) Close() error {
	o.closing.Do(func() { close(o.done) })
	return nil
}
