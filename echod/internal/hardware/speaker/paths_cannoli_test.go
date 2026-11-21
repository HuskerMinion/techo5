//go:build dot && cannoli

package speaker

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The Speaker gain is the MAX98396's own level, 1 dB a step from Fire OS's +14 dB, held to +6 dB over it.
func TestTheDot5SpeakerGainIsTheAmplifiers(t *testing.T) {
	if got := ampFor(0); got != ampUnity {
		t.Errorf("0 dB is level %d, not %d", got, ampUnity)
	}
	if got := ampFor(3); got != ampUnity+3 {
		t.Errorf("3 dB is level %d", got)
	}
	if got := ampFor(config.SpeakerGainMax); got > ampMax {
		t.Errorf("the most gain is level %d, past %d", got, ampMax)
	}
	if got := ampFor(-5); got != ampUnity {
		t.Errorf("a negative gain is level %d", got)
	}
	seq := withSpeakerGain(pathSequence[OutputSpeaker], OutputSpeaker, 2)
	if len(seq) != 1 || seq[0].name != ampLevel || seq[0].level != ampUnity+2 {
		t.Errorf("the speaker's path with 2 dB: %+v", seq)
	}
	if pathSequence[OutputSpeaker][0].level != ampUnity {
		t.Error("withSpeakerGain changed the path it was given")
	}
}

// One output at eTDM2's 96 kHz, opened with DL2's routes into it on, and no jack.
func TestTheDot5PlaysAtTheAmplifiersRate(t *testing.T) {
	if HasJack || HasBoth || DetectOutput() != OutputSpeaker {
		t.Error("the Dot 5 has a jack")
	}
	for _, out := range []Output{OutputSpeaker, OutputHeadphone} {
		f := formatFor(out)
		if f.rate != 96000 || f.bits != Bits {
			t.Errorf("%s: %d Hz, %d bits", out, f.rate, f.bits)
		}
		on := map[string]bool{}
		for _, c := range f.open {
			on[c.name] = c.level == 1
		}
		if !on["O04 I40 Switch"] || !on["O05 I41 Switch"] {
			t.Errorf("%s: DL2 is not routed into eTDM2 on open: %+v", out, f.open)
		}
	}
	if gainForStep(OutputSpeaker, 0) != 0 || gainForStep(OutputSpeaker, VolumeSteps) != 1 {
		t.Error("the volume curve does not run from silence to unity")
	}
}
