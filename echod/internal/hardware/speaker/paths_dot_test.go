//go:build dot && !donut

package speaker

import (
	"math"
	"testing"
)

// The Dot's vendor curve reaches unity at the top.
func TestGainForStep(t *testing.T) {
	cases := []struct {
		out  Output
		step int
		want float32
	}{
		{OutputSpeaker, 0, 0},
		{OutputSpeaker, 30, 1},
		{OutputSpeaker, 15, 0.398}, // -8 dB
		{OutputSpeaker, 5, 0.0708}, // -23 dB
		{OutputHeadphone, 0, 0},
		{OutputHeadphone, 30, 1},
		{OutputHeadphone, 15, 0.112}, // -19 dB
	}

	for _, c := range cases {
		got := gainForStep(c.out, c.step)
		if math.Abs(float64(got-c.want)) > 0.001 {
			t.Errorf("gainForStep(%s, %d) = %v, want %v", c.out, c.step, got, c.want)
		}
	}
}

// The DAC reaches the speaker amp and the jack through the same two mixer switches, and the codec
// boots with them off, so each output has to turn them on itself: a Dot that started on the jack
// once stayed silent until the output was changed. The Echo 2nd gen has the same codec.
func TestDotPathsRouteTheDAC(t *testing.T) {
	for board, paths := range map[string]map[Output][]kctl{"dot": dotPaths, "radar": radarPaths(unityEQ(), unityEQ())} {
		for _, out := range []Output{OutputSpeaker, OutputHeadphone} {
			on := map[string]bool{}
			for _, k := range paths[out] {
				if k.level == 1 {
					on[k.name] = true
				}
			}
			for _, name := range []string{"HPL Output Mixer L_DAC Switch", "HPR Output Mixer R_DAC Switch"} {
				if !on[name] {
					t.Errorf("%s %s: %s not on", board, out, name)
				}
			}
		}
	}
}
