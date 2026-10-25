package phone

import (
	"context"
	"io"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emiago/diago"
	"github.com/emiago/diago/audio"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// What a call being placed sounds like until it is answered. Without it the device was silent until the
// far end picked up: a call that rang for half a minute seemed dead, and an announcement from the far
// end's network ("the number you have dialed is not available") was never heard, only a call that went
// nowhere. Now the far end's network is heard whenever it sends audio before the answer (early media),
// and when it only says the far end is ringing, the device plays a ringing tone of its own.

// progress is the sound of one call being placed: nothing until the network says something, then the
// tone or the network's own audio, until stop.
type progress struct {
	ctx context.Context

	mu     sync.Mutex
	cancel context.CancelFunc // ends what plays now; nil while nothing does
	done   chan struct{}      // closed once what played has let go of the speaker
	early  bool               // what plays is the network's own audio, which a tone never replaces
}

// Seams for the tests: what plays the ringing tone, and the network's audio, until ctx ends.
var (
	playRingback = func(ctx context.Context) { ring(ctx, ringbackTone()) }
	playEarly    = playEarlyMedia
)

func newProgress(ctx context.Context) *progress { return &progress{ctx: ctx} }

// ringing is the far end ringing with nothing to hear: the device's own ringing tone, unless something
// already plays.
func (g *progress) ringing() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cancel != nil {
		return
	}
	g.startLocked(func(ctx context.Context) { playRingback(ctx) })
}

// earlyMedia is the network's own audio before the answer: it takes over from the tone, if that plays.
func (g *progress) earlyMedia(m *diago.DialogMedia) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.early {
		return
	}
	g.stopLocked()
	g.early = true
	g.startLocked(func(ctx context.Context) { playEarly(ctx, m) })
}

// stop ends what plays and waits for it to let go of the speaker, for the call's own audio to take it.
func (g *progress) stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stopLocked()
}

func (g *progress) startLocked(play func(context.Context)) {
	ctx, cancel := context.WithCancel(g.ctx)
	done := make(chan struct{})
	g.cancel, g.done = cancel, done
	go func() {
		defer close(done)
		play(ctx)
	}()
}

func (g *progress) stopLocked() {
	if g.cancel == nil {
		return
	}
	g.cancel()
	<-g.done
	g.cancel, g.done = nil, nil
}

// playEarlyMedia plays the network's audio before the answer, as a call's far end is played (receive),
// until ctx ends. The reader is the call's own, and the call's audio (talk) reads on from it once this
// returns, so the read is ended for sure before then (endRead), and the moment ctx ends: left to stop
// at the next packet, it would still be waiting in a read when talk opened the same reader, or take
// the first packet after the answer and drop it, which is the start of whatever the far end said
// first.
//
// It sends silence the other way meanwhile. The device's firewall lets a call's audio in only once the
// device has sent some itself (and so does a router's NAT on the way to a provider), which a call does
// from the microphones the moment it is answered; before that nothing had gone out, and the network's
// ringing was dropped at the device.
func playEarlyMedia(ctx context.Context, m *diago.DialogMedia) {
	var rprops, wprops diago.MediaProps
	r, err := m.AudioReader(diago.WithAudioReaderMediaProps(&rprops))
	if err != nil {
		slog.Warn("phone: early media", "err", err)
		return
	}
	read := newEndRead(r, func() { stopRTPRead(m) }, func() { startRTPRead(m) })
	stopEnding := context.AfterFunc(ctx, read.end)
	defer func() {
		stopEnding()
		read.end() // waits for the one ctx started, if it did
	}()
	dec, err := audio.NewPCMDecoderReader(rprops.Codec.PayloadType, read)
	if err != nil {
		slog.Warn("phone: early media", "err", err)
		return
	}
	slog.Info("phone: early media", "codec", rprops.Codec.Name)

	var wg sync.WaitGroup
	if w, err := m.AudioWriter(diago.WithAudioWriterMediaProps(&wprops)); err != nil {
		slog.Warn("phone: early media: no way to send", "err", err)
	} else if enc, err := audio.NewPCMEncoderWriter(wprops.Codec.PayloadType, w); err != nil {
		slog.Warn("phone: early media: no way to send", "err", err)
	} else {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sendSilence(ctx, enc, int(wprops.Codec.SampleRate))
		}()
	}
	var st stats
	_ = receive(ctx, dec, false, &st)
	wg.Wait() // the writer is the call's own too: done with before the call's audio writes to it
}

// stopRTPRead makes a read of the call's audio under way return now, and startRTPRead lets reads wait
// for packets again. Seams for the tests, which have no media session.
var (
	stopRTPRead = func(m *diago.DialogMedia) {
		if s := m.MediaSession(); s != nil {
			_ = s.StopRTP(1, 0)
		}
	}
	startRTPRead = func(m *diago.DialogMedia) {
		if s := m.MediaSession(); s != nil {
			_ = s.StartRTP(1)
		}
	}
)

// endRead is a reader that can be ended for sure: once end returns, no read is under way and none
// starts, and the reader underneath reads normally again for whoever reads it next.
type endRead struct {
	r              io.Reader
	interrupt, run func()

	mu    sync.Mutex // held for the length of each read
	ended atomic.Bool
	once  sync.Once
}

func newEndRead(r io.Reader, interrupt, run func()) *endRead {
	return &endRead{r: r, interrupt: interrupt, run: run}
}

func (e *endRead) Read(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ended.Load() {
		return 0, io.EOF
	}
	return e.r.Read(p)
}

// end interrupts a read under way, waits for it to return, and lets the reader underneath read again.
// A read that checks ended just before it is set still meets the interrupt, which is in place before
// end waits. A second end, at the same time or after, returns once the first is done.
func (e *endRead) end() {
	e.once.Do(func() {
		e.ended.Store(true)
		e.interrupt()
		e.mu.Lock()
		defer e.mu.Unlock()
		e.run()
	})
}

// sendSilence writes 20 ms of silence to w every 20 ms until ctx ends, at rate samples a second (8000
// for the narrowband codecs a call uses).
func sendSilence(ctx context.Context, w io.Writer, rate int) {
	if rate <= 0 {
		rate = 8000
	}
	frame := make([]byte, 2*rate/50)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if _, err := w.Write(frame); err != nil {
				if ctx.Err() == nil {
					slog.Warn("phone: early media: sending", "err", err)
				}
				return
			}
		}
	}
}

// ringbackTone is one cycle of the tone a caller hears while the far end rings, as European networks
// play it: 425 Hz for a second, then four seconds quiet, at the speaker's voice rate, its edges softened
// so it does not click.
var ringbackTone = sync.OnceValue(func() []int16 {
	const (
		rate = speaker.VoiceRate
		on   = rate
		edge = rate / 100 // 10 ms in and out
	)
	out := make([]int16, 5*rate)
	for i := 0; i < on; i++ {
		env := math.Min(1, math.Min(float64(i), float64(on-i))/edge)
		out[i] = int16(5000 * env * math.Sin(2*math.Pi*425*float64(i)/rate))
	}
	return out
})
