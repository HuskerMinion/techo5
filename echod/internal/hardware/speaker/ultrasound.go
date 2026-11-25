package speaker

import (
	"encoding/binary"
	"math"
)

// Ultrasound: presence detection's tone (feature/ultrasound), played under everything else at the
// card's own rate. The tuning runs at Rate, whose Nyquist is 24 kHz, so the tone cannot go through it:
// it is added to what the card is sent, after the upsampler, and only on a card that runs faster than
// Rate. It is added after the limiters, as Fire OS's DSP adds its own, which is what the Echo Dot 5th
// gen's USPD FBL Throttling table keeps headroom for (asp's Tuning.UltrasoundLevel): with the tone at
// that level the sum stays under the full-band limiter's ceiling.

// SetUltrasound plays a tone of hz under the speaker's output, or stops it at 0. The tone fades in
// and out, and a change of frequency fades the old one out first, so neither clicks.
func (p *Player) SetUltrasound(hz int) { p.usHz.Store(int32(hz)) }

// Ultrasound is the tone asked for, 0 for none.
func (p *Player) Ultrasound() int { return int(p.usHz.Load()) }

// ultraFallback is the tone's level where the tuning keeps no headroom for it (none loaded, or a
// tuning without the table): low enough to sit under most of what plays, with what does not clipped.
var ultraFallback = math.Pow(10, -20.0/20)

// ultraLevel is the tone's level now, at the current volume.
func (p *Player) ultraLevel() float32 {
	if t := p.tuning; t != nil {
		if a, ok := t.UltrasoundLevel(float64(p.Step()) / VolumeSteps); ok {
			return float32(a)
		}
	}
	return float32(ultraFallback)
}

// ultraTone is the tone's oscillator and fade, owned by the write loop.
type ultraTone struct {
	hz, rate int
	table    []float32 // one whole number of cycles, so it loops exactly
	at       int
	gain     float32
}

// ultraFade is the fade's per-sample step at the card's rate: some 20 ms from silence to full.
func ultraFade(rate int) float32 { return float32(1 - math.Exp(-1/(0.02*float64(rate)))) }

// ultraTable is cos(2π hz n / rate) over the shortest loop that holds a whole number of cycles. Nil
// for a tone the card cannot carry, or one whose loop would be long (hz not a multiple of something
// that divides the rate).
func ultraTable(hz, rate int) []float32 {
	if hz <= 0 || 2*hz >= rate {
		return nil
	}
	n := rate / gcd(rate, hz)
	if n > 4096 {
		return nil
	}
	t := make([]float32, n)
	for i := range t {
		t[i] = float32(math.Cos(2 * math.Pi * float64(hz) * float64(i) / float64(rate)))
	}
	return t
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// mix adds the tone to buf, interleaved 16-bit PCM of channels at rate, every channel the same: hz
// at level, fading toward it. Saturates rather than wraps.
func (u *ultraTone) mix(buf []byte, channels, rate, hz int, level float32) {
	if hz == 0 && u.gain == 0 {
		return
	}
	if u.rate != rate {
		// A new rate is a new table, from silence: nothing of the old one carries over.
		u.rate, u.hz, u.table, u.at, u.gain = rate, 0, nil, 0, 0
	}
	co := ultraFade(rate)
	frame := 2 * channels
	for i := 0; i+frame <= len(buf); i += frame {
		want := level
		if hz != u.hz {
			want = 0 // the old tone goes out first
			if u.gain < 1e-5 {
				u.hz, u.table, u.at, u.gain = hz, ultraTable(hz, rate), 0, 0
			}
		}
		if u.table == nil {
			continue
		}
		u.gain += (want - u.gain) * co
		s := u.table[u.at] * u.gain * 32767
		u.at++
		if u.at == len(u.table) {
			u.at = 0
		}
		for c := range channels {
			at := i + 2*c
			v := float32(int16(binary.LittleEndian.Uint16(buf[at:]))) + s
			binary.LittleEndian.PutUint16(buf[at:], uint16(int16(max(-32768, min(32767, v)))))
		}
	}
}
