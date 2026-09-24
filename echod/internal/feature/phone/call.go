package phone

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emiago/diago"
	"github.com/emiago/diago/audio"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

const (
	// callRate and callFrame are a phone call's audio: 20 ms of 8 kHz. An intercom call carries the
	// microphone's own 16 kHz, wideFrame to a frame, since both ends are this daemon.
	callRate  = 8000
	callFrame = callRate / 50
	wideFrame = 2 * callFrame

	// maxBehind is how far the speaker may fall behind the call before what is queued is dropped. The
	// network delivers in bursts and the speaker plays at its own pace; without a limit a call drifts
	// seconds late and people talk over each other.
	maxBehind = 300 * time.Millisecond
)

// talk carries a call's audio both ways until the call ends or ctx does: the microphones (after echo
// cancellation) to the far end, and the far end to the speaker. say is spoken audio to send as well, at
// 16 kHz, such as an announcement made while the call is up.
func talk(ctx context.Context, m *diago.DialogMedia, say <-chan []int16) error {
	var wprops, rprops diago.MediaProps
	w, err := m.AudioWriter(diago.WithAudioWriterMediaProps(&wprops))
	if err != nil {
		return err
	}
	enc, err := audio.NewPCMEncoderWriter(wprops.Codec.PayloadType, w)
	if err != nil {
		return err
	}
	r, err := m.AudioReader(diago.WithAudioReaderMediaProps(&rprops))
	if err != nil {
		return err
	}
	dec, err := audio.NewPCMDecoderReader(rprops.Codec.PayloadType, r)
	if err != nil {
		return err
	}
	slog.Info("phone: audio", "codec", wprops.Codec.Name, "local", wprops.Laddr, "remote", wprops.Raddr)
	return carry(ctx, enc, dec, false, say)
}

// carry is a call's audio both ways, whatever the call runs over: enc takes the microphones and dec
// gives the far end, as 16-bit little-endian samples, at 8 kHz or (wide) at 16 kHz.
func carry(ctx context.Context, enc io.Writer, dec io.Reader, wide bool, say <-chan []int16) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var st stats
	defer func() {
		slog.Info("phone: call audio", "sent", st.sent.Load(), "sent_dbfs", st.level(&st.sentSq, &st.sent),
			"received", st.received.Load(), "received_dbfs", st.level(&st.recvSq, &st.received))
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	safe.Go("phone: send", func() {
		defer wg.Done()
		defer cancel()
		if err := send(ctx, enc, wide, say, &st); err != nil && ctx.Err() == nil {
			slog.Warn("phone: sending audio", "err", err)
		}
	})
	safe.Go("phone: receive", func() {
		defer wg.Done()
		defer cancel()
		if err := receive(ctx, dec, wide, &st); err != nil && ctx.Err() == nil && !errors.Is(err, io.EOF) {
			slog.Warn("phone: receiving audio", "err", err)
		}
	})
	<-ctx.Done()
	wg.Wait()
	return nil
}

// send is the microphones, and anything to say, out to the call.
func send(ctx context.Context, enc io.Writer, wide bool, say <-chan []int16, st *stats) error {
	frames, stop := mic.Get().Listen("call")
	defer stop()

	d := newDown()
	frameLen := callFrame
	if wide {
		frameLen = wideFrame
	}
	var pending []int16 // 16 kHz still to say
	buf := make([]byte, 2*frameLen)
	var out []int16 // the call's rate, not yet sent as a whole frame
	for {
		select {
		case <-ctx.Done():
			return nil
		case s := <-say:
			pending = append(pending, s...)
			continue
		case f, ok := <-frames:
			if !ok {
				return errors.New("microphones stopped")
			}
			frame := append([]int16(nil), f...)
			// Something to say is mixed over the room rather than replacing it, so whoever answers a
			// call placed for help hears both the message and the room.
			if len(pending) > 0 {
				n := min(len(pending), len(frame))
				for i := 0; i < n; i++ {
					frame[i] = clamp(float64(frame[i]) + float64(pending[i]))
				}
				pending = pending[n:]
			}
			if wide {
				out = append(out, frame...)
			} else {
				out = append(out, d.run(frame)...)
			}
			for len(out) >= frameLen {
				for i := 0; i < frameLen; i++ {
					binary.LittleEndian.PutUint16(buf[2*i:], uint16(out[i]))
				}
				if _, err := enc.Write(buf); err != nil {
					return err
				}
				st.add(&st.sent, &st.sentSq, out[:frameLen])
				out = out[frameLen:]
			}
		}
	}
}

// receive is the far end, out of the speaker. It holds the speaker for the call; anything that takes it
// in the meantime (an announcement) has it until it is done, and then the call takes it back.
func receive(ctx context.Context, dec io.Reader, wide bool, st *stats) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	audioCh := make(chan []int16, 50)
	errCh := make(chan error, 1)
	safe.Go("phone: read", func() {
		u := newUp()
		buf := make([]byte, 2*wideFrame)
		for {
			n, err := dec.Read(buf)
			if n >= 2 {
				s := make([]int16, n/2)
				for i := range s {
					s[i] = int16(binary.LittleEndian.Uint16(buf[2*i:]))
				}
				st.add(&st.received, &st.recvSq, s)
				if !wide {
					s = u.run(s)
				}
				select {
				case audioCh <- s:
				default: // the speaker is behind; this frame is lost rather than everything after it late
				}
			}
			if err != nil {
				errCh <- err
				return
			}
			if ctx.Err() != nil {
				errCh <- nil
				return
			}
		}
	})

	sound := speaker.Sound()
	limit := int(maxBehind * speaker.Rate / time.Second)
	for ctx.Err() == nil {
		claim := sound.Claim("call", func(cctx context.Context, p *speaker.Player) error {
			for {
				select {
				case <-cctx.Done():
					return nil
				case <-ctx.Done():
					return nil
				case s := <-audioCh:
					if p.Queued() > limit {
						p.Drain()
					}
					p.PlayVoice(s)
				}
			}
		})
		select {
		case err := <-errCh:
			cancel()
			<-claim.Done()
			return err
		case <-claim.Done():
		}
		if ctx.Err() != nil {
			break
		}
		// Something else took the speaker. What arrives meanwhile is dropped, and the call takes the
		// speaker back once it is free.
		for ctx.Err() == nil && sound.Busy() {
			select {
			case <-audioCh:
			case err := <-errCh:
				return err
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	return nil
}

// stats counts a call's audio frames each way and their energy, for the log line at its end: a call
// where one side heard nothing says here which way the audio stopped.
type stats struct {
	sent, received atomic.Int64
	sentSq, recvSq atomic.Uint64 // mean square per frame, summed
}

func (st *stats) add(n *atomic.Int64, sq *atomic.Uint64, s []int16) {
	if len(s) == 0 {
		return
	}
	var sum float64
	for _, v := range s {
		sum += float64(v) * float64(v)
	}
	n.Add(1)
	sq.Add(uint64(sum / float64(len(s))))
}

func (st *stats) level(sq *atomic.Uint64, n *atomic.Int64) string {
	if n.Load() == 0 {
		return "none"
	}
	ms := float64(sq.Load()) / float64(n.Load())
	if ms < 1 {
		return "silent"
	}
	return fmt.Sprintf("%.0f", 10*math.Log10(ms/(32768*32768)))
}

// ringTone is one cycle of a device ringing: two short rising bursts and a pause, at 16 kHz.
func ringTone() []int16 {
	const rate = speaker.VoiceRate
	var out []int16
	burst := func(f1, f2 float64, ms int) {
		n := rate * ms / 1000
		for i := 0; i < n; i++ {
			t := float64(i) / rate
			env := math.Min(1, math.Min(float64(i), float64(n-i))/(rate/100))
			v := 0.5*math.Sin(2*math.Pi*f1*t) + 0.5*math.Sin(2*math.Pi*f2*t)
			out = append(out, int16(9000*env*v))
		}
	}
	rest := func(ms int) { out = append(out, make([]int16, rate*ms/1000)...) }
	burst(660, 880, 350)
	rest(150)
	burst(880, 1100, 350)
	rest(2000)
	return out
}

// ring sounds until ctx ends.
func ring(ctx context.Context) {
	tone := ringTone()
	claim := speaker.Sound().Claim("ringing", func(cctx context.Context, p *speaker.Player) error {
		for {
			p.PlayVoice(tone)
			for p.Queued() > 0 {
				select {
				case <-cctx.Done():
					p.Drain()
					return nil
				case <-ctx.Done():
					p.Drain()
					return nil
				case <-time.After(50 * time.Millisecond):
				}
			}
		}
	})
	<-ctx.Done()
	<-claim.Done()
}
