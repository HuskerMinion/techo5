//go:build !dot && !spot

package camera

import (
	"math"
	"testing"
)

// ispModel is what the ISP does to one 10-bit sample of a channel with the development's settings
// (the kernel's program_tone): OBC's offset and gain on the 12-bit scale, clipped to 12 bits, then
// the gamma table, linear between its knots, to 10 bits, which BT.601 takes at a quarter of a code.
func ispModel(d *cronosDevelop, ch, x int) float64 {
	v := (float64(4*x) + float64(d.black)) * float64(d.gain[ch]) / 512
	v = math.Min(math.Max(v, 0), 4095)
	i := 0
	for i < 143 && float64(ispKnot(i+1)) <= v {
		i++
	}
	k0, k1 := float64(ispKnot(i)), float64(ispKnot(i+1))
	y := float64(d.curve[i]) + (float64(d.curve[i+1])-float64(d.curve[i]))*(v-k0)/(k1-k0)
	return y / 4
}

// The ISP's leveling of a channel matches the cores' tables to within a code over the whole range,
// for an ordinary frame and for a dark, backlit one (steep gamma, big stretch).
func TestISPDevelopTone(t *testing.T) {
	for _, tn := range []tone{
		{gainR: 1.65, gainB: 1.29, white: 1844, black: 32, gamma: 1 / 1.933},
		{gainR: 1.1, gainB: 1.2, white: 244, black: 20, gamma: 1 / 2.4},
		{gainR: 1, gainB: 1, white: 2046, black: 0, gamma: 1},
	} {
		var d cronosDevelop
		ispDevelopTone(tn, &d)
		lut := tn.tables()
		worst, worstDark := 0.0, 0.0
		for ch, lc := range []int{0, 1, 1, 2} {
			for x := 0; x < 1024; x++ {
				if 2*x <= tn.black {
					continue // at or below black: the curve's start may be lifted (program_tone)
				}
				diff := math.Abs(ispModel(&d, ch, x) - float64(lut[lc][2*x]))
				// The table's first two entries (12-bit inputs below 16) are coarse where a gamma
				// curve is steepest: the first level or two above black.
				if (float64(4*x)+float64(d.black))*float64(d.gain[ch])/512 < 16 {
					worstDark = math.Max(worstDark, diff)
				} else {
					worst = math.Max(worst, diff)
				}
			}
		}
		if worst > 1 || worstDark > 4 {
			t.Errorf("tone %+v: off by %.2f codes at worst, %.2f just above black", tn, worst, worstDark)
		}
	}
}

// The gamma table's knots cover the 12-bit range in 144 steps.
func TestISPKnots(t *testing.T) {
	if ispKnot(0) != 0 || ispKnot(64) != 512 || ispKnot(96) != 1024 || ispKnot(128) != 2048 || ispKnot(144) != 4096 {
		t.Fatal("knots", ispKnot(64), ispKnot(96), ispKnot(128), ispKnot(144))
	}
	for i := 0; i < 144; i++ {
		if ispKnot(i+1) <= ispKnot(i) {
			t.Fatal("not increasing at", i)
		}
	}
}
