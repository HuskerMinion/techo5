package cast

import (
	"encoding/binary"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// holdMax bounds what is held ahead: the phone may send only the latency ahead, so more than this is a
// timestamp that cannot be believed.
const holdMax = 5 * speaker.Rate

// audioOut places a cast's sound by output frame index, as the speaker's Source asks. The first chunk
// fixes where the phone's timeline lands on the card, and each chunk after it goes where its time says,
// so a burst of network jitter moves nothing: the chunk is either in time or dropped.
//
// The card's clock runs a little fast or slow against the phone's (about 200 ppm measured on this
// hardware, 12 ms a minute), so the mapping from time to frames, laid at the nominal rate, slides. It is
// corrected as sendspin's output is (feature/sendspin/output.go, where the reasoning for the numbers is):
// each render period the frame being heard is compared with the frame the clock says should be, the
// error is smoothed, and one frame is repeated or dropped per period while it is outside the band.
//
// driftGain smooths over about a second of periods; driftBand is 2 ms either side, the jitter that
// smoothing leaves; past snapBand the error is a misplaced anchor rather than drift and is put right in
// one step, and past maxSnap it is a clock that changed, so the next chunk anchors afresh. tailFrames is
// the hardware tail: the write counter is that far ahead of what is heard.
const (
	driftGain  = 0.02
	driftBand  = speaker.Rate / 500
	snapBand   = speaker.Rate / 100
	maxSnap    = 2 * speaker.Rate
	tailFrames = int64(speaker.HardwareTail * speaker.Rate / time.Second)
)

type audioOut struct {
	p *speaker.Player

	mu    sync.Mutex
	ready bool
	held  bool
	gain  float32

	// pcm holds frames from base onward; played is where the card has got to.
	base   uint64
	played uint64
	pcm    []int16

	// The frame that carries the time at, fixed by the first chunk.
	anchored bool
	frame    uint64
	at       time.Time

	// drift is the smoothed error between the frame being heard and the frame the clock wants, in
	// frames, positive when the card is playing early. corrected counts frames repeated (positive) or
	// dropped (negative) to hold it. nextReport is the frame the next log line is due at, one a second.
	drift      float64
	corrected  int64
	nextReport uint64

	// now stands in for the clock, so a test can hold time still.
	now func() time.Time

	late, dropped int
}

var (
	_ speaker.Producer = (*audioOut)(nil)
	_ speaker.Source   = (*audioOut)(nil)
)

func newAudioOut(p *speaker.Player) *audioOut { return &audioOut{p: p, gain: 1} }

// open readies the output for a new cast and takes the speaker's source.
func (o *audioOut) open() {
	o.mu.Lock()
	o.ready, o.held, o.anchored = true, false, false
	o.pcm, o.base, o.played, o.late, o.dropped = nil, 0, 0, 0, 0
	o.drift, o.corrected, o.nextReport = 0, 0, 0
	o.mu.Unlock()
	o.p.Attach(o)
}

func (o *audioOut) close() {
	o.p.Attach(nil)
	o.mu.Lock()
	o.ready, o.pcm = false, nil
	o.mu.Unlock()
}

// write places pcm, interleaved S16LE stereo, to be heard at at.
func (o *audioOut) write(pcm []byte, at time.Time) {
	samples := make([]int16, len(pcm)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(pcm[2*i:]))
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.ready {
		return
	}
	frame := o.frameFor(at)
	if frame > o.played && frame-o.played > holdMax {
		o.dropped++
		return
	}
	span := uint64(len(samples) / speaker.Channels)
	if frame+span <= o.played {
		o.late++ // its frames have played: playing it now only leaves this behind for good
		return
	}
	if frame < o.played {
		samples = samples[(o.played-frame)*speaker.Channels:]
		frame = o.played
	}
	if len(o.pcm) == 0 {
		o.base = frame
	}
	if frame < o.base {
		o.pcm = append(make([]int16, (o.base-frame)*speaker.Channels), o.pcm...)
		o.base = frame
	}
	off := int(frame-o.base) * speaker.Channels
	if need := off + len(samples); need > len(o.pcm) {
		o.pcm = append(o.pcm, make([]int16, need-len(o.pcm))...)
	}
	copy(o.pcm[off:], samples)
}

// frameFor is the output frame a time falls on. Wants mu.
func (o *audioOut) frameFor(at time.Time) uint64 {
	if !o.anchored {
		// Frame Written() is going to the card now and is heard a hardware tail later.
		ahead := time.Until(at) - speaker.HardwareTail
		o.frame = o.p.Written() + uint64(max(0, ahead.Seconds()*speaker.Rate))
		o.at, o.anchored = at, true
		o.played = o.frame
		return o.frame
	}
	return uint64(int64(o.frame) + int64(at.Sub(o.at).Seconds()*speaker.Rate))
}

// Render is the speaker asking what to play next.
func (o *audioOut) Render(from uint64, buf []int16) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if from > o.played {
		o.played = from
	}
	if from > o.base && len(o.pcm) > 0 {
		if gone := int((from - o.base) * speaker.Channels); gone >= len(o.pcm) {
			o.pcm = o.pcm[:0]
		} else {
			o.pcm = append(o.pcm[:0], o.pcm[gone:]...)
		}
	}
	if from > o.base {
		o.base = from
	}
	if o.held || !o.ready || from < o.base {
		return
	}
	o.correct(from)
	for i := range min(len(buf), len(o.pcm)) {
		buf[i] = scale(o.pcm[i], o.gain)
	}
}

// correct holds the card to the clock. Wants mu, and pcm's head at from.
func (o *audioOut) correct(from uint64) {
	if !o.anchored || len(o.pcm) < 2*speaker.Channels {
		return
	}
	now := time.Now
	if o.now != nil {
		now = o.now
	}

	// The frame the clock says should be heard now, against the one that is: the frame being rendered
	// less the tail it has yet to travel.
	want := int64(o.frame) + now().Sub(o.at).Nanoseconds()*speaker.Rate/1e9

	// Frames already handed to the card still play at the old alignment, so a correction at full gain
	// overshoots and the next swings back: driftGain is what damps that.
	off := float64(int64(from) - tailFrames - want)
	o.drift += (off - o.drift) * driftGain

	if from >= o.nextReport {
		o.nextReport = from + speaker.Rate
		slog.Debug("cast correction", "off_ms", int64(off)*1000/speaker.Rate,
			"drift_ms", int64(o.drift)*1000/speaker.Rate, "corrected", o.corrected,
			"queued_ms", len(o.pcm)/speaker.Channels*1000/speaker.Rate)
	}

	if off > maxSnap || off < -maxSnap {
		slog.Warn("cast re-anchoring", "off_s", int64(off)/speaker.Rate)
		o.anchored = false
		o.drift = 0
		return
	}

	switch {
	case o.drift > snapBand:
		n := int(o.drift)
		o.pcm = append(make([]int16, n*speaker.Channels), o.pcm...)
		o.frame += uint64(n)
		o.corrected += int64(n)
		o.drift = 0
	case o.drift < -snapBand:
		n := min(int(-o.drift), len(o.pcm)/speaker.Channels-1)
		o.pcm = append(o.pcm[:0], o.pcm[n*speaker.Channels:]...)
		o.frame -= uint64(n)
		o.corrected -= int64(n)
		o.drift = 0
	case o.drift > driftBand:
		// Early: say the first frame twice, and move the anchor with it so what arrives next lands in
		// step with what is already queued.
		o.pcm = append(o.pcm, o.pcm[:speaker.Channels]...)
		copy(o.pcm[speaker.Channels:], o.pcm[:len(o.pcm)-speaker.Channels])
		o.frame++
		o.drift--
		o.corrected++
	case o.drift < -driftBand:
		// Late: skip the first frame.
		n := copy(o.pcm, o.pcm[speaker.Channels:])
		o.pcm = o.pcm[:n]
		o.frame--
		o.drift++
		o.corrected--
	}
}

// Suspend and Resume: whatever else wants the speaker (an answer, an alarm) takes it, and the cast
// rejoins where its clock says when it is let back.
func (o *audioOut) Suspend() { o.mu.Lock(); o.held = true; o.mu.Unlock() }
func (o *audioOut) Resume()  { o.mu.Lock(); o.held = false; o.mu.Unlock() }

// Duck quietens rather than pauses: a hole in the sound of a film is worse than a quiet stretch. db is
// the attenuation the speaker's arbiter asks for, negative, and 0 for none (as sendspin's output reads it).
func (o *audioOut) Duck(db int) {
	gain := float32(1)
	if db < 0 {
		gain = float32(math.Pow(10, float64(db)/20))
	}
	o.mu.Lock()
	o.gain = gain
	o.mu.Unlock()
}

// Requeue has nothing to do: the level is applied as frames are rendered.
func (o *audioOut) Requeue() {}

func (o *audioOut) misses() (late, dropped int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.late, o.dropped
}

func scale(s int16, gain float32) int16 {
	if gain == 1 {
		return s
	}
	return int16(max(math.MinInt16, min(float64(s)*float64(gain), math.MaxInt16)))
}
