//go:build !dot && !spot

package speaker

import (
	"math"
	"testing"
	"time"
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
	} else if !got[0].ifPresent {
		// Some 2nd gen units have a TAS5805M, with no safe mode, instead of the MAX98396 (#59).
		t.Error("2nd gen: the safe mode write is not skipped on a unit without the control")
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

// The jack (1st gen Show 5 and Show 8): headphones open the codec's headphone pins and mute the line-out that feeds the
// speaker; the speaker opens the line-out again, after headphoneOff has closed the pins. The boards
// without a jack touch nothing.
func TestShowPaths(t *testing.T) {
	paths, off := showPaths(false)
	if len(paths[OutputSpeaker]) != 0 || len(paths[OutputHeadphone]) != 0 || len(paths[OutputBoth]) != 0 || len(off) != 0 {
		t.Errorf("no jack: %+v %+v", paths, off)
	}

	paths, off = showPaths(true)
	level := func(seq []kctl) map[string]int32 {
		m := map[string]int32{}
		for _, k := range seq {
			m[k.name] = k.level
		}
		return m
	}
	hp := level(paths[OutputHeadphone])
	for _, name := range []string{"HPVOL Playback Switch", "HPO MIX HPVOL Switch", "HP Playback Switch"} {
		if v, ok := hp[name]; !ok || v != 1 {
			t.Errorf("headphone: %s not on", name)
		}
	}
	if v, ok := hp["OUT Playback Switch"]; !ok || v != 0 {
		t.Error("headphone: the speaker's line-out is not muted")
	}
	if v, ok := level(paths[OutputSpeaker])["OUT Playback Switch"]; !ok || v != 1 {
		t.Error("speaker: the line-out is not unmuted")
	}
	both := level(paths[OutputBoth])
	for _, name := range []string{"OUT Playback Switch", "HPVOL Playback Switch", "HPO MIX HPVOL Switch", "HP Playback Switch"} {
		if v, ok := both[name]; !ok || v != 1 {
			t.Errorf("both: %s not on", name)
		}
	}
	hpOff := level(off)
	for _, name := range []string{"HPVOL Playback Switch", "HPO MIX HPVOL Switch", "HP Playback Switch"} {
		if v, ok := hpOff[name]; !ok || v != 0 {
			t.Errorf("headphoneOff: %s not off", name)
		}
	}
}

// Both plays the two outputs only while something is plugged in and only where the board can; with
// nothing in the jack it is the speaker, and where it cannot it follows the jack as Automatic does.
func TestDesiredOutputBoth(t *testing.T) {
	was := HasBoth
	t.Cleanup(func() { HasBoth = was })
	p := &Player{outputMode: OutputModeBoth}

	HasBoth = true
	if got := p.desiredOutput(OutputHeadphone); got != OutputBoth {
		t.Errorf("plugged in: %s, want both", got)
	}
	if got := p.desiredOutput(OutputSpeaker); got != OutputSpeaker {
		t.Errorf("nothing plugged in: %s, want the speaker", got)
	}

	// The other choices are as they were beside it.
	for _, c := range []struct {
		mode     OutputMode
		detected Output
		want     Output
	}{
		{OutputModeSpeaker, OutputHeadphone, OutputSpeaker},
		{OutputModeHeadphone, OutputHeadphone, OutputHeadphone},
		{OutputModeHeadphone, OutputSpeaker, OutputSpeaker},
		{OutputModeAuto, OutputHeadphone, OutputHeadphone},
	} {
		q := &Player{outputMode: c.mode}
		if got := q.desiredOutput(c.detected); got != c.want {
			t.Errorf("mode %d, %s detected: %s, want %s", c.mode, c.detected, got, c.want)
		}
	}

	HasBoth = false
	if got := p.desiredOutput(OutputHeadphone); got != OutputHeadphone {
		t.Errorf("a board without both: %s, want the headphones, as Automatic", got)
	}
}

// Output answers from what was last set without the path lock, so the write loop never waits on a
// switch: held here as setOutput holds it across the codec writes.
func TestOutputDoesNotWaitOnASwitch(t *testing.T) {
	p := New()
	p.pathMu.Lock()
	defer p.pathMu.Unlock()
	done := make(chan Output, 1)
	go func() { done <- p.Output() }()
	select {
	case got := <-done:
		if got != OutputSpeaker {
			t.Errorf("Output = %s, want the speaker on a board with no jack", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Output waited on the path lock")
	}
}

// The Show's DL1 driver reads a restart with one period in the ring as an underflow and stops it
// again; the stream has to start full (#142).
func TestTheShowStartsWithAFullRing(t *testing.T) {
	if !startFull {
		t.Fatal("the Show's playback starts before its ring is full")
	}
}
