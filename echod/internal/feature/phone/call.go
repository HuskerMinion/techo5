package phone

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emiago/diago"
	"github.com/emiago/diago/audio"

	"github.com/HuskerMinion/techo5/echod/internal/config"
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

	// farEndLoud is the mean square per sample, at int16 scale, over which a frame from the far end
	// counts as somebody talking: about -45 dBFS, well under speech on a call and over a line's hiss.
	farEndLoud = 3.2e-5 * 32768 * 32768

	// farEndHold is how long after a loud frame arrives the far end still counts as talking. The frame
	// plays up to maxBehind later, the room rings with it after that, and a word is reported a moment
	// after it is said: a word the far end said ends inside this.
	farEndHold = maxBehind + 500*time.Millisecond
)

// farEndAt is when the last loud frame came from the far end, as Unix nanoseconds; zero is never.
var farEndAt atomic.Int64

// farEndTalking reports whether the far end was loud in the last farEndHold: what comes out of the
// speaker then could be the far end saying the word, not somebody in the room.
func farEndTalking(now time.Time) bool {
	at := farEndAt.Load()
	return at != 0 && now.Sub(time.Unix(0, at)) < farEndHold
}

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
			var frame []int16
			frame, pending = uplink(append([]int16(nil), f...), pending, repliesPlaying.Load() > 0)
			if wide {
				out = append(out, frame...)
			} else {
				out = append(out, d.Run(frame)...)
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

// turnsListening counts turns listening here (Phone.TurnListening): while one does, the far end is held
// back from the room (playFarEnd).
var turnsListening atomic.Int32

// playFarEnd reports whether the far end's audio goes out of the speaker now. Not while a turn listens:
// with Wake word during calls on, the caller would otherwise be speaking into what the device sends Home
// Assistant, and hearing the chime, they would know when to.
func playFarEnd() bool { return turnsListening.Load() == 0 }

// repliesPlaying counts turns' replies playing here (Phone.ReplyStarted): a count, so the end of one
// that lands after the next has begun does not let the room back in under it.
var repliesPlaying atomic.Int32

// uplink is one frame of the room as it goes up the call, with what there is to say. That is mixed over
// the room rather than replacing it, so whoever answers a call placed for help hears both the message
// and the room. Not while a turn's reply plays here (alone): the room is then mostly the reply again,
// out of the speaker a moment later and not taken off by the echo canceller, which a turn stops from
// learning; mixed, the far end hears it twice, as an echo, and faintly even with the reply not sent.
// So the reply goes up alone, or silence if it is not being sent. It returns the frame and what is
// still to say.
func uplink(frame, pending []int16, alone bool) ([]int16, []int16) {
	if alone {
		clear(frame)
	}
	n := min(len(pending), len(frame))
	for i := 0; i < n; i++ {
		frame[i] = clamp(float64(frame[i]) + float64(pending[i]))
	}
	return frame, pending[n:]
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
				if st.add(&st.received, &st.recvSq, s) > farEndLoud {
					farEndAt.Store(time.Now().UnixNano())
				}
				if !wide {
					s = u.Run(s)
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
					if !playFarEnd() {
						continue // held back while a turn listens; dropped, as live audio has to be
					}
					if p.Queued() > limit {
						// A chime or a beep mixed over the call (the wake word's, during a call) has
						// made the queue long, not the network: draining would lose it, so this frame
						// is dropped instead, as the drain would have dropped it.
						if p.Overlaying() {
							continue
						}
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
		// Something else took the speaker. What arrives meanwhile is dropped, or with Hear the call
		// under the assistant on, played quieter under it (underlay), and the call takes the speaker
		// back once it is free.
		var under *underlay
		for ctx.Err() == nil && sound.Busy() {
			select {
			case s := <-audioCh:
				if !config.Get().Home.CallUnderlay || !playFarEnd() {
					continue
				}
				if under == nil {
					under = newUnderlay(speaker.Get(), limit)
				}
				under.play(s)
			case err := <-errCh:
				// The call is over: what is still queued of it under the other sound goes too.
				if under != nil {
					speaker.Get().DrainUnder()
				}
				return err
			case <-time.After(50 * time.Millisecond):
			}
		}
		// What is left of the underlay is behind the far end the call is about to play as itself.
		if under != nil {
			speaker.Get().DrainUnder()
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

func (st *stats) add(n *atomic.Int64, sq *atomic.Uint64, s []int16) float64 {
	if len(s) == 0 {
		return 0
	}
	var sum float64
	for _, v := range s {
		sum += float64(v) * float64(v)
	}
	ms := sum / float64(len(s))
	n.Add(1)
	sq.Add(uint64(ms))
	return ms
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

// RingSounds are the ways a call can ring, in the order the screen and Home Assistant offer them;
// the first is the one a device starts with.
var RingSounds = []string{"Old phone", "Chime"}

// RingSoundIndex is the saved call ring's place in RingSounds; one no longer offered reads as the first.
func RingSoundIndex() int {
	if i := slices.Index(RingSounds, config.Get().Home.RingSound); i >= 0 {
		return i
	}
	return 0
}

// ringTone is one cycle of the call ring chosen here.
func ringTone() []int16 {
	if RingSounds[RingSoundIndex()] == "Chime" {
		return chimeRing()
	}
	return bellRing()
}

// bellRing is built once: it is a lot of sums, and a ring that waited on them would take the speaker
// late.
var bellRing = sync.OnceValue(buildBellRing)

// chimeRing is one cycle of a chime ring: two short rising bursts and a pause, at 16 kHz.
func chimeRing() []int16 {
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

// buildBellRing makes one cycle of an old desk phone's ring: a clapper beating between two bells
// 20 times a second for two seconds, then four seconds quiet, at 16 kHz.
func buildBellRing() []int16 {
	const (
		rate   = speaker.VoiceRate
		strike = rate / 20 // the clapper hits a bell every 50 ms, the other bell in between
		on     = 2 * rate
		cycle  = 6 * rate
	)
	// Each bell is a struck metal dome: a few partials that are not harmonics, the higher ones dying first.
	type partial struct{ ratio, gain, decay float64 }
	partials := []partial{{1, 1, 0.15}, {2.76, 0.45, 0.07}, {4.9, 0.2, 0.03}}
	bells := []float64{1180, 1420}
	sum := make([]float64, cycle)
	for at, b := 0, 0; at < on; at, b = at+strike/2, 1-b {
		for _, pt := range partials {
			f := bells[b] * pt.ratio
			for i := 0; at+i < cycle && float64(i)/rate < 6*pt.decay; i++ {
				t := float64(i) / rate
				sum[at+i] += pt.gain * math.Exp(-t/pt.decay) * math.Sin(2*math.Pi*f*t)
			}
		}
	}
	peak := 0.0
	for _, v := range sum {
		peak = math.Max(peak, math.Abs(v))
	}
	out := make([]int16, cycle)
	for i, v := range sum {
		out[i] = int16(9000 * v / peak)
	}
	return out
}

// ring sounds one cycle of a call ring, from ringTone, over and over until ctx ends. The caller picks
// the tone where the call comes in, so the setting is not read from the ring's own goroutine.
//
// Anything that takes the speaker meanwhile (a turn's chime and reply over a ringing call, an
// announcement) has it until it is done, and then the ring takes it back, as a call's audio does
// (receive): a call left ringing in silence is one nobody knows is there.
func ring(ctx context.Context, tone []int16) {
	sound := speaker.Sound()
	for ctx.Err() == nil {
		claim := sound.Claim("ringing", func(cctx context.Context, p *speaker.Player) error {
			return ringCycles(ctx, cctx, p, tone)
		})
		select {
		case <-ctx.Done():
			<-claim.Done()
			return
		case <-claim.Done():
		}
		for ctx.Err() == nil && sound.Busy() {
			select {
			case <-ctx.Done():
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
}

// ringCycles plays the ring until the call ends (ctx) or the speaker is taken (cctx).
func ringCycles(ctx, cctx context.Context, p *speaker.Player, tone []int16) error {
	cycle := time.Duration(len(tone)) * time.Second / speaker.VoiceRate
	for {
		p.PlayVoice(tone)
		// With no playback device nothing is queued, and the cycle is waited out as though it had
		// played, rather than resampling the whole tone again every look.
		next := time.Now().Add(cycle)
		idle := p.Queued() == 0
		// Looked at every 50 ms even when nothing is queued: with no playback device nothing ever is,
		// and a ring that only looked while sound was queued never stopped.
		for {
			select {
			case <-cctx.Done():
				p.Drain()
				return nil
			case <-ctx.Done():
				p.Drain()
				return nil
			case <-time.After(50 * time.Millisecond):
			}
			if p.Queued() == 0 && (!idle || !time.Now().Before(next)) {
				break
			}
		}
	}
}
