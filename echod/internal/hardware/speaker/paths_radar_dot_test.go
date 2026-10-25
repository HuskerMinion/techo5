//go:build dot && !donut

package speaker

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture is a made-up mixer file in the vendor's layout (testdata/audio_device.xml), and these are
// the counting patterns it carries.
var fixture = filepath.Join("testdata", "audio_device.xml")

func pattern(step, start int) []byte {
	out := make([]byte, len(speakerEQ))
	for i := range out {
		out[i] = byte((i*step + start) % 256)
	}
	return out
}

// Each output's chain comes from its own turnon path, byte for byte.
func TestMixerFileCoefficients(t *testing.T) {
	m, from, err := readMixerFile([]string{filepath.Join(t.TempDir(), "missing.xml"), fixture})
	if err != nil {
		t.Fatal(err)
	}
	if from != fixture {
		t.Errorf("read %s, want the first file that is there, %s", from, fixture)
	}
	for path, want := range map[string][]byte{speakerPath: pattern(7, 3), headphonePath: pattern(11, 5)} {
		got, err := m.coefficients(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: read %v, want %v", path, got, want)
		}
	}
}

// A chain that is not one we can write is refused, rather than written to the codec.
func TestMixerFileRefusesWhatItCannotWrite(t *testing.T) {
	file := func(paths string) string {
		return `<?xml version="1.0" ?><mixercontrol>` + paths + `</mixercontrol>`
	}
	path := func(value string) string {
		return `<path name="ext_speaker_output" value="turnon"><kctl name="biquad coefficients" value="` + value + `" /></path>`
	}
	bytesOf := func(n int, v string) string { return strings.TrimSpace(strings.Repeat(v+" ", n)) }

	cases := map[string]string{
		"no path":     file(`<path name="ext_headphone_output" value="turnon"><kctl name="biquad coefficients" value="1" /></path>`),
		"turnoff":     file(`<path name="ext_speaker_output" value="turnoff"><kctl name="biquad coefficients" value="` + bytesOf(117, "1") + `" /></path>`),
		"short":       file(path(bytesOf(116, "1"))),
		"long":        file(path(bytesOf(118, "1"))),
		"not a byte":  file(path(bytesOf(116, "1") + " 256")),
		"not numbers": file(path(bytesOf(116, "1") + " x")),
		"all zero":    file(path(bytesOf(117, "0"))),
		"twice":       file(path(bytesOf(117, "1")) + path(bytesOf(117, "2"))),
	}
	for name, xml := range cases {
		m, err := parseMixerFile([]byte(xml))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got, err := m.coefficients(speakerPath); err == nil {
			t.Errorf("%s: read %v, want it refused", name, got)
		}
	}

	if _, err := parseMixerFile([]byte("<mixercontrol><path")); err == nil {
		t.Error("a file that is not XML was read")
	}
}

// With no mixer file, or none carrying a chain for an output, that output gets the flat chain.
func TestRadarEQFallsBackToUnity(t *testing.T) {
	speaker, jack := radarEQ([]string{filepath.Join(t.TempDir(), "missing.xml")})
	if !bytes.Equal(speaker, unityEQ()) || !bytes.Equal(jack, unityEQ()) {
		t.Error("with no mixer file the chains are not flat")
	}

	dir := t.TempDir()
	only := filepath.Join(dir, "audio_device.xml")
	writeFile(t, only, `<mixercontrol><path name="ext_speaker_output" value="turnon"><kctl name="biquad coefficients" value="`+
		strings.TrimSpace(strings.Repeat("9 ", len(speakerEQ)))+`" /></path></mixercontrol>`)
	speaker, jack = radarEQ([]string{only})
	if !bytes.Equal(speaker, bytes.Repeat([]byte{9}, len(speakerEQ))) {
		t.Errorf("the speaker's chain was not the file's: %v", speaker)
	}
	if !bytes.Equal(jack, unityEQ()) {
		t.Errorf("the jack, with no chain in the file, is not flat: %v", jack)
	}

	speaker, jack = radarEQ([]string{fixture})
	if !bytes.Equal(speaker, pattern(7, 3)) || !bytes.Equal(jack, pattern(11, 5)) {
		t.Error("the chains were not the file's")
	}
}

// The flat chain is the Dot's layout: six unity biquads, then the Dot's closing block.
func TestUnityEQ(t *testing.T) {
	eq := unityEQ()
	if len(eq) != len(speakerEQ) {
		t.Fatalf("%d bytes, want %d", len(eq), len(speakerEQ))
	}
	n := len(unityBiquad)
	for i := range eqBiquads {
		if !bytes.Equal(eq[i*n:(i+1)*n], unityBiquad) {
			t.Errorf("biquad %d is %v, not flat", i, eq[i*n:(i+1)*n])
		}
	}
	if !bytes.Equal(eq[eqBiquads*n:], speakerEQ[eqBiquads*n:]) {
		t.Error("the closing block is not the Dot's")
	}
	// A fresh slice each time: a path holding one must not see another's changes.
	eq[0] = 0
	if unityEQ()[0] != unityBiquad[0] {
		t.Error("unityEQ hands out a shared slice")
	}
}

// The no-pop order: the mute held first thing at start (and so at close, which writes the same
// sequence), the amplifier switched off behind it, and the mute let go last of all, only once the
// stream is playing. Neither path touches the amplifier or the mute itself.
func TestRadarSequenceOrder(t *testing.T) {
	was := MuteSwitch
	t.Cleanup(func() { MuteSwitch = was })
	MuteSwitch = radarMute

	start, paths, off := boardPaths(true, []string{fixture})
	if len(start) == 0 || start[0].name != radarMute || start[0].value != "On" {
		t.Fatalf("the init sequence does not start by holding the mute: %v", start)
	}
	if indexOf(start, AmpSwitch) < 0 || start[indexOf(start, AmpSwitch)].value != "Off" {
		t.Error("the init sequence does not turn the amplifier off")
	}
	for out, seq := range paths {
		for _, k := range seq {
			if k.name == radarMute || k.name == AmpSwitch {
				t.Errorf("%s path writes %s itself", out, k.name)
			}
		}
	}
	for _, k := range off {
		if k.name == radarMute || k.name == AmpSwitch {
			t.Errorf("headphoneOff writes %s", k.name)
		}
	}

	speaker := paths[OutputSpeaker]
	if i := indexOf(speaker, eqControl); i < 0 || !bytes.Equal(speaker[i].blob, pattern(7, 3)) {
		t.Error("the speaker path does not write the file's chain")
	}
	if i := indexOf(paths[OutputHeadphone], eqControl); i < 0 || !bytes.Equal(paths[OutputHeadphone][i].blob, pattern(11, 5)) {
		t.Error("the jack's path does not write the file's chain")
	}
	if i := indexOf(speaker, "Right Channel Only"); i < 0 || speaker[i].value != "Off" {
		t.Error("the speaker path does not take both channels")
	}

	on, unmute := ampOn(true)
	if len(on) != 1 || !is(on[0], AmpSwitch, "On") || len(unmute) != 1 || !is(unmute[0], radarMute, "Off") {
		t.Errorf("turning the amplifier on is %v then %v, want the switch on and then the mute let go", on, unmute)
	}
	if muteSettle <= 0 {
		t.Error("the mute is let go the moment the amplifier is switched on")
	}
	if on, unmute := ampOn(false); indexOf(on, radarMute) >= 0 || len(unmute) != 0 {
		t.Errorf("the mute was let go before the stream was playing: %v %v", on, unmute)
	}
	if unmuteAfter <= periods {
		t.Errorf("the mute is let go after %d periods, before the %d-period ring has been taken from", unmuteAfter, periods)
	}
	offSeq := ampOff()
	if len(offSeq) != 2 || offSeq[0].name != radarMute || offSeq[0].value != "On" || !is(offSeq[1], AmpSwitch, "Off") {
		t.Errorf("turning the amplifier off is %v, want the mute held and then the switch off", offSeq)
	}
}

// The Dot's sequences are as they were: nothing of the radar's mute, the chain its own.
func TestDotSequencesUnchanged(t *testing.T) {
	start, paths, off := boardPaths(false, []string{fixture})
	all := append(append([]kctl{}, start...), off...)
	for _, seq := range paths {
		all = append(all, seq...)
	}
	if indexOf(all, radarMute) >= 0 {
		t.Error("the Dot writes the radar's mute")
	}
	if i := indexOf(start, eqControl); i < 0 || !bytes.Equal(start[i].blob, speakerEQ) {
		t.Error("the Dot's chain is not speakerEQ")
	}
	if i := indexOf(paths[OutputSpeaker], "Right Channel Only"); i < 0 || paths[OutputSpeaker][i].value != "On" {
		t.Error("the Dot's speaker no longer takes the right channel only")
	}
	if MuteSwitch != "" {
		t.Errorf("MuteSwitch is %q off a radar", MuteSwitch)
	}
	if on, unmute := ampOn(true); len(on) != 1 || !is(on[0], AmpSwitch, "On") || len(unmute) != 0 {
		t.Errorf("the Dot's amplifier turns on with %v then %v", on, unmute)
	}
}

// The radar's speaker curve is the Dot's 6 dB down at the low steps and the Dot's again from step 20,
// never louder than the Dot's, and it still rises with the dial.
func TestRadarCurve(t *testing.T) {
	radar, dot := radarCurves[OutputSpeaker], dotCurves[OutputSpeaker]
	for step := 1; step <= VolumeSteps; step++ {
		if radar[step] > dot[step] {
			t.Errorf("step %d: %v dB, louder than the Dot's %v", step, radar[step], dot[step])
		}
		if radar[step] < radar[step-1] {
			t.Errorf("step %d: %v dB, quieter than step %d", step, radar[step], step-1)
		}
		switch {
		case step <= 10 && radar[step] != dot[step]-6:
			t.Errorf("step %d: %v dB, want the Dot's %v less 6", step, radar[step], dot[step])
		case step >= 20 && radar[step] != dot[step]:
			t.Errorf("step %d: %v dB, want the Dot's %v", step, radar[step], dot[step])
		}
	}
	if radar[0] > mute {
		t.Error("step 0 is not muted")
	}
	if radarCurves[OutputHeadphone] != dotCurves[OutputHeadphone] {
		t.Error("the jack's curve is not the Dot's")
	}
}

// is is whether k sets the enum name to value.
func is(k kctl, name, value string) bool { return k.name == name && k.value == value }

func indexOf(seq []kctl, name string) int {
	for i, k := range seq {
		if k.name == name {
			return i
		}
	}
	return -1
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
