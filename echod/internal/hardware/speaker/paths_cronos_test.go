//go:build !dot && !spot && !panel

package speaker

import (
	"math"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
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

// A workstation reads as a Show 5, so the board values have to be the Show 5's: its playback device,
// the card at the pipeline's own rate, nothing trimmed, and its own start sequence.
func TestTheShowsPlaybackIsUnchangedOffAthena(t *testing.T) {
	if PlaybackDevice != 23 || hwRate != Rate || hwTrimDB != 0 {
		t.Errorf("device %d at %d Hz trimmed %v dB, want 23 at %d and 0", PlaybackDevice, hwRate, hwTrimDB, Rate)
	}
	if got, want := len(boardInit()), len(showInit(rt5616())); got != want {
		t.Errorf("start sequence has %d writes, want the Show's %d", got, want)
	}
	if onAthena(7, 23) != 23 {
		t.Error("onAthena picked athena's value on a workstation")
	}
}

// athena's card runs at twice the pipeline's rate, so a period has to come out whole there, and its
// trim only ever turns the speaker down.
func TestAthenasPlaybackSettings(t *testing.T) {
	const athenaRate = 96000
	if athenaRate%Rate != 0 || period*athenaRate%Rate != 0 {
		t.Errorf("%d Hz is not a whole multiple of %d with a %d-frame period", athenaRate, Rate, period)
	}
	if athenaTrimDB > 0 {
		t.Errorf("athena's trim is %+v dB: it may only make the speaker quieter", athenaTrimDB)
	}
}

// athena's playback ring fits the block the driver used for it on the unit (paths_cronos.go).
func TestAthenasPlaybackRingFits(t *testing.T) {
	if bytes := period * 96000 / Rate * periods * Channels * Bits / 8; bytes > athenaRingMax {
		t.Errorf("playback ring %d bytes, the driver's block is %d", bytes, athenaRingMax)
	}
}

// The Skylight's speaker is the RK809 codec's headphone outputs feeding an external amplifier: the
// SPKO path was silent on the unit. The mux has to name HP, and the codec runs at its 0 dB top (255)
// with the volume in software.
func TestTheSkylightPlaysThroughTheHeadphoneOutputs(t *testing.T) {
	want := map[string]kctl{
		"Playback Mux":           {name: "Playback Mux", value: "HP"},
		"Master Playback Volume": {name: "Master Playback Volume", level: 255},
	}
	if len(skylightInit) != len(want) {
		t.Fatalf("%d writes, want %d: %+v", len(skylightInit), len(want), skylightInit)
	}
	for _, c := range skylightInit {
		w, ok := want[c.name]
		if !ok || c.value != w.value || c.level != w.level || c.ifPresent {
			t.Errorf("write %+v, want %+v", c, w)
		}
	}
}

// athenaLevel is the level a sequence writes to athena's amplifier volume, or -1 when it writes none.
func athenaLevel(t *testing.T, seq []kctl) int32 {
	t.Helper()
	level := int32(-1)
	for _, k := range seq {
		if k.name == athenaMaster {
			if level >= 0 {
				t.Fatalf("the amplifier volume is written twice in %v", seq)
			}
			level = k.level
		}
	}
	return level
}

// onAthenaMainline makes the test's board athena on the mainline kernel, as far as the Speaker gain
// is concerned.
func onAthenaMainline(t *testing.T) {
	was := gainOnPath
	gainOnPath = true
	t.Cleanup(func() { gainOnPath = was })
}

// On athena's mainline kernel the Speaker gain is the amplifier's digital volume, 110 (0 dB) plus 1 a
// dB, written with the speaker's path after initSequence's 0 dB, held to 0..+6 dB.
func TestSpeakerGainOnAthenaIsTheAmplifierVolume(t *testing.T) {
	if got := athenaLevel(t, athenaMainlineInit); got != 110 {
		t.Fatalf("athena's init writes the amplifier at %d, want 110 (0 dB)", got)
	}
	onAthenaMainline(t)
	path := []kctl{{name: "Some Switch", level: 1}}
	for db := 0; db <= config.SpeakerGainMax; db++ {
		gained := withSpeakerGain(path, OutputSpeaker, db)
		if got := athenaLevel(t, gained); got != int32(110+db) {
			t.Errorf("+%d dB: level %d, want %d", db, got, 110+db)
		}
		if len(gained) != 2 || gained[0].name != path[0].name || gained[0].level != path[0].level || gained[1].name != athenaMaster {
			t.Errorf("+%d dB: the path is %v, want %v then the amplifier volume", db, gained, path)
		}
		if got := withSpeakerGain(path, OutputHeadphone, db); len(got) != 1 {
			t.Errorf("+%d dB off the speaker: %v", db, got)
		}
	}
	for db, want := range map[int]int32{-1: 110, -40: 110, 7: 116, 29: 116} {
		if got := athenaLevel(t, withSpeakerGain(nil, OutputSpeaker, db)); got != want {
			t.Errorf("%d dB: level %d, want %d", db, got, want)
		}
	}
	if len(path) != 1 || cap(path) != 1 {
		t.Errorf("the path itself was changed: %v", path)
	}
	if got := speakerGainControls(4); len(got) != 1 || athenaLevel(t, got) != 114 {
		t.Errorf("a live change writes %v, want the amplifier volume alone at 114", got)
	}
	if !GainAvailable() || !GainPossible() {
		t.Error("athena's amplifier volume is always there")
	}
}

// Every other Show build board keeps the Show 5's TAS5805M gain (gain.go): no path carries it.
func TestSpeakerGainOffAthenaIsNotOnThePath(t *testing.T) {
	if gainOnPath {
		t.Skip("this test host reads as athena on the mainline kernel")
	}
	path := []kctl{{name: "OUT Playback Switch", level: 1}}
	if got := withSpeakerGain(path, OutputSpeaker, 6); len(got) != 1 || athenaLevel(t, got) != -1 {
		t.Errorf("the path became %v", got)
	}
	if got := speakerGainControls(6); got != nil {
		t.Errorf("a live change writes %v, want nothing", got)
	}
}

// athena's gain follows every path the player writes (Player.path), and a change while the speaker
// plays is written at once, both channels by the one level.
func TestSpeakerGainOnAthenaFollowsThePath(t *testing.T) {
	onAthenaMainline(t)
	p := &Player{out: OutputSpeaker}
	for db := 0; db <= config.SpeakerGainMax; db++ {
		p.gainDB = db
		if got := athenaLevel(t, p.path(OutputSpeaker)); got != int32(110+db) {
			t.Errorf("speaker at +%d dB: level %d", db, got)
		}
	}

	m := &athenaGainWrites{}
	was := heldPathMixer
	heldPathMixer = func(*Player) pathGainMixer { return m }
	t.Cleanup(func() { heldPathMixer = was })
	if err := p.SetSpeakerGain(6); err != nil {
		t.Fatal(err)
	}
	if len(m.levels) != 1 || m.names[0] != athenaMaster || m.levels[0] != 116 || p.gainDB != 6 {
		t.Fatalf("+6 dB on the speaker: wrote %v %v, kept %d", m.names, m.levels, p.gainDB)
	}
	if err := p.SetSpeakerGain(7); err == nil || p.gainDB != 6 {
		t.Fatalf("+7 dB was taken: err %v, kept %d", err, p.gainDB)
	}
}

type athenaGainWrites struct {
	names  []string
	levels []uint32
}

func (m *athenaGainWrites) SetInt(name string, v uint32) error {
	m.names = append(m.names, name)
	m.levels = append(m.levels, v)
	return nil
}
