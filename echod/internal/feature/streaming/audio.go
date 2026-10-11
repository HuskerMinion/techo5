package streaming

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// What the receivers send, as they are told to send it: 16-bit stereo at 44.1 kHz, interleaved.
const (
	audioRate     = 44100
	audioChannels = 2
)

// setAside is how long a receiver must be quiet, after something else took the speaker from it, before
// what it sends next counts as asked for again: an app still playing to a speaker that was given
// something else does not take it straight back, as on any speaker; pausing and playing again does.
var setAside = 2 * time.Second

// play is what plays a receiver's audio; media.Get().PlayReceived, a variable for the tests.
var play = func(name string, src media.PCMSource, rate, channels int) {
	media.Get().PlayReceived(name, src, rate, channels)
}

// level is a gain on what a receiver sends that has to change in step with the audio, not with the
// moment it is told: Spotify's, which undoes librespot's own volume. What is already in the pipe, and
// what was read from it but not yet played, was scaled at the old volume, so a lower volume, whose gain
// is larger, waits until all of that has gone through: applied at once, it would multiply audio still
// at the old level into a clipped burst. A higher volume applies at once, since its smaller gain on the
// old audio only makes a moment of it quieter.
type level struct {
	mu      sync.Mutex
	read    int64   // bytes taken from the pipe so far
	through int64   // bytes of those played, or thrown away
	now     float64 // the gain in force
	next    float64 // the gain waiting for through to reach at
	at      int64
	waiting bool
}

func newLevel() *level { return &level{now: 1} }

// inFlight is a read's worth of audio that can be between the pipe and the count: taken by a read that
// has not reached fromPipe yet, it is in neither the pipe's queue nor read. A lower volume waits that
// much longer, which errs on the side of a moment too quiet.
const inFlight = 16384

// set takes a new gain, with queued bytes still in the pipe from before it.
func (l *level) set(g float64, queued int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if g <= l.now {
		l.now, l.waiting = g, false
		return
	}
	l.next, l.at, l.waiting = g, l.read+int64(max(queued, 0))+inFlight, true
}

// fromPipe counts n bytes read from the pipe.
func (l *level) fromPipe(n int) {
	if l == nil || n <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.read += int64(n)
}

// passed counts n bytes played or thrown away, and brings a waiting gain in once the audio from
// before it has all gone.
func (l *level) passed(n int) {
	if l == nil || n <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.through += int64(n)
	if l.waiting && l.through >= l.at {
		l.now, l.waiting = l.next, false
	}
}

func (l *level) gain() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.now
}

// pump plays what a receiver writes to r as the track named name: a track starts whenever audio arrives
// and none of the receiver's own is playing, and runs until the receiver goes quiet or something else
// takes the speaker. r stays open across tracks and across the program's restarts. lv, when there is
// one, scales every sample (Spotify's, which undoes librespot's own volume); nil leaves them as sent.
// starts, when there is one, counts the times the sender starts to play (AirPlay's).
func pump(ctx context.Context, name string, r *os.File, lv *level, starts *startCount) {
	buf := make([]byte, 16384)
	for ctx.Err() == nil {
		_ = r.SetReadDeadline(time.Time{})
		n, err := r.Read(buf)
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("streaming: reading a receiver's audio failed", "from", name, "err", err)
			}
			return
		}
		lv.fromPipe(n)
		if n == 0 {
			continue
		}
		src := &pipeSource{f: r, pending: append([]byte(nil), buf[:n]...), lv: lv, done: make(chan struct{})}
		slog.Info("streaming: audio arrived", "from", name)
		play(name, src, audioRate, audioChannels)
		select {
		case <-ctx.Done():
			src.Close()
			return
		case <-src.done:
		}
		// A track that ended because the receiver went quiet is simply over: what it sends next is a new
		// one. One that was taken from it (something else played, or it was stopped here, which not every
		// phone hears of) leaves the receiver still sending: that is set aside until it has been quiet a
		// moment, which a pause on the phone gives, or until the sender says it plays again. An iPhone
		// told to pause keeps its session for some seconds, and shairport-sync sends silence all that
		// time, so a play on it soon after a stop here came before any quiet, and was set aside with the
		// rest for as long as it played.
		if !src.wentQuiet() {
			at := starts.count()
			drain(ctx, r, buf, lv, func() bool { return starts.count() != at })
		}
	}
}

// startCount counts the times a receiver's sender has started to play: a session beginning, or
// going on after a pause. nil counts none.
type startCount struct{ n atomic.Uint64 }

func (c *startCount) count() uint64 {
	if c == nil {
		return 0
	}
	return c.n.Load()
}

func (c *startCount) start() {
	if c != nil {
		c.n.Add(1)
	}
}

// drain reads and drops what r sends until it has sent nothing for setAside, or anew says the sender
// started to play again. It reads no faster than the audio would play: librespot writes as fast as it
// is read, and drained flat out it would race through the listener's queue while the speaker plays
// something else.
func drain(ctx context.Context, r *os.File, buf []byte, lv *level, anew func() bool) {
	const bytesPerSecond = audioRate * audioChannels * 2
	start, read := time.Now(), int64(0)
	for ctx.Err() == nil && !anew() {
		switch ahead := time.Duration(read)*time.Second/bytesPerSecond - time.Since(start); {
		case ahead > 0:
			select {
			case <-ctx.Done():
				return
			case <-time.After(ahead):
			}
		case ahead < -time.Second:
			// Behind (the sender stalled): paced from now, rather than read flat out to catch up.
			start, read = time.Now(), 0
		}
		_ = r.SetReadDeadline(time.Now().Add(setAside))
		n, err := r.Read(buf)
		read += int64(n)
		lv.fromPipe(n)
		lv.passed(n) // thrown away
		if err != nil {
			if !errors.Is(err, os.ErrDeadlineExceeded) && ctx.Err() == nil {
				slog.Warn("streaming: reading a receiver's audio failed", "err", err)
			}
			return
		}
	}
}

// pipeSource is one track's view of a receiver's pipe: what was read before it started, then the pipe
// itself. Closing it ends the track's reads without closing the pipe, which the next track reads on.
type pipeSource struct {
	f       *os.File
	mu      sync.Mutex
	pending []byte
	closed  bool
	quiet   bool // the track's last read found the receiver quiet; a paused track reads on after one
	lv      *level
	once    sync.Once
	done    chan struct{}
}

func (s *pipeSource) Read(p []byte) (int, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, os.ErrClosed
	}
	// Scaled, a read hands on whole samples only, and a buffer too small for one gets nothing.
	if s.lv != nil && len(p) < 2 {
		s.mu.Unlock()
		return 0, nil
	}
	// A lone byte kept back by scale is half a sample: it leads the next read from the pipe rather
	// than being handed out alone, which scale would only keep back again.
	if len(s.pending) > 1 || (len(s.pending) == 1 && s.lv == nil) {
		n := copy(p, s.pending)
		s.pending = s.pending[n:]
		n = s.scale(p, n)
		s.mu.Unlock()
		return n, nil
	}
	lead := 0
	if len(s.pending) == 1 {
		p[0], s.pending, lead = s.pending[0], nil, 1
	}
	s.mu.Unlock()
	n, err := s.f.Read(p[lead:])
	s.lv.fromPipe(n)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		// Over while the read waited: what it took belongs to whatever reads the pipe next, and is
		// dropped rather than queued to a track that has ended.
		s.lv.passed(lead + max(n, 0))
		return 0, os.ErrClosed
	}
	s.quiet = errors.Is(err, os.ErrDeadlineExceeded)
	return s.scale(p, lead+n), err
}

// scale applies the gain to the n bytes read into p and says how many to hand on. Samples are two
// bytes, and a pipe may end a read between them, so an odd byte waits in pending for its other half
// whatever the gain is: one read out of step would put every sample after it out of step. Called with
// mu held.
func (s *pipeSource) scale(p []byte, n int) int {
	if s.lv == nil || n <= 0 {
		return n
	}
	if n%2 == 1 {
		n--
		s.pending = append([]byte{p[n]}, s.pending...)
	}
	// The gain as these bytes begin: one waiting for audio partway through them comes in after, which
	// leaves the newer audio in them a moment quiet rather than any older audio loud.
	g := s.lv.gain()
	if g != 1 {
		for i := 0; i < n; i += 2 {
			v := float64(int16(binary.LittleEndian.Uint16(p[i:]))) * g
			v = math.Max(math.Min(math.Round(v), math.MaxInt16), math.MinInt16)
			binary.LittleEndian.PutUint16(p[i:], uint16(int16(v)))
		}
	}
	s.lv.passed(n)
	return n
}

// wentQuiet is whether the track ended on the receiver going quiet, not by being closed.
func (s *pipeSource) wentQuiet() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.quiet
}

func (s *pipeSource) SetReadDeadline(t time.Time) error { return s.f.SetReadDeadline(t) }

func (s *pipeSource) Close() error {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		// What was read for this track and never played is gone with it.
		s.lv.passed(len(s.pending))
		s.pending = nil
		s.mu.Unlock()
		// A read waiting on the pipe is let go now, rather than whenever the receiver next writes.
		_ = s.f.SetReadDeadline(time.Now())
		close(s.done)
	})
	return nil
}
