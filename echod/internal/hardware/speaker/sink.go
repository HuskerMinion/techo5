package speaker

import (
	"encoding/binary"
	"log/slog"
	"math"
	"sync/atomic"
	"syscall"
	"time"
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

// A LevelSink is a Sink with a volume of its own: a Bluetooth speaker that takes its volume from this
// side (AVRCP absolute volume), as it takes a phone's. While Level says it takes it, the sink is told
// the step the output plays at and sent the audio at full scale, rather than the audio being turned
// down here and the speaker left at whatever level it had: a speaker the last phone left at 70% then
// never got louder than that. Level is called for every period, from the write loop, so it must not
// block.
type LevelSink interface {
	Sink
	Level(step int) bool
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

	// sounding is when sound began going to the sink after it was quiet (unix nanoseconds), 0 while
	// it is quiet: none yet, or paused for silence. Read by SinkSounding from outside the write loop.
	sounding atomic.Int64
	// onSound is told when sound begins going to the sink after it was quiet (OnSinkSound).
	onSound func()
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
		st = &sinkState{sink: s, rate: rate, channels: channels, name: name,
			onSound: func() { p.OnSinkSound.Emit(struct{}{}) }}
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

// SinkSounding is how long sound has been going to the attached sink without a pause for silence:
// false while there is no sink, or it has had nothing to play since it was attached or paused.
func (p *Player) SinkSounding() (time.Duration, bool) {
	s := p.sink.Load()
	if s == nil {
		return 0, false
	}
	since := s.sounding.Load()
	if since == 0 {
		return 0, false
	}
	return time.Since(time.Unix(0, since)), true
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

// sinkScale is what the output gain is multiplied by for a sink that takes step as its own volume:
// the step's gain on the sink curve is the speaker's to apply, so it is taken back out here. Never a
// boost, and a muted output stays muted.
func sinkScale(gain float32, step int) float32 {
	g := sinkGain(step)
	if gain <= 0 || g <= 0 {
		return gain
	}
	return min(1, gain/g)
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
			s.sounding.Store(0)
		}
		if s.paused {
			return
		}
	} else {
		s.quiet = 0
		if s.sounding.Load() == 0 {
			s.sounding.Store(time.Now().UnixNano())
			if s.onSound != nil {
				s.onSound()
			}
		}
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
