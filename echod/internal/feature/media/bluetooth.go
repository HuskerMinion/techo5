package media

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
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
		stop := context.AfterFunc(ctx, func() { _ = src.Close() })
		defer stop()
		defer src.Close()

		err := m.receive(ctx, t, src, newToSpeaker(rate, channels))
		if err != nil && ctx.Err() == nil {
			slog.Warn("received audio ended", "from", name, "err", err)
		}
		// A phone that stopped sending is not a stream to put back on: it is somebody walking away.
		m.finished(t, false)
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

// receive reads what the remote sends and queues it, until the remote goes quiet or the track is
// replaced.
func (m *Stream) receive(ctx context.Context, t *track, src PCMSource, conv *toSpeaker) error {
	buf := make([]byte, chunk-chunk%(conv.channels*2))
	var carry []byte
	for {
		if err := m.wait(ctx); err != nil {
			return err
		}

		// Armed only around the read, as for a url: waiting for a reply to finish is not the remote
		// going quiet.
		_ = src.SetReadDeadline(time.Now().Add(receiveQuiet))
		n, err := src.Read(buf)
		if n > 0 {
			in := append(carry, buf[:n]...)
			whole := len(in) - len(in)%(conv.channels*2)
			m.queue(t, conv.run(in[:whole]))
			carry = append(carry[:0], in[whole:]...)
		}
		switch {
		case err == nil:
		case errors.Is(err, os.ErrDeadlineExceeded):
			slog.Info("received audio went quiet", "item", t.item)
			return nil
		case errors.Is(err, io.EOF), errors.Is(err, os.ErrClosed):
			return nil
		default:
			return err
		}
	}
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
