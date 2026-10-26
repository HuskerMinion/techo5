//go:build dot && donut

package speaker

import (
	"testing"
	"time"
)

// The speaker plays at 96 kHz 16-bit, as the port's first build did: at 48 kHz the TAS2770 turned the
// top of speech and music into static (techo5-dot#20). The jack plays at 48 kHz in 32-bit containers,
// the only rate its codec takes.
func TestTheSpeakerAndTheJackOpenTheirOwnWays(t *testing.T) {
	if f := formatFor(OutputSpeaker); f.rate != 96000 || f.bits != 16 {
		t.Errorf("speaker: %d Hz %d-bit, want 96000 Hz 16-bit", f.rate, f.bits)
	}
	if f := formatFor(OutputHeadphone); f.rate != 48000 || f.bits != 32 {
		t.Errorf("jack: %d Hz %d-bit, want 48000 Hz 32-bit", f.rate, f.bits)
	}
	if formatFor(OutputSpeaker).same(formatFor(OutputHeadphone)) {
		t.Error("the two outputs open the same way: a move between them would not reopen the device")
	}
}

// The jack's back end is on for the jack and off for the speaker (DL1 will not open at 96 kHz with it
// on), and nothing written at every start or Reopen turns it on under a stream already open.
func TestTheJacksBackEndFollowsTheFormat(t *testing.T) {
	level := func(seq []kctl, name string) (int32, bool) {
		for _, c := range seq {
			if c.name == name {
				return c.level, true
			}
		}
		return 0, false
	}
	const adda, i2s = "INT ADDA O03_O04 Switch", "I2S O03_O04 Switch"
	if v, ok := level(formatFor(OutputSpeaker).open, adda); !ok || v != 0 {
		t.Errorf("speaker: %s = %d (set %v), want 0", adda, v, ok)
	}
	if v, ok := level(formatFor(OutputHeadphone).open, adda); !ok || v != 1 {
		t.Errorf("jack: %s = %d (set %v), want 1", adda, v, ok)
	}
	for _, out := range []Output{OutputSpeaker, OutputHeadphone} {
		if v, ok := level(formatFor(out).open, i2s); !ok || v != 1 {
			t.Errorf("%s: %s = %d (set %v), want 1: DPCM refuses a front end with no back end", out, i2s, v, ok)
		}
	}
	// The amplifier is muted before the jack's 48 kHz stream opens, so the speaker never plays it.
	if v, ok := level(formatFor(OutputHeadphone).open, "PCM Playback Volume"); !ok || v != 0 {
		t.Errorf("jack: PCM Playback Volume = %d (set %v), want 0 before the stream opens", v, ok)
	}
	if _, ok := level(initSequence, adda); ok {
		t.Errorf("initSequence writes %s: it would join a 96 kHz stream already open", adda)
	}
}

// Every format's ring is over the AFE's 36 KB of SRAM, which panics the kernel when the stream starts,
// and within the quiet margin.
func TestEveryRingIsInDRAMAndInsideTheMargin(t *testing.T) {
	for _, out := range []Output{OutputSpeaker, OutputHeadphone} {
		f := formatFor(out)
		frames := period * f.rate / Rate * periods
		bytes := frames * Channels * f.bits / 8
		if bytes <= 36*1024 {
			t.Errorf("%s: ring %d bytes, want over 36 KB", out, bytes)
		}
		if d := time.Duration(frames) * time.Second / time.Duration(f.rate); d > 150*time.Millisecond {
			t.Errorf("%s: ring %v, want 150 ms or less", out, d)
		}
	}
}

// The speaker gets quieter at every step down, and its first step is really quiet: through the TAS2770
// the Dot 2's -33 dB was not (2026-10-25). The top is unity, so the loudest the Dot 3 plays is unchanged.
func TestTheSpeakersCurveFallsAllTheWayToQuiet(t *testing.T) {
	curve := volumeCurves[OutputSpeaker]
	if curve[1] > -50 {
		t.Errorf("step 1 at %v dB, want -50 dB or lower", curve[1])
	}
	if curve[VolumeSteps] != 0 {
		t.Errorf("step %d at %v dB, want 0", VolumeSteps, curve[VolumeSteps])
	}
	for step := 2; step <= VolumeSteps; step++ {
		if curve[step] <= curve[step-1] {
			t.Errorf("step %d at %v dB, not louder than step %d at %v dB", step, curve[step], step-1, curve[step-1])
		}
	}
}
