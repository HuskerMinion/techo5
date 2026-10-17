//go:build dot

package speaker

import (
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// The Echo 2nd gen (radar) plays through the Dot's codec, but its speaker has an amplifier of its own
// behind it, which the codec's MFP2 pin mutes. The kernel boots it muted and nothing of the Dot's
// touches it: with it On the speaker is silent, with it Off it plays (unit 1, 2026-10-16, the
// microphones 26 dB up on a sweep). Its paths are the unit's own audio_device.xml, as Fire OS's HAL
// writes them, with the filter coefficients read from that file rather than kept here.
//
// The order is the one that does not pop: the mute is held On while the path is set up and the
// filters written, the stream starts on silence, and only once the card is taking it (unmuteAfter)
// is the mute let go (Player.Run); after a path switch, a moment after the amplifier is switched on
// (muteSettle). It goes On again before every path switch, at close, and when playback fails.
// Enabling the amplifier with no path defined was reported to reboot the unit, which is why the
// release always comes last. Tested on a unit on 2026-10-16: the speaker plays through the gate, and
// no pop was heard at startup.
const radarMute = "MFP Gpio Mute"

// vendorMixerFiles are where the unit's mixer file is read from: the copy the boot script keeps in the
// store, then Fire OS's own on a unit still running it.
var vendorMixerFiles = []string{"/store/techo5/audio_device.xml", "/system/etc/audio_device.xml"}

// The paths, by the file's names for them.
const (
	speakerPath   = "ext_speaker_output"
	headphonePath = "ext_headphone_output"
)

// radarInit is the file's initial settings with the mute held, the amplifier off and the driver at
// zero. Close writes it too, which leaves the speaker muted.
var radarInit = []kctl{
	{name: radarMute, value: "On"},
	{name: AmpSwitch, value: "Off"},
	{name: "HPL Output Mixer L_DAC Switch", level: 1},
	{name: "HPR Output Mixer R_DAC Switch", level: 1},
	{name: "Audio_DacMux_Setting", value: "Off"},
	{name: "Ignore Ramp Up", value: "Off"},
	{name: "Right Channel Only", value: "Off"},
	{name: driverGain, level: 0},
}

// radarPaths are the file's turnon sequences, with the coefficients it carries for each. Unlike the
// Dot's, the speaker takes both channels (Right Channel Only Off). Ext_Speaker_Amp_Switch, last in
// the file's speaker path, is AmpSwitch, which the player turns on once the codec has settled.
//
// The jack is mono (the codec's Board Channel Config reads MonoRight) and needs Audio_DacMux_Setting
// On with a plug in, as the Dot's does. The speaker's mute stays On while it plays: it gates the
// speaker's amplifier, not the jack. Tested on a unit on 2026-10-16: the jack plays, and the output
// switches on every plug and unplug.
func radarPaths(speaker, jack []byte) map[Output][]kctl {
	return map[Output][]kctl{
		OutputSpeaker: {
			{name: "HPL Output Mixer L_DAC Switch", level: 1},
			{name: "HPR Output Mixer R_DAC Switch", level: 1},
			{name: "Audio_DacMux_Setting", value: "Off"},
			{name: "Right Channel Only", value: "Off"},
			{name: eqControl, blob: speaker},
			{name: "DRC Control", value: "Disabled"},
			{name: driverGain, level: 6},
		},
		OutputHeadphone: {
			{name: "HPL Output Mixer L_DAC Switch", level: 1},
			{name: "HPR Output Mixer R_DAC Switch", level: 1},
			{name: eqControl, blob: jack},
			{name: "DRC Control", value: "Disabled"},
			{name: "Ignore Ramp Up", value: "On"},
			{name: driverGain, level: 11},
			{name: "Audio_DacMux_Setting", value: "On"},
		},
	}
}

// radarHeadphoneOff is the file's ext_headphone_output turnoff sequence.
var radarHeadphoneOff = []kctl{
	{name: "Audio_DacMux_Setting", value: "Off"},
	{name: "Ignore Ramp Up", value: "Off"},
}

// radarCurves start from the Dot's: the speaker 6 dB quieter up to step 10, easing back to the Dot's
// curve by step 20, since testers found the low steps too loud. A first guess, to be measured; the
// jack is the Dot's as it is. With the tuning on the speaker follows firstCurves["radar"] instead,
// which keeps these loudnesses; these are what plays with the tuning off or missing.
var radarCurves = map[Output][VolumeSteps + 1]float64{
	OutputSpeaker: {
		-90, -39, -36, -32, -31, -29, -27, -25, -23, -22,
		-20, -18.4, -16.8, -14.2, -12.6, -11, -9.4, -6.8, -6.2, -4.6,
		-4, -4, -4, -3, -3, -3, -3, -3, -2, -1, 0,
	},
	OutputHeadphone: dotCurves[OutputHeadphone],
}

// radarEQ is the speaker's and the jack's filter chains from the first mixer file that reads. Where
// there is none, or it carries no usable chain for an output, that output plays through unityEQ: an
// all-zero chain is silence on this codec, and a wrong one could be anything.
func radarEQ(files []string) (speaker, jack []byte) {
	m, from, err := readMixerFile(files)
	if err != nil {
		slog.Warn("the unit's mixer file is not readable, the speaker and the jack play through flat filters", "err", err)
		return unityEQ(), unityEQ()
	}
	pick := func(path string) []byte {
		eq, err := m.coefficients(path)
		if err != nil {
			slog.Warn("no usable filter in the unit's mixer file, playing through a flat one", "file", from, "path", path, "err", err)
			return unityEQ()
		}
		return eq
	}
	speaker, jack = pick(speakerPath), pick(headphonePath)
	slog.Info("speaker filters read from the unit's mixer file", "file", from)
	return speaker, jack
}

// unityBiquad is one of the chain's biquads left flat, as the first six of the Dot's speakerEQ are.
var unityBiquad = []byte{128, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}

// eqBiquads is how many biquads open the chain; the block after them is the Dot's own closing filter.
const eqBiquads = 6

// unityEQ is the chain with its six biquads flat, closed by the block the Dot plays with, which is
// the one part of speakerEQ that is not unity.
func unityEQ() []byte {
	out := make([]byte, 0, len(speakerEQ))
	for range eqBiquads {
		out = append(out, unityBiquad...)
	}
	return append(out, speakerEQ[eqBiquads*len(unityBiquad):]...)
}

// mixerFile is a vendor audio_device.xml: the controls written once at start, then the sequences the
// HAL writes as each output is turned on and off.
type mixerFile struct {
	Paths []struct {
		Name     string         `xml:"name,attr"`
		Value    string         `xml:"value,attr"`
		Controls []mixerSetting `xml:"kctl"`
	} `xml:"path"`
}

type mixerSetting struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// readMixerFile parses the first of files that is there and reads, and says which it was.
func readMixerFile(files []string) (*mixerFile, string, error) {
	var errs []error
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		m, err := parseMixerFile(b)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f, err))
			continue
		}
		return m, f, nil
	}
	if len(errs) == 0 {
		return nil, "", errors.New("no mixer file to read")
	}
	return nil, "", errors.Join(errs...)
}

func parseMixerFile(b []byte) (*mixerFile, error) {
	var m mixerFile
	if err := xml.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// coefficients is the filter chain the path writes as it turns on: as long as the Dot's, every value a
// byte, and not all zero. More than one in the path is a file we do not understand.
func (m *mixerFile) coefficients(path string) ([]byte, error) {
	var found []string
	for _, p := range m.Paths {
		if p.Name != path || p.Value != "turnon" {
			continue
		}
		for _, c := range p.Controls {
			if c.Name == eqControl {
				found = append(found, c.Value)
			}
		}
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("%s turns on with no %s", path, eqControl)
	case 1:
	default:
		return nil, fmt.Errorf("%s writes %s %d times", path, eqControl, len(found))
	}

	fields := strings.Fields(found[0])
	if len(fields) != len(speakerEQ) {
		return nil, fmt.Errorf("%s: %d coefficient bytes, expected %d", path, len(fields), len(speakerEQ))
	}
	out := make([]byte, len(fields))
	zero := true
	for i, f := range fields {
		v, err := strconv.ParseUint(f, 10, 8)
		if err != nil {
			return nil, fmt.Errorf("%s: coefficient %d is %q, not a byte", path, i, f)
		}
		out[i] = byte(v)
		zero = zero && v == 0
	}
	if zero {
		return nil, fmt.Errorf("%s: the coefficients are all zero", path)
	}
	return out, nil
}
