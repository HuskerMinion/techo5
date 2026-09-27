package speaker

import (
	"encoding/binary"
	"math"
)

// The level is how loud the speaker is playing, from 0 for silence to 1 for full scale, for the screen:
// the orb pulses with it while a reply plays. It is measured on each period just before it goes to the
// codec, so it follows what is heard rather than what is queued.
//
// Like the microphone's level it is smoothed for looking at: it jumps up with a syllable and falls
// away over a few periods, so the pulse reads as speech rather than flicker.

// levelFall is how much of the last reading carries into a quieter period.
const levelFall = 0.82

// meter reads one period's peak into the level. Both channels carry the same signal and a peak does
// not need every sample, so it looks at the left channel of every fourth frame.
func (p *Player) meter(buf []byte) {
	peak := 0
	for i := 0; i+1 < len(buf); i += 4 * Channels * Bits / 8 {
		v := int(int16(binary.LittleEndian.Uint16(buf[i:])))
		if v < 0 {
			v = -v
		}
		peak = max(peak, v)
	}
	now := float32(peak) / 32767
	if last := math.Float32frombits(p.level.Load()); now < last {
		now = last*levelFall + now*(1-levelFall)
	}
	p.level.Store(math.Float32bits(now))
}

// Level is the speaker's level now, 0 to 1.
func (p *Player) Level() float64 {
	return float64(math.Float32frombits(p.level.Load()))
}
