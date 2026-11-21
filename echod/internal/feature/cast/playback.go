package cast

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// Playback plays a stamped audio stream on the speaker with the cast receiver's timing, for a source
// that is not a cast: the dashboard stream's sound uses it. The sender's clock messages fix the
// difference between its clock and this one, and a chunk is heard at its stamp plus that difference
// plus the latency, or dropped if that moment has gone.
type Playback struct {
	out     *audioOut
	sync    clockSync
	latency time.Duration
}

// NewPlayback takes the speaker as a background source, as a cast does, and gives it up at Close.
// latency is how long after its stamp a chunk is heard.
func NewPlayback(latency time.Duration) *Playback {
	p := &Playback{out: newAudioOut(speaker.Get()), sync: clockSync{start: time.Now()}, latency: latency}
	p.out.open()
	speaker.Sound().Backgrounds().Took(p.out)
	return p
}

// Clock takes a clock message: the sender's clock now, in µs.
func (p *Playback) Clock(us int64) { p.sync.observe(us) }

// Audio takes one chunk of interleaved S16LE stereo 48 kHz PCM stamped us. It is dropped before the
// first clock message (nothing can be placed), when it is not whole frames, and when it is stamped
// further ahead than a sender that keeps to the latency would.
func (p *Playback) Audio(us int64, pcm []byte) {
	if len(pcm) == 0 || len(pcm)%4 != 0 {
		return
	}
	due, ok := p.sync.at(us)
	if !ok {
		return
	}
	due = due.Add(p.latency)
	if time.Until(due) > farMax {
		return
	}
	p.out.write(pcm, due)
}

// Close gives the speaker up and drops what is queued.
func (p *Playback) Close() {
	speaker.Sound().Backgrounds().Gave(p.out)
	p.out.close()
}
