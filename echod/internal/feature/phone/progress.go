package phone

import (
	"context"
	"io"
	"log/slog"
	"math"
	"sync"
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
// until ctx ends. The reader is the call's own: once ctx ends it stops reading at the next packet, and
// the call's audio (talk) reads on from there.
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
	dec, err := audio.NewPCMDecoderReader(rprops.Codec.PayloadType, r)
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
