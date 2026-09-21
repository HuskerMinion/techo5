//go:build spot

package speaker

import (
	"math"
	"os"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Output is one of the device's audio outputs: the speaker, or the 3.5 mm jack.
type Output string

const (
	OutputSpeaker   Output = "speaker"
	OutputHeadphone Output = "headphone"
)

// The playback ring: the Show's, since it is the same LineageOS kernel and AFE driver.
const (
	period  = 768
	periods = 4
)

// DRAMHold keeps the DL1 driver off its SRAM ring, which faults on this kernel build on the Show
// (paths_cronos.go). Held on the Spot as a precaution until the SRAM path is proven safe here.
const DRAMHold = "/dev/snd/pcmC0D1c"

// A mixer write, as the shared player applies it.
type kctl struct {
	name  string
	value string
	level int32
	blob  []byte
}

// The Spot plays through a TLV320AIC32x4 DAC and an external amplifier, like the Dot 2, and its codec
// takes the Dot's sequences almost word for word. The values are the Spot's own, from Fire OS 5.5.6.9's
// /system/etc/audio_device.xml. Measured on LineageOS 18.1: nothing is heard until
// Ext_Speaker_Amp_Switch is On, and the codec already carries this speaker path at boot.
var initSequence = []kctl{
	{name: AmpSwitch, value: "Off"},
	{name: "Audio_LineOut_Setting", value: "Off"},
	{name: "Ignore Ramp Up", value: "Off"},
	{name: driverGain, level: 0},
	{name: "HPL Output Mixer L_DAC Switch", level: 1},
	{name: "HPR Output Mixer R_DAC Switch", level: 1},
}

// headphoneEQ is Fire OS's filter chain for the jack: six unity blocks and one tuned filter, the same
// coefficients the Dot's speaker uses.
var headphoneEQ = []byte{
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
		{name: "Audio_LineOut_Setting", value: "Off"},
		{name: "Right Channel Only", value: "On"},
		{name: driverGain, level: 9},
		{name: "PCM Playback Volume", level: 127},
		{name: "Amp Fault Enable", value: "On"},
	},
	OutputHeadphone: {
		{name: "biquad coefficients", blob: headphoneEQ},
		{name: "DRC Control", value: "Disabled"},
		{name: "Ignore Ramp Up", value: "On"},
		{name: driverGain, level: 11},
		{name: "PCM Playback Volume", level: 127},
		{name: "Right Channel Only", value: "Off"},
	},
}

// headphoneOff is Fire OS's ext_headphone_output turnoff sequence.
var headphoneOff = []kctl{
	{name: "Audio_LineOut_Setting", value: "Off"},
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

// VolumeSteps is the number of volume steps. The range is config's, since that is what a stored
// volume is in.
const VolumeSteps = config.VolumeSteps

// volumeCurves maps a volume step to attenuation in dB. Not yet tuned on the Spot: the Show's curve,
// linear in dB and topping out 6 dB under unity, which starts quiet rather than loud.
var volumeCurves = map[Output][VolumeSteps + 1]float64{
	OutputSpeaker: {
		-90, -45, -43.5, -42, -40.5, -39, -37.5, -36, -34.5, -33,
		-31.5, -30, -28.5, -27, -25.5, -24, -22.8, -21.6, -20.4, -19.2,
		-18, -16.8, -15.6, -14.4, -13.2, -12, -10.8, -9.6, -8.4, -7.2, -6,
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

// MediaService is the init service that owns Android's audio HAL on LineageOS.
const MediaService = "vendor.audio-hal"

// AmpSwitch gates the speaker. On the Spot it is safe to switch, unlike the Show's.
const AmpSwitch = "Ext_Speaker_Amp_Switch"

// OutputBoost is make-up gain on everything the speaker plays. Unity until measured.
const OutputBoost = 1.0

// DriverTuning applies the vendor driver's EQ and limiter (lib/asp), read from the unit's own vendor
// partition. The Spot ("Rook") has one filter for every volume rather than a set of them, half the
// length of the other devices', and a compressor chosen by power mode — lib/asp knows it as asp.Spot.
// A unit whose files are missing says so and plays untuned.
const DriverTuning = true
