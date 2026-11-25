//go:build dot && cannoli

package speaker

import (
	"math"
	"slices"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/asp"
)

// Output is one of the device's audio outputs. The Echo Dot 5th gen has the speaker alone (no jack this
// generation), so OutputHeadphone and OutputBoth only name the same path for the code that asks.
type Output string

const (
	OutputSpeaker   Output = "speaker"
	OutputHeadphone Output = "headphone"
	OutputBoth      Output = "both"
)

type kctl struct {
	name  string
	value string
	level int32
	blob  []byte
	// ifPresent writes the control only on a unit that has it.
	ifPresent bool
}

// The Echo Dot 5th gen (cannoli) plays out of the MT8519's AFE on its card mt-snd-card (Amazon's
// mendocino machine driver, sound/soc/mediatek/mt8519/mendocino.c in the 6.5.5.5 GPL tree): the DL2
// memory interface (DAPM inputs I40/I41) through the interconnect into O04/O05, which feed the eTDM2
// output, an I2S link at a fixed 96 kHz, two channels, 32 bits (the DVT device tree's etdm2-out-fix-*)
// to a Maxim MAX98396 mono amplifier at i2c 3-0038. DL3 (I20/I21) reaches O04/O05 as well; DL2 is the
// one taken, and nothing else is routed there.
//
// Read on one unit (2026-11-21, slot-B Linux with cannoli_hwfixup): DL2 Playback is the
// card's playback device 1; the interconnect switches are "O04 I40 Switch" and "O05 I41 Switch", off from
// boot; the amplifier's controls are "Speaker Volume A" (11 from boot), "Digital Volume A" (127) and
// "Speaker Safe Mode A" (1, on, from boot); and a tone through DL2 at 96 kHz plays from the speaker.
// Which of eTDM2's two slots the mono amplifier plays is not known (both are fed the same).
//
// The MAX98396 comes up in "Speaker Safe Mode", which takes about 30 dB off on the 2nd gen Show 5's
// MAX98396 (paths_cronos.go): cleared at start where the control is there.
var initSequence = []kctl{
	{name: "Speaker Safe Mode A", level: 0, ifPresent: true},
	{name: "O04 I40 Switch", level: 1},
	{name: "O05 I41 Switch", level: 1},
	{name: "O04 I20 Switch", level: 0, ifPresent: true},
	{name: "O05 I21 Switch", level: 0, ifPresent: true},
}

// cannoliSpeaker is the one path: the amplifier's own gain at the Speaker gain setting.
var cannoliSpeaker = []kctl{
	{name: ampLevel, level: ampUnity},
}

var pathSequence = map[Output][]kctl{
	OutputSpeaker:   cannoliSpeaker,
	OutputHeadphone: cannoliSpeaker,
	OutputBoth:      cannoliSpeaker,
}

// headphoneOff is nothing: there is no jack to leave.
var headphoneOff = []kctl{}

// ampLevel is the MAX98396's speaker gain, 0 to 17 for +4 to +21 dB at 1 dB a step (the kernel's TLV,
// read on the unit by echo-dot-assist, 2026-11-15); ampUnity is where Fire OS's normal playback path
// leaves it (+14 dB), and ampMax is +6 dB over that, as far as anyone has listened.
const (
	ampLevel = "Speaker Volume A"
	ampUnity = 10
	ampMax   = 16
)

// gainOnPath is on: the Speaker gain setting is the amplifier's own level, raised from ampUnity.
const gainOnPath = true

// withSpeakerGain is seq, out's path, with the Speaker gain in it.
func withSpeakerGain(seq []kctl, _ Output, db int) []kctl {
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

// ampFor is the amplifier's level for a Speaker gain of db dB, 1 dB a step.
func ampFor(db int) int32 {
	db = max(0, min(db, config.SpeakerGainMax))
	return int32(min(ampUnity+db, ampMax))
}

// jackPoll is how often the jack would be sampled; there is none (DetectOutput).
const jackPoll = time.Second

// DetectOutput is always the speaker: there is no jack.
func DetectOutput() Output { return OutputSpeaker }

// VolumeSteps is the number of volume steps; the range is config's.
const VolumeSteps = config.VolumeSteps

// volumeCurves is Fire OS's own speaker music curve on this unit (8349 system.img): VolumeCurves.xml's
// <speaker><music> gives each of the 31 steps an index from 0 to 100 (0,5,8,15,20,26,32,40,45,51,56,60,
// 65,68,70,73,78,82,87,89,90..100), and audio_policy's DEFAULT_DEVICE_CATEGORY_SPEAKER_VOLUME_CURVE
// turns an index into dB between its points 1:-58, 20:-40, 60:-17 and 100:0, linearly. AFE.cfg has no
// music_volTab here (lib/asp), and VolumeGainTuning's per-speaker-vendor offsets (at most about +-5 dB a
// step) are left out. Listened to on one unit against the Dot 3's (2026-11-21): this one is
// better.
var volumeCurves = map[Output][VolumeSteps + 1]float64{
	OutputSpeaker:   cannoliCurve,
	OutputHeadphone: cannoliCurve,
	OutputBoth:      cannoliCurve,
}

var cannoliCurve = [VolumeSteps + 1]float64{
	-90, -54.21, -51.37, -44.74, -40, -36.55, -33.1, -28.5, -25.63, -22.18,
	-19.3, -17, -14.88, -13.6, -12.75, -11.48, -9.35, -7.65, -5.53, -4.68,
	-4.25, -3.83, -3.4, -2.98, -2.55, -2.13, -1.7, -1.28, -0.85, -0.43, 0,
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

// The playback ring: six periods of 1024 frames at 96 kHz, 64 ms, inside HardwareTail. The MT8519's
// DL2 buffer size has not been read; the Dot 3's 48 KB rule (paths_donut.go) does not apply to this
// AFE, but the same size keeps clear of it anyway.
const (
	period  = 1024
	periods = 6
)

// DRAMHold is unused here.
const DRAMHold = ""

// startFull is off: no restart storm has been seen on this board.
const startFull = false

// softRef is on as a fallback: the capture reads the playback looped back on UL9 (mic/device_cannoli.go)
// and turns the software reference off (echoref.Use), so this player only feeds it where that loopback's
// routes could not be set.
const softRef = true

// speakerFormat is eTDM2's own rate, 96 kHz, so the AFE has nothing to convert on the way out; the FE
// gets 16-bit samples and the back end widens them to its 32-bit slots.
var speakerFormat = cardFormat{rate: 2 * Rate, bits: Bits, open: initSequence[1:3]}

// formatFor is the one format, whatever the output is called.
func formatFor(Output) cardFormat { return speakerFormat }

// MediaService is the init service that owns the playback device on Fire OS: Amazon's mixer daemon.
const MediaService = "mixer"

// AmpSwitch is empty: the MAX98396 is powered with the stream by its driver.
const AmpSwitch = ""

// MuteSwitch is empty: no separate amplifier mute.
const MuteSwitch = ""

// idleGate is none: nothing on this card to put to sleep (idle.go).
var idleGate *ampGate

// mayHaveTAS5805M is false: the amplifier is a MAX98396 (the DT's alternative TI TAS2764 is disabled).
const mayHaveTAS5805M = false

// OutputBoost is make-up gain on the output.
const OutputBoost = 1.0

// DriverTuning is on: Fire OS's own chain for this board (lib/asp iir.go, asp.Cannoli), read from the
// unit's /vendor/etc/audio-algorithms, which the boot script adopts from system_a into the store. Without
// it the speaker plays flat: no bass lift, no presence cut, and nothing holding the driver.
const DriverTuning = true

// tuningSet is asp.Cannoli, by name: the directory also holds an EQ_100.cfg its AFE.cfg never names.
var tuningSet = &asp.Cannoli

// firstCurves puts the volume in front of the tuning, where Fire OS has it (audio policy turns the
// music down, then the vendor's chain lifts and limits it): the same curve as untuned, so a step is as
// loud tuned or not in the mids, and the limiters see the level they were set for.
var firstCurves = map[string][VolumeSteps + 1]float64{"cannoli": cannoliCurve}

// HasJack is false: the Dot 5 has no headphone jack.
const HasJack = false

// HasBoth is false: one output.
const HasBoth = false

// PlaybackDevice is DL2 Playback on mt-snd-card (see initSequence).
const (
	PlaybackDevice = 1
	hwTrimDB       = 0.0
)

// ownVolumeTable is false: only the 2nd gen Shows read their AFE.cfg's table (paths_cronos.go).
func ownVolumeTable() bool { return false }
