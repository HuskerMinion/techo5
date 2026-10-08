//go:build !dot && !spot

package speaker

import (
	"math"
	"os"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// Output is one of the device's audio outputs. Only the 1st gen Show 5 has a headphone jack (see
// HasJack); on the others OutputHeadphone exists only so the shared code compiles, and DetectOutput
// never returns it.
type Output string

const (
	OutputSpeaker   Output = "speaker"
	OutputHeadphone Output = "headphone"
	// OutputBoth is the speaker and the jack at once (HasBoth).
	OutputBoth Output = "both"
)

// The playback ring: the vendor HAL's period at twice its depth.
const (
	period  = 768
	periods = 4
)

// DRAMHold is a second AFE PCM node the player holds open, unconfigured, for as long as it holds
// the playback device.
//
// The MediaTek DL1 driver decides at open() where its ring lives: in the AFE's internal SRAM when
// no other AFE stream is open, in DRAM otherwise. On this Amazon kernel the SRAM path faults on
// the first copy_from_user — a kernel panic and reboot (mtk_pcm_I2S0dl1_copy, fault address
// ffffff8009eb0004), reproduced five times on 2026-09-14 with rings of 12 and 16 KB — while the
// DRAM path plays cleanly. Holding any other AFE node marks the SRAM taken, so DL1 takes DRAM.
// MultiMedia1_Capture is an AFE capture nothing on this device uses.
const DRAMHold = "/dev/snd/pcmC0D1c"

// A mixer write, as the shared player applies it.
type kctl struct {
	name  string
	value string
	level int32
	blob  []byte
	// ifPresent writes the control only on a unit that has it: a part that differs between units of
	// the same model, rather than one that should always be there.
	ifPresent bool
}

// On cronos the AIC3101 codec is left as the kernel brings it up, the playback stream is driven
// at unity and the volume curve is applied in software; there is nothing to route, and the
// amplifier switch is never touched (see AmpSwitch below). One write matters: the MAX98396 comes
// up in "Speaker Safe Mode", a power cap that takes about 30 dB off the output (measured
// 2026-09-15: a 0.3 FS tone at the mic went from -47 to -15 dBFS when it was cleared). Amazon's
// HAL cleared it at boot; with Android on the null HAL nobody does, so the daemon does. Codec
// writes are cached until the stream powers up, which is why this goes before the first write.
//
// The 1st gen (checkers) has neither chip: a Realtek RT5616 codec and an amplifier on a GPIO, and
// the kernel leaves the codec connected to nothing, which Amazon's audio layer wired once at boot.
// Three things make it play, found on a unit by Empty2k12 (techo5-checkers docs/hardware.md), and
// missing any one is silence:
//   - Ext_Speaker_Amp_Switch is active low here: LineageOS plays with it Off. It is set Off once
//     and left, so AmpSwitch, which switches it On to play, stays empty on both generations.
//   - The DAC reaches the speaker through OUT MIX, OUTVOL and the line-out (not the headphone
//     pins), each step a switch to turn on.
//   - Two mutes share LOUT_CTRL1 (reg 03), OUT Playback Switch and OUT Channel Switch, both set
//     out of reset; with only the first cleared everything looks routed and nothing plays. A
//     switch at 1 is unmuted.
//
// The Show 8 (crown) has the same RT5616 on the same playback device, and the same sequence is
// what it wants: checked a write at a time on a unit 2026-09-22, where LineageOS had left the
// routing wired but OUT Playback Switch off and the amplifier switch On. Clearing the mute alone
// was not enough; setting Ext_Speaker_Amp_Switch Off is what reached the room, so crown's amplifier
// switch is active low as well and its separate amp_gpio is not the gate. A 1 kHz tone at 0.3 FS
// then read -4 dBFS at the microphones, up from -53. See TECHO5-CROWN crown-port-notes.md.
var initSequence = showInit(rt5616())

// rt5616 is whether the speaker is the Realtek codec and its GPIO amplifier rather than the
// MAX98396: true on the 1st gen Show 5 and on the Show 8, false on the 2nd gen Show 5.
func rt5616() bool { return layout.Checkers() || layout.Crown() }

func showInit(rt5616 bool) []kctl {
	if !rt5616 {
		// The MAX98396's safe mode, cleared at start. Some 2nd gen units have a TI TAS5805M instead,
		// which has no such mode and plays at a normal level as it comes up (#59).
		return []kctl{{name: "Speaker Safe Mode A", level: 0, ifPresent: true}}
	}
	return []kctl{
		{name: "Ext_Speaker_Amp_Switch", value: "Off"},
		{name: "DAC MIXL INF1 Switch", level: 1},
		{name: "DAC MIXR INF1 Switch", level: 1},
		{name: "Stereo DAC MIXL DAC L1 Switch", level: 1},
		{name: "Stereo DAC MIXL DAC R1 Switch", level: 1},
		{name: "Stereo DAC MIXR DAC R1 Switch", level: 1},
		{name: "Stereo DAC MIXR DAC L1 Switch", level: 1},
		{name: "OUT MIXL DAC L1 Switch", level: 1},
		{name: "OUT MIXR DAC R1 Switch", level: 1},
		{name: "LOUT MIX OUTVOL L Switch", level: 1},
		{name: "LOUT MIX OUTVOL R Switch", level: 1},
		{name: "OUT Playback Switch", level: 1},
		{name: "OUT Channel Switch", level: 1},
	}
}

// pathSequence and headphoneOff move the 1st gen Show 5 between its speaker and its jack; the other
// boards have no jack and nothing to move.
var pathSequence, headphoneOff = showPaths(HasJack)

// showPaths is the RT5616's two outputs on the 1st gen Show 5, found on a unit 2026-10-03 with
// headphones in. The jack hangs off the codec's headphone pins: OUT MIX feeds HPVOL, HPVOL feeds HPO
// MIX, and HP Playback Switch unmutes the pins; out of reset all three are off, which is why the jack
// was silent. The speaker is the line-out (see showInit), so muting OUT Playback Switch quiets it
// while the headphones play. The board's own Headphone_Speaker_Mux and Ext_Headphone_Amp_Switch were
// left alone and were not needed.
func showPaths(jack bool) (map[Output][]kctl, []kctl) {
	if !jack {
		return map[Output][]kctl{OutputSpeaker: {}, OutputHeadphone: {}, OutputBoth: {}}, []kctl{}
	}
	return map[Output][]kctl{
			OutputSpeaker: {
				{name: "OUT Playback Switch", level: 1},
			},
			OutputHeadphone: {
				{name: "OUT Playback Switch", level: 0},
				{name: "HPVOL Playback Switch", level: 1},
				{name: "HPO MIX HPVOL Switch", level: 1},
				{name: "HP Playback Switch", level: 1},
			},
			// Both is the headphone path with the line-out left open: the speaker and the jack play
			// the same audio at the same volume.
			OutputBoth: {
				{name: "OUT Playback Switch", level: 1},
				{name: "HPVOL Playback Switch", level: 1},
				{name: "HPO MIX HPVOL Switch", level: 1},
				{name: "HP Playback Switch", level: 1},
			},
		}, []kctl{
			{name: "HP Playback Switch", level: 0},
			{name: "HPO MIX HPVOL Switch", level: 0},
			{name: "HPVOL Playback Switch", level: 0},
		}
}

// jackState is the kernel's headphone jack switch: 1 while something is plugged in. Only read where
// there is a jack.
const jackState = "/sys/class/switch/h2w/state"

// jackPoll is how often the jack switch is sampled.
const jackPoll = 500 * time.Millisecond

// DetectOutput picks the output to use. A board with no jack, or a missing switch, is the speaker.
func DetectOutput() Output {
	if !HasJack {
		return OutputSpeaker
	}
	b, err := os.ReadFile(jackState)
	if err != nil || strings.TrimSpace(string(b)) == "0" {
		return OutputSpeaker
	}
	return OutputHeadphone
}

// VolumeSteps is the number of volume steps. The range is config's, since that is what a stored
// volume is in.
const VolumeSteps = config.VolumeSteps

// volumeCurves maps a volume step to attenuation in dB. With the amplifier out of safe mode this
// speaker is loud: the Dot's vendor curve, which reaches unity and bunches its top half into 8 dB,
// put "fricking loud" at half the dial in a small room (2026-09-15). This curve is linear in dB
// instead: about 1.5 dB a step up to half the dial (−45 → −24 dB), 1.2 dB a step above it
// (→ −6 dB), so half is a quiet conversation and the top is room-loud rather than painful.
var volumeCurves = map[Output][VolumeSteps + 1]float64{
	OutputSpeaker: {
		-90, -45, -43.5, -42, -40.5, -39, -37.5, -36, -34.5, -33,
		-31.5, -30, -28.5, -27, -25.5, -24, -22.8, -21.6, -20.4, -19.2,
		-18, -16.8, -15.6, -14.4, -13.2, -12, -10.8, -9.6, -8.4, -7.2, -6,
	},
	OutputHeadphone: {
		-90, -45, -43.5, -42, -40.5, -39, -37.5, -36, -34.5, -33,
		-31.5, -30, -28.5, -27, -25.5, -24, -22.8, -21.6, -20.4, -19.2,
		-18, -16.8, -15.6, -14.4, -13.2, -12, -10.8, -9.6, -8.4, -7.2, -6,
	},
}

// firstCurves are the volume curves for a tuned speaker, by tuning set, in dB in front of the
// tuning rather than behind it. The vendor's own chain turns the volume down first and then applies
// the EQ and the compressor ("Playback": AVL, UserEQ, EQ, MBCL in AFE.cfg), so the compressor sees
// music as loud as the dial makes it. Behind the tuning, it sees full-scale music at every volume
// and pulls the mids down by 30 dB on each kick drum, which is music that sounds flat and pumps with
// the bass (issue #81).
//
// Each step is as loud as volumeCurves' step was with the volume behind the tuning, worked out
// offline from each set's own files on three stations' worth of real music, six clips leveled to
// -14 dBFS RMS (about where Spotify and Music Assistant normalize), matching their average loudness
// (TestVolumeInFrontKeepsTheLoudness checks it). The old order squashed every song to much the same level, so a song
// mastered quieter than that now plays quieter, as it would anywhere else. The jumps are where the
// vendor's EQ changes filter (steps 13 and 25 on the Show 5, step 10 on the Show 8): the loudness
// moved there before too.
var firstCurves = map[string][VolumeSteps + 1]float64{
	"show": {
		-90, -64.3, -62.8, -61.3, -59.8, -58.3, -56.8, -55.3, -53.8, -52.3,
		-50.8, -49.3, -47.8, -44.7, -43.2, -41.7, -40.5, -39.3, -38.1, -36.9,
		-35.7, -34.5, -33.2, -32, -30.8, -27.8, -26.6, -25.2, -23.5, -21.6, -19.5,
	},
	"crown": {
		-90, -63.9, -62.4, -60.9, -59.4, -57.9, -56.4, -54.9, -53.4, -51.9,
		-48.7, -47.2, -45.7, -44.2, -42.7, -41.2, -39.3, -38.1, -36.9, -35.7,
		-34.5, -33.3, -32.6, -31.4, -30.2, -29, -27.7, -26.5, -25.1, -23.7, -21.9,
	},
}

// mute is the attenuation the curves use for step 0.
const mute = -90

// gainForStep converts a volume step to a linear gain using the output's curve.
func gainForStep(out Output, step int) float32 {
	curve, ok := volumeCurves[out]
	if !ok {
		curve = volumeCurves[OutputSpeaker]
	}
	step = max(0, min(step, VolumeSteps))

	db := curve[step]
	if db <= mute {
		return 0
	}
	return float32(math.Pow(10, db/20))
}

// MediaService is the init service that owns Android's audio HAL on LineageOS.
const MediaService = "vendor.audio-hal"

// AmpSwitch is empty on both generations (for the 1st gen, see initSequence): on cronos
// Ext_Speaker_Amp_Switch drives the GPIO wired to the MAX98396's
// reset, so switching it off and on resets the amplifier and wipes the register setup the codec
// driver did at probe — which it never repeats, leaving the speaker silent until a reboot
// (found 2026-09-14). The amplifier is left as the kernel brought it up.
const AmpSwitch = ""

// OutputBoost is make-up gain on everything the speaker plays, before the volume curve and the
// limiter. Unity: the quiet output that once seemed to need it was the amplifier's safe mode
// (see initSequence), and speech has its own normalizer.
const OutputBoost = 1.0

// DriverTuning applies the vendor driver's volume-dependent EQ and limiter (lib/asp), read from the
// unit's own vendor partition. Both generations of Show 5 declare the same four filters and the same
// compressor in their AFE.cfg ("Cronos" and "Checkers"), which lib/asp knows as asp.Show; a unit
// whose files are missing or are a set we do not know says so and plays untuned.
const DriverTuning = true

// HasJack is whether the device has a headphone jack, and so the Audio output choice: the 1st gen
// Show 5 does; the 2nd gen Show 5 has none, and the Show 8 is not known to. The kernel's jack switch
// has to be there as well: a board told apart by the mute driver alone, with no panel name, could be
// a Show 8 taken for a 1st gen Show 5.
var HasJack = layout.Checkers() && exists(jackState)

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// HasBoth is whether the speaker and the jack can play at once, and so the Both choice: wherever
// there is a jack, since the speaker is the line-out and the jack the headphone pins, and the codec
// drives both from the one mixer (heard on a 1st gen 2026-10-03).
var HasBoth = HasJack
