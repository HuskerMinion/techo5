//go:build dot

package speaker

import (
	"math"
	"os"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Output is one of the device's audio outputs.
type Output string

const (
	OutputSpeaker   Output = "speaker"
	OutputHeadphone Output = "headphone"
	// OutputBoth is the speaker and the jack at once, which this board does not do (HasBoth).
	OutputBoth Output = "both"
)

// The mixer sequences below come direct from the device.
type kctl struct {
	name  string
	value string
	level int32
	blob  []byte
	// ifPresent writes the control only on a unit that has it: a part that differs between units of
	// the same model, rather than one that should always be there.
	ifPresent bool
}

var initSequence = []kctl{
	{name: AmpSwitch, value: "Off"},
	{name: "Audio_DacMux_Setting", value: "On"},
	{name: "Ignore Ramp Up", value: "Off"},
	{name: driverGain, level: 0},
	{name: "biquad coefficients", blob: speakerEQ},
}

// speakerEQ is the DAC's filter chain: six unity blocks and one tuned filter, which is the vendor's
// tuning for this speaker. The coefficients read back as zeros until something writes them.
var speakerEQ = []byte{
	128, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	128, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	128, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	128, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	128, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	128, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	127, 247, 0, 0, 128, 9, 0, 0, 127, 239, 0, 0, 0, 17, 0, 0,
	0, 17, 0, 0, 127, 222, 0, 0, 15, 0, 0,
}

var pathSequence = map[Output][]kctl{
	OutputSpeaker: {
		{name: "HPL Output Mixer L_DAC Switch", level: 1},
		{name: "HPR Output Mixer R_DAC Switch", level: 1},
		{name: "Audio_DacMux_Setting", value: "Off"},
		{name: "Right Channel Only", value: "On"},
		{name: driverGain, level: 6},
	},
	OutputHeadphone: {
		// The DAC reaches the jack through these too, and the codec boots with them off: without them
		// here, a Dot that starts on the jack is silent until the speaker path has run once.
		{name: "HPL Output Mixer L_DAC Switch", level: 1},
		{name: "HPR Output Mixer R_DAC Switch", level: 1},
		{name: "Ignore Ramp Up", value: "On"},
		{name: driverGain, level: 11},
		{name: "Audio_DacMux_Setting", value: "On"},
		{name: "Right Channel Only", value: "Off"},
	},
}

// headphoneOff is the ext_headphone_output turnoff sequence.
var headphoneOff = []kctl{
	{name: "Audio_DacMux_Setting", value: "Off"},
	{name: "Right Channel Only", value: "On"},
	{name: "Ignore Ramp Up", value: "Off"},
}

const driverGain = "HP Driver Gain Volume"

// jackState is the kernel's headphone jack switch: 1 while something is plugged in.
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

// VolumeSteps is the number of volume steps, matching the vendor's curves. The range is config's,
// since that is what a stored volume is in.
const VolumeSteps = config.VolumeSteps

// The vendor's volume curves, direct from the device: index is the volume step, value is
// attenuation in dB.
var volumeCurves = map[Output][VolumeSteps + 1]float64{
	OutputSpeaker: {
		-90, -33, -30, -26, -25, -23, -21, -19, -17, -16,
		-14, -13, -12, -10, -9, -8, -7, -5, -5, -4,
		-4, -4, -4, -3, -3, -3, -3, -3, -2, -1, 0,
	},
	OutputHeadphone: {
		-100, -39, -38, -36, -34, -33, -31, -29, -27, -26,
		-25, -24, -23, -21, -20, -19, -18, -17, -15, -14,
		-13, -12, -11, -9, -8, -7, -6, -4, -3, -2, 0,
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

// The playback ring on the Dot.
const (
	period  = 1024
	periods = 4
)

// DRAMHold is unused on the Dot: its DL1 driver's SRAM ring works.
const DRAMHold = ""

// MediaService is the init service that owns Android's audio HAL on Fire OS.
const MediaService = "media"

// AmpSwitch gates the speaker.
const AmpSwitch = "Ext_Speaker_Amp_Switch"

// OutputBoost is make-up gain on the output; the Dot's vendor tuning already carries its own.
const OutputBoost = 1.0

// DriverTuning applies the vendor driver's volume-dependent EQ and limiter (lib/asp), read from the
// vendor partition: the files it reads are the Dot's.
const DriverTuning = true

// firstCurves puts the volume in front of the tuning, as on the Show (paths_cronos.go, which says
// why and how these were worked out). The Dot's AFE.cfg has no volume stage of its own, so Android
// turned the volume down before the tuning, which is where this puts it too.
//
// Up to step 16 each step is as loud as it was. Above it, the old curve's repeated values left
// steps no louder than the one below (17 and 18, 19 to 21), so from 16 to 30 the loudness now rises
// evenly instead, to the same top. The gain itself drops at 22, 25 and 28, where the vendor's EQ
// moves to a louder filter: the loudness still rises there.
var firstCurves = map[string][VolumeSteps + 1]float64{
	"dot": {
		-90, -42.1, -38.9, -34.7, -33.6, -31.6, -29.5, -27.5, -25.5, -24.4,
		-22.3, -21.3, -20.2, -18, -16.9, -15.8, -14.5, -14, -13.4, -12.8,
		-12.2, -11.6, -14.5, -13.8, -13.1, -16.4, -15.5, -14.6, -20.6, -19.5, -18.2,
	},
}

// HasJack is whether the device has a headphone jack, and so the Audio output choice.
const HasJack = true

// HasBoth is whether the speaker and the jack can play at once, and so the Both choice. Not here:
// the routes have not been tried together on this board.
const HasBoth = false
