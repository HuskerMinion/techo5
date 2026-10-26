//go:build dot && donut

package speaker

import (
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/asp"
)

// Output is one of the device's audio outputs.
type Output string

const (
	OutputSpeaker   Output = "speaker"
	OutputHeadphone Output = "headphone"
	// OutputBoth is the speaker and the jack at once, which this board does not do (HasBoth).
	OutputBoth Output = "both"
)

// The mixer sequences below come from the device: Fire OS's mixer_paths.xml on the Echo Dot 3rd gen
// (donut), unit 1, 2026-10-23.
type kctl struct {
	name  string
	value string
	level int32
	blob  []byte
	// ifPresent writes the control only on a unit that has it.
	ifPresent bool
}

// The Dot 3 plays out of the MT8516's AFE two ways, both from the AFE's O03/O04: over I2S to a TI
// TAS2770 amplifier on the speaker, or through the SoC's own codec to the jack, fed from DL1 (I05/I06)
// as Fire OS feeds them. (DL2, I07/I08, reaches the jack's codec and the echo reference but not the
// amplifier: on unit 1 a tone on it was quieter than the room.) On the jack both back ends are
// connected (formatFor; on the speaker the jack's is off) and the path picks the output by muting the
// other: the amplifier's digital volume (PCM Playback Volume) is at
// full on the speaker's path and zero on the jack's, which is how Fire OS keeps the speaker quiet while
// the jack plays, and the jack's mux is open on the speaker's. The amplifier's own level is DAC
// Playback Volume, 16 of 0 to 20 from boot, 11 to 21 dBV full scale at 0.5 dB a step. Found by
// echo-dot-assist (Gamer92000, MIT: devices/donut/README.md) and read off the unit.
var initSequence = []kctl{
	{name: "LINEOUT Ext Amp Switch", level: 0},
	{name: "HP Ext Amp Switch", level: 0},
	{name: "O03 I05 Switch", level: 1},
	{name: "O04 I06 Switch", level: 1},
	{name: "O03 I07 Switch", level: 0},
	{name: "O04 I08 Switch", level: 0},
	// The back ends (INT ADDA and I2S O03_O04) are the format's (formatFor): written here, the jack's
	// would join a stream already open at the speaker's 96 kHz, which its codec refuses.
}

var pathSequence = map[Output][]kctl{
	OutputSpeaker: {
		{name: "Audio Amp Playback Volume", level: 0},
		{name: "PCM Playback Volume", level: 255},
		{name: ampLevel, level: ampUnity},
		{name: "LINEOUT Mux", value: "VOICE_AMP"},
		{name: "HPOUT Mux", value: "OPEN"},
	},
	OutputHeadphone: {
		{name: "Audio Amp Playback Volume", level: 3},
		{name: "PCM Playback Volume", level: 0},
		{name: "LINEOUT Mux", value: "OPEN"},
		{name: "HPOUT Mux", value: "AUDIO_AMP"},
	},
}

// headphoneOff takes the jack's path down when it is left.
var headphoneOff = []kctl{
	{name: "HPOUT Mux", value: "OPEN"},
}

// ampLevel is the TAS2770's level, 0.5 dB a step; ampUnity is where the board boots it and where Fire
// OS leaves it.
const (
	ampLevel = "DAC Playback Volume"
	ampUnity = 16
	ampMax   = 20
)

// gainOnPath is whether the Speaker gain setting is written with the speaker's path: here it is the
// amplifier's own level, raised from ampUnity by up to 2 dB, which is all the control has above it.
const gainOnPath = true

// withSpeakerGain is seq, out's path, with the Speaker gain in it.
func withSpeakerGain(seq []kctl, out Output, db int) []kctl {
	if out != OutputSpeaker {
		return seq
	}
	gained := slices.Clone(seq)
	for i := range gained {
		if gained[i].name == ampLevel {
			gained[i].level = ampFor(db)
		}
	}
	return gained
}

// speakerGainControls is what a change of the Speaker gain writes while the speaker plays.
func speakerGainControls(db int) []kctl {
	return []kctl{{name: ampLevel, level: ampFor(db)}}
}

// ampFor is the amplifier's level for a Speaker gain of db dB.
func ampFor(db int) int32 {
	db = max(0, min(db, config.SpeakerGainMax))
	return int32(min(ampUnity+2*db, ampMax))
}

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

// VolumeSteps is the number of volume steps; the range is config's.
const VolumeSteps = config.VolumeSteps

// volumeCurves are the board's volume curves: index is the volume step, value is attenuation in dB.
//
// The speaker's low end is its own. The Dot 2's curve, which it started with, has its first step at
// -33 dB, and through the Dot 3's TAS2770 that was still far from quiet (the user, 2026-10-25, on
// dot-v1.0.33). Fire OS's files carry no curve to read: Amazon set the volume in its own audio service.
// So the first step is -55 dB, and the low steps come up 3 to 4 dB at a time to meet the Dot 2's
// curve at step 12; from there up it is the Dot 2's, smoothed where that one repeats a value.
var volumeCurves = map[Output][VolumeSteps + 1]float64{
	OutputSpeaker: {
		-90, -55, -51, -47, -43, -39, -35, -31, -27, -23,
		-20, -17, -14, -12, -10, -9, -8, -7, -6, -5,
		-4.5, -4, -3.5, -3, -2.5, -2, -1.5, -1, -0.5, -0.25, 0,
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

// The playback ring: over 36 KB, always, and inside the quiet margin. The MT8516 AFE driver puts a DL1
// ring of 36 KB or less in its internal SRAM (mt8167-afe-pcm.c, max_sram_size), mapped as device
// memory, and zeroing it at the start of the stream (a memset that uses DC ZVA) takes an alignment
// fault and panics the kernel: a 4096-frame 16-bit ring (16 KB) did exactly that on unit 1, 2026-10-23,
// three times. The ring also has to stay under HardwareTail (150 ms), and at 48 kHz 16-bit both cannot
// hold. Both formats below make six periods of 1024 frames at Rate, 128 ms and 48 KB: the speaker's at
// 96 kHz 16-bit, the jack's at 48 kHz in 32-bit containers.
const (
	period  = 1024
	periods = 6
)

// DRAMHold is unused here.
const DRAMHold = ""

// startFull is off: no restart storm has been seen on this board.
const startFull = false

// softRef is on: the capture uses the card's DL1_AWB_Record where it opens, and otherwise asks for
// the software reference (echoref.InUse), which this player then feeds.
const softRef = true

// The card runs two ways, and the player opens it again when the output moves from one to the other.
//
// The speaker's: 96 kHz, 16-bit, the amplifier's I2S back end alone. At 48 kHz the TAS2770 turns the
// upper frequencies of speech and music into static (techo5-dot#20): a sweep is clean low and gritty at
// the top, and the same sentence that is clean on a Show crackles here, from echod and from aplay alike.
// At 96 kHz it is clean, as it was in the port's first build (donut.3, 2b9c706c): the user's bench unit
// played with static on every build from donut.8 (48 kHz S24_LE) to v1.0.32 and without it on donut.3,
// 2026-10-24. The jack's codec refuses 96 kHz, and with its back end connected DL1 will not open at that
// rate at all, so it is off here.
//
// The jack's: 48 kHz in 32-bit containers (S24_LE), both back ends connected, as the jack needs; the
// amplifier is silent on this path, so its static is not heard. Its digital volume goes to zero before
// the stream opens: the jack's path (pathSequence) is written only after setOutput's drain, and until
// then the amplifier would play the 48 kHz stream at full. DPCM refuses to prepare a
// front end with no back end enabled ("ASoC: no backend DAIs enabled for DL1 Playback", EINVAL), so the
// routes go on before the device opens.
var (
	speakerFormat = cardFormat{rate: 2 * Rate, bits: Bits, open: []kctl{
		{name: "O03 I05 Switch", level: 1},
		{name: "O04 I06 Switch", level: 1},
		{name: "INT ADDA O03_O04 Switch", level: 0},
		{name: "I2S O03_O04 Switch", level: 1},
	}}
	jackFormat = cardFormat{rate: Rate, bits: 32, open: []kctl{
		{name: "PCM Playback Volume", level: 0},
		{name: "O03 I05 Switch", level: 1},
		{name: "O04 I06 Switch", level: 1},
		{name: "INT ADDA O03_O04 Switch", level: 1},
		{name: "I2S O03_O04 Switch", level: 1},
	}}
)

// formatFor is the speaker's format on the speaker and the jack's on the jack.
func formatFor(out Output) cardFormat {
	if out == OutputSpeaker {
		return speakerFormat
	}
	return jackFormat
}

// MediaService is the init service that owns Android's audio HAL on Fire OS.
const MediaService = "media"

// AmpSwitch is empty: the TAS2770 has no gate of its own on this card; its digital volume carries the
// speaker's on and off (pathSequence).
const AmpSwitch = ""

// MuteSwitch is empty: no separate amplifier mute.
const MuteSwitch = ""

// idleGate is none: nothing on this card to put to sleep (idle.go).
var idleGate *ampGate

// mayHaveTAS5805M is false: the amplifier is a TAS2770.
const mayHaveTAS5805M = false

// OutputBoost is make-up gain on the output.
const OutputBoost = 1.0

// DriverTuning is off: the Dot 3's vendor tuning (audio-algorithms) is another set than the ones
// lib/asp reads, and it plays without it until it is.
const DriverTuning = false

// tuningSet is nil: no tuning is loaded by name here.
var tuningSet *asp.Set

// firstCurves is empty: without the tuning, the volume is volumeCurves as they are.
var firstCurves = map[string][VolumeSteps + 1]float64{}

// HasJack is whether the device has a headphone jack: the Dot 3 has one, on the SoC codec.
const HasJack = true

// HasBoth is whether the speaker and the jack can play at once: not tried, so not offered.
const HasBoth = false

// PlaybackDevice is DL1_Playback, at the output's format (formatFor). S24_LE on the jack's: the
// amplifier plays nothing of S32_LE, though it takes it (unit 1, 2026-10-23).
const (
	PlaybackDevice = 6
	hwTrimDB       = 0.0
)
