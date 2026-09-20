//go:build !dot && !spot

package speaker

import (
	"math"
	"testing"
)

// The Show's curve is linear in dB: −45 dB at the first step, −24 dB at half, −6 dB at the top.
func TestGainForStep(t *testing.T) {
	cases := []struct {
		out  Output
		step int
		db   float64
	}{
		{OutputSpeaker, 1, -45},
		{OutputSpeaker, 5, -39},
		{OutputSpeaker, 15, -24},
		{OutputSpeaker, 30, -6},
		{OutputHeadphone, 15, -24},
		{OutputHeadphone, 30, -6},
	}

	for _, c := range cases {
		got := gainForStep(c.out, c.step)
		want := float32(math.Pow(10, c.db/20))
		if math.Abs(float64(got-want)) > 0.001 {
			t.Errorf("gainForStep(%s, %d) = %v, want %v (%v dB)", c.out, c.step, got, want, c.db)
		}
	}
	for _, out := range []Output{OutputSpeaker, OutputHeadphone} {
		if got := gainForStep(out, 0); got != 0 {
			t.Errorf("gainForStep(%s, 0) = %v, want silence", out, got)
		}
	}
}

// The 2nd gen's start is its one safe-mode write, as before; the 1st gen's is the RT5616's routing
// with the amplifier switch Off (active low) and both of LOUT_CTRL1's mutes cleared.
func TestShowInit(t *testing.T) {
	if got := showInit(false); len(got) != 1 || got[0].name != "Speaker Safe Mode A" || got[0].level != 0 {
		t.Errorf("2nd gen: %+v", got)
	}
	want := map[string]bool{"Ext_Speaker_Amp_Switch": false, "OUT Playback Switch": false, "OUT Channel Switch": false,
		"LOUT MIX OUTVOL L Switch": false, "LOUT MIX OUTVOL R Switch": false}
	for _, k := range showInit(true) {
		if k.name == "Speaker Safe Mode A" {
			t.Error("1st gen: the 2nd gen's safe mode control is not on this board")
		}
		if k.name == "Ext_Speaker_Amp_Switch" && k.value != "Off" {
			t.Errorf("1st gen: the amplifier switch is active low and must be Off, got %q", k.value)
		}
		if _, ok := want[k.name]; ok {
			want[k.name] = k.name == "Ext_Speaker_Amp_Switch" || k.level == 1
		}
	}
	for name, ok := range want {
		if !ok {
			t.Errorf("1st gen: %s missing or not set", name)
		}
	}
	if AmpSwitch != "" {
		t.Error("AmpSwitch must stay empty: switching it resets the 2nd gen's amplifier and silences the 1st gen's")
	}
}
