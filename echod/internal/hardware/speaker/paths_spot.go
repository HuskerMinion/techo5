//go:build spot

package speaker

import (
	"math"
	"os"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/asp"
)

// Output is one of the device's audio outputs: the speaker, or the 3.5 mm jack.
type Output string

const (
	OutputSpeaker   Output = "speaker"
	OutputHeadphone Output = "headphone"
	// OutputBoth is the speaker and the jack at once, which this board does not do (HasBoth).
	OutputBoth Output = "both"
)

// The playback ring: the Show's, since it is the same LineageOS kernel and AFE driver.
const (
	period  = 768
	periods = 4
)

// DRAMHold keeps the DL1 driver off its SRAM ring, which faults on this kernel build on the Show
// (paths_cronos.go). Held on the Spot as a precaution until the SRAM path is proven safe here.
const DRAMHold = "/dev/snd/pcmC0D1c"

// startFull is the Show's (paths_cronos.go): the same DL1 driver and the same restart after an
// underrun.
const startFull = true

// softRef is off: the card's own loopback is the echo reference.
const softRef = false

// openSequence is empty: the playback device opens with the card as the kernel left it.
var openSequence []kctl

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
		// The jack is the line-out: Fire OS's HAL turns it on in code, not in audio_device.xml, and
		// without it nothing is heard there (#100).
		{name: "Audio_LineOut_Setting", value: "On"},
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

// volumeCurves maps a volume step to attenuation in dB. The speaker's is the Dot's vendor curve, which
// the Spot's codec and speaker are closest to, 6 dB down: it is what plays when the
// tuning is off or missing, with no limiter behind it, and it stops where the Spot's untuned curve
// always has. Tuned, the speaker follows firstCurves instead, which is worked out from the vendor curve
// itself. The Show's curve, which the Spot had before, is made for the Show's much louder amplifier
// and left a tuned Spot at half the dial about 16 dB quieter than Fire OS (#95).
var volumeCurves = map[Output][VolumeSteps + 1]float64{
	OutputSpeaker: {
		-90, -39, -36, -32, -31, -29, -27, -25, -23, -22,
		-20, -19, -18, -16, -15, -14, -13, -11, -11, -10,
		-10, -10, -10, -9, -9, -9, -9, -9, -8, -7, -6,
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

// MuteSwitch is a mute on the amplifier besides AmpSwitch: none on the Spot.
const MuteSwitch = ""

// idleGate is none: the amplifier is AmpSwitch's to gate (idle.go).
var idleGate *ampGate

// mayHaveTAS5805M is whether a board of this build can carry the TAS5805M amplifier (GainAvailable):
// the Spot plays through its TLV320AIC32x4 and an external amplifier gated by AmpSwitch.
const mayHaveTAS5805M = false

// OutputBoost is make-up gain on everything the speaker plays. Unity until measured.
const OutputBoost = 1.0

// DriverTuning applies the vendor driver's EQ and limiter (lib/asp), read from the unit's own vendor
// partition. The Spot ("Rook") has one filter for every volume rather than a set of them, half the
// length of the other devices', and a compressor chosen by power mode — lib/asp knows it as asp.Spot.
// A unit whose files are missing says so and plays untuned.
const DriverTuning = true

// tuningSet is the tuning to load by name, where the files cannot tell it: nil, the Spot's can.
var tuningSet *asp.Set

// firstCurves puts the volume in front of the tuning, as on the Show (paths_cronos.go, which says
// why and how these were worked out). The Spot's AFE.cfg has no volume stage of its own, so Android
// turned the volume down before the tuning, which is where this puts it too. Worked out from a Spot's
// own files the same way as the Dot's: each step up to 16 as loud as the vendor curve's step was behind
// the tuning (volumeCurves 6 dB up), and from 16 to 30 rising evenly to the same top, since the vendor
// curve's repeated values leave steps there no louder than the one below.
var firstCurves = map[string][VolumeSteps + 1]float64{
	"spot": {
		-90, -45.8, -42.8, -38.9, -37.9, -35.8, -33.8, -31.7, -28.9, -27.3,
		-24, -22.3, -20.6, -17.6, -16.2, -14.9, -13.5, -12.8, -12.1, -11.4,
		-10.6, -9.8, -9, -8.1, -7.1, -6.1, -4.9, -3.7, -2.5, -1.3, 0,
	},
}

// HasJack is whether the device has a headphone jack, and so the Audio output choice.
const HasJack = true

// HasBoth is whether the speaker and the jack can play at once, and so the Both choice. Not here:
// the routes have not been tried together on this board.
const HasBoth = false

// PlaybackDevice is the codec's playback PCM, which the card takes at Rate as it is: hwRate is Rate
// and nothing is trimmed after the volume (see paths_cronos.go for the board where neither holds).
const (
	PlaybackDevice = 23
	hwRate         = Rate
	hwTrimDB       = 0.0
	hwBits         = Bits
)
