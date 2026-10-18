package phone

import (
	"math"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// underlay is the far end played under whatever took the speaker from the call (a turn's chime and
// reply, an announcement), on the track's queue, which is mixed under the words rather than waiting
// for them. It is as far down as music is under a reply.
type underlay struct {
	bed   *speaker.Bed
	up    speaker.Resampler
	gain  float64
	limit int // frames queued, at the speaker's rate, past which the queue is dropped, as the call's own is
	out   []int16
}

func newUnderlay(bed *speaker.Bed, limit int) *underlay {
	up, _ := speaker.NewResampler(config.DefaultResampling)
	return &underlay{bed: bed, up: up, gain: underGain(config.Get().Media.DuckDB), limit: limit}
}

// underGain is the level music is ducked to under words, as a gain: none asked for is none given.
func underGain(db int) float64 {
	if db >= 0 {
		return 1
	}
	return math.Pow(10, float64(db)/20)
}

// play queues 16 kHz mono from the call, turned down and stretched to the speaker's rate.
func (u *underlay) play(s []int16) {
	quiet := make([]int16, len(s))
	for i, v := range s {
		quiet[i] = int16(float64(v) * u.gain)
	}
	if u.bed.Queued() > u.limit {
		u.bed.Drain()
	}
	u.out = u.up.Run(quiet, u.out[:0])
	u.bed.Play(append([]int16(nil), u.out...))
}
