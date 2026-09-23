package speaker

import (
	"encoding/binary"
	"log/slog"
	"math"
	"sync/atomic"
	"syscall"
)

// A Sink is somewhere other than the codec to send what the speaker would have played: Bluetooth
// earbuds through bluez-alsa. It takes interleaved S16_LE at its own rate and channel count.
//
// While a sink is attached the codec keeps getting silence at its own pace, which is what keeps the
// write loop's clock and stops the amplifier hissing; the sink gets the same frames, converted, and
// is expected to drain them at nominally the same rate. A write that would block is dropped rather
// than stalling the loop: two clocks drift, and a dropped period every few hours is the cheapest
// way to let them.
type Sink interface {
	Write([]byte) (int, error)
	// Pause stops the transport while nothing is playing and Resume starts it again. A Bluetooth
	// stream that carries silence keeps the radio busy, and it shares the antenna with Wi-Fi:
	// measured on the bench, an idle A2DP stream held open dropped the Wi-Fi rate to 6.5 Mbit/s.
	Pause() error
	Resume() error
	Close() error
}

// sinkQuietAfter is how many silent periods before the transport is paused: about two seconds,
// so the gaps in speech and between a chime and a reply do not pump it.
const sinkQuietAfter = 2 * Rate / period

type sinkState struct {
	sink     Sink
	rate     int
	channels int
	name     string

	// quiet counts silent periods in a row; paused is whether the transport is stopped for it.
	quiet  int
	paused bool

	// Linear interpolation state for a rate that is not the codec's.
	pos    float64
	lastL  int16
	lastR  int16
	out    []byte
	frames []int16

	dropped atomic.Uint64
}

// SetSink routes playback to s until it is cleared with nil. The previous sink, if any, is closed.
// name is for the log. rate and channels are what s expects.
func (p *Player) SetSink(s Sink, name string, rate, channels int) {
	var st *sinkState
	if s != nil {
		if channels < 1 || channels > 2 || rate <= 0 {
			slog.Warn("audio sink refused", "name", name, "rate", rate, "channels", channels)
			_ = s.Close()
			return
		}
		st = &sinkState{sink: s, rate: rate, channels: channels, name: name}
	}
	old := p.sink.Swap(st)
	if old != nil {
		_ = old.sink.Close()
		slog.Info("audio sink detached", "name", old.name, "dropped", old.dropped.Load())
	}
	if st != nil {
		slog.Info("audio sink attached", "name", name, "rate", rate, "channels", channels)
	}
	// The curve changes with the route.
	p.SetVolume(p.Step())
}

// Sinking reports the name of the attached sink, empty for none.
func (p *Player) Sinking() string {
	if s := p.sink.Load(); s != nil {
		return s.name
	}
	return ""
}

// sinkGain is a plain curve for a sink: it has its own amplifier, so nothing here is calibrated to
// a driver. Linear in dB from -40 dB at the first step to full scale at the top; the bottom is off.
func sinkGain(step int) float32 {
	if step <= 0 {
		return 0
	}
	db := (float64(step)/VolumeSteps - 1) * 40
	return float32(math.Pow(10, db/20))
}

// push converts one period (48 kHz stereo S16_LE, already at the sink's gain) and writes it.
// Silence is counted rather than sent: after sinkQuietAfter periods of it the transport is
// paused, and the next period with anything in it resumes it first.
func (s *sinkState) push(buf []byte) {
	n := len(buf) / 4
	if cap(s.frames) < n*2 {
		s.frames = make([]int16, n*2)
	}
	frames := s.frames[:n*2]
	silent := true
	for i := range frames {
		frames[i] = int16(binary.LittleEndian.Uint16(buf[i*2:]))
		if frames[i] != 0 {
			silent = false
		}
	}
	if silent {
		s.quiet++
		if s.quiet >= sinkQuietAfter && !s.paused {
			if err := s.sink.Pause(); err != nil {
				slog.Warn("audio sink pause", "name", s.name, "err", err)
			} else {
				s.paused = true
				slog.Info("audio sink paused", "name", s.name)
			}
		}
		if s.paused {
			return
		}
	} else {
		s.quiet = 0
		if s.paused {
			if err := s.sink.Resume(); err != nil {
				slog.Warn("audio sink resume", "name", s.name, "err", err)
			} else {
				slog.Info("audio sink resumed", "name", s.name)
			}
			s.paused = false
		}
	}

	out := s.out[:0]
	if s.rate == Rate {
		out = s.appendFrames(out, frames, 0, n)
	} else {
		// Walk the input at the ratio, interpolating between neighbors; the last frame of the
		// previous period is the left neighbor of the first.
		step := float64(Rate) / float64(s.rate)
		for s.pos < float64(n) {
			i := int(s.pos)
			f := s.pos - float64(i)
			l0, r0 := s.lastL, s.lastR
			if i > 0 {
				l0, r0 = frames[(i-1)*2], frames[(i-1)*2+1]
			}
			l1, r1 := frames[i*2], frames[i*2+1]
			l := int16(float64(l0)*(1-f) + float64(l1)*f)
			r := int16(float64(r0)*(1-f) + float64(r1)*f)
			out = s.appendFrame(out, l, r)
			s.pos += step
		}
		s.pos -= float64(n)
		s.lastL, s.lastR = frames[(n-1)*2], frames[(n-1)*2+1]
	}
	s.out = out

	if _, err := s.sink.Write(out); err != nil {
		if err == syscall.EAGAIN || errIsAgain(err) {
			if d := s.dropped.Add(1); d == 1 || d%1000 == 0 {
				slog.Warn("audio sink not keeping up", "name", s.name, "dropped", d)
			}
			return
		}
		slog.Warn("audio sink write failed", "name", s.name, "err", err)
	}
}

func (s *sinkState) appendFrames(out []byte, frames []int16, from, to int) []byte {
	for i := from; i < to; i++ {
		out = s.appendFrame(out, frames[i*2], frames[i*2+1])
	}
	return out
}

func (s *sinkState) appendFrame(out []byte, l, r int16) []byte {
	if s.channels == 1 {
		m := int16((int32(l) + int32(r)) / 2)
		return binary.LittleEndian.AppendUint16(out, uint16(m))
	}
	out = binary.LittleEndian.AppendUint16(out, uint16(l))
	return binary.LittleEndian.AppendUint16(out, uint16(r))
}

func errIsAgain(err error) bool {
	type temporary interface{ Temporary() bool }
	t, ok := err.(temporary)
	return ok && t.Temporary()
}
