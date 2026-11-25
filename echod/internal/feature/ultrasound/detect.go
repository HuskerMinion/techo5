package ultrasound

import "time"

// hold is how long after the last movement Motion stays on: people stop moving now and then, and a
// sensor that flickers off between steps is no use to an automation.
const hold = 30 * time.Second

// minCarrier is how far over the noise the tone has to be heard, in dB per bin, for the room to be
// measured at all: under it the speaker is not playing it (stopped, the output elsewhere) or the
// microphones do not hear it, and the motion band says nothing either way.
const minCarrier = 25.0

// onBlocks of the last ofBlocks have to see movement for it to count: Fire OS's "Major Movement State
// Ctr 0 to 1", a count of frames. A hop is 64 ms, so that is about 200 ms of movement in 256.
const (
	onBlocks = 3
	ofBlocks = 4
)

// thresholdFor is the motion band's excess over the noise that counts as movement, in dB, for a
// sensitivity of 1 to 100: 10 dB at 1, 6 at 50, 3 at 100. A still room reads 0 to 2 (the most of
// three microphones' readings, each a ratio of two averages of a hundred bins; dsp_test.go, with music
// playing), and somebody walking a couple of meters off tens of dB.
func thresholdFor(sensitivity int) float64 {
	s := float64(min(max(sensitivity, 1), 100))
	if s <= 50 {
		return 10 - 4*(s-1)/49
	}
	return 6 - 3*(s-50)/50
}

// detector turns readings into Motion.
type detector struct {
	threshold float64
	recent    [ofBlocks]bool
	n         int
	last      time.Time // the last movement
	deaf      bool      // the last reading did not hear the tone
}

// take one reading at now, and say whether there is motion.
func (d *detector) take(r reading, now time.Time) bool {
	d.deaf = r.carrier < minCarrier
	moved := !d.deaf && r.motion > d.threshold
	d.recent[d.n%ofBlocks] = moved
	d.n++
	count := 0
	for _, m := range d.recent {
		if m {
			count++
		}
	}
	if count >= onBlocks {
		d.last = now
	}
	return d.motion(now)
}

// motion is whether movement was seen within hold of now.
func (d *detector) motion(now time.Time) bool {
	return !d.last.IsZero() && now.Sub(d.last) < hold
}

// reset forgets what was seen, for a new tone or after the microphones were cut.
//
//lint:ignore U1000 used by the Dot 5th gen's listener (ultrasound_cannoli.go), not on other builds
func (d *detector) reset() {
	d.recent, d.n, d.last = [ofBlocks]bool{}, 0, time.Time{}
}
