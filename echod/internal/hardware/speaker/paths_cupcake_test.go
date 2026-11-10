//go:build dot && donut

package speaker

import "testing"

// The Echo Input's paths: its line out is its one output, under both names, and the amplifier's
// controls it does not have are written only where they are there.
func TestTheEchoInputPlaysItsLineOutWhateverTheOutput(t *testing.T) {
	paths := boardPaths(true)
	for _, out := range []Output{OutputSpeaker, OutputHeadphone} {
		seq, ok := paths[out]
		if !ok || len(seq) == 0 {
			t.Fatalf("%s: no path", out)
		}
		mux := map[string]string{}
		for _, c := range seq {
			if c.value != "" {
				mux[c.name] = c.value
			}
			if (c.name == "PCM Playback Volume" || c.name == ampLevel) && !c.ifPresent {
				t.Errorf("%s: writes the amplifier's %q, which the Echo Input does not have, without ifPresent", out, c.name)
			}
		}
		if mux["HPOUT Mux"] != "AUDIO_AMP" || mux["LINEOUT Mux"] != "VOICE_AMP" {
			t.Errorf("%s: muxes %v, want both of the codec's outputs open", out, mux)
		}
	}
	for _, c := range jackFormat.open {
		if c.name == "PCM Playback Volume" && !c.ifPresent {
			t.Error("the jack's format writes the amplifier's volume without ifPresent: it would fail on the Echo Input")
		}
	}
}

// The Echo Dot 3 keeps its own two paths and curves.
func TestTheEchoDot3KeepsItsPaths(t *testing.T) {
	paths := boardPaths(false)
	if len(paths[OutputSpeaker]) == 0 || len(paths[OutputHeadphone]) == 0 {
		t.Fatal("the Dot 3 lost a path")
	}
	if equalSeq(paths[OutputSpeaker], paths[OutputHeadphone]) {
		t.Error("the Dot 3's speaker and jack paths are the same")
	}
	curves := boardCurves(false)
	if curves[OutputSpeaker] == curves[OutputHeadphone] {
		t.Error("the Dot 3's speaker took the jack's curve")
	}
}

// The line out is the jack's codec output: the Echo Input's curve for it is the jack's, under both names.
func TestTheEchoInputTakesTheJacksCurve(t *testing.T) {
	curves := boardCurves(true)
	for _, out := range []Output{OutputSpeaker, OutputHeadphone} {
		if curves[out] != dotCurves[OutputHeadphone] {
			t.Errorf("%s: not the jack's curve", out)
		}
	}
}

func equalSeq(a, b []kctl) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].name != b[i].name || a[i].value != b[i].value || a[i].level != b[i].level {
			return false
		}
	}
	return true
}
