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

// Output is one of the device's audio outputs. The Echo Show 5 has a speaker and no jack, so
// OutputHeadphone exists only so the shared code compiles; DetectOutput never returns it.
type Output string

const (
	OutputSpeaker   Output = "speaker"
	OutputHeadphone Output = "headphone"
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
var initSequence = showInit(layout.Checkers())

func showInit(checkers bool) []kctl {
	if !checkers {
		return []kctl{{name: "Speaker Safe Mode A", level: 0}}
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

var pathSequence = map[Output][]kctl{
	OutputSpeaker:   {},
	OutputHeadphone: {},
}

var headphoneOff = []kctl{}

// jackState is where a headphone switch would be; there is none, so the read fails and the speaker
// is assumed.
const jackState = "/sys/class/switch/h2w/state"

// jackPoll is how often the jack switch is sampled.
const jackPoll = 500 * time.Millisecond

// DetectOutput picks the output to use. A missing switch means no jack detection, so assume the
// speaker.
func DetectOutput() Output {
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
// (see initSequence), and speech has its own normaliser.
const OutputBoost = 1.0

// DriverTuning is off: lib/asp reads the Dot's tuning files. The Echo Show 5's vendor partition holds a
// different set, so it plays as it always has, without that stage.
const DriverTuning = false
