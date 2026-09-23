//go:build dot

package mic

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A recorded Dot capture replayed through the real mixers and canceller: speech over music, with the
// device's own loopback as the far end. Set TECHO5_MICBENCH to a capture directory from techo5-dot's
// tools/micbench/micsession.sh (music_front1m_seg0.s24: 1 s of lead-in, music alone to about 11 s, then
// a talker at 1 m over the music). Skipped without one; the recordings are too large to check in.
func TestReplayMusicCancelsBetterOnTheAverage(t *testing.T) {
	dir := os.Getenv("TECHO5_MICBENCH")
	if dir == "" {
		t.Skip("TECHO5_MICBENCH not set")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "music_front1m_seg0.s24"))
	if err != nil {
		t.Fatal(err)
	}

	center := replay(t, raw, Center{})
	average := replay(t, raw, Average{})

	for _, r := range []struct {
		name string
		got  replayed
	}{{"center mic", center}, {"average of seven", average}} {
		t.Logf("%-17s residual after convergence %.1f dBFS, speech over it %.1f dB",
			r.name, r.got.residual, r.got.speechOver)
	}

	if average.speechOver < center.speechOver+3 {
		t.Errorf("average hears speech over music %.1f dB above its residual, center %.1f: want at least 3 dB better",
			average.speechOver, center.speechOver)
	}
}

// setReplayEngine honours TECHO5_AEC: "webrtc" runs the helper process (techo5-aec on PATH), anything
// else the built-in filter. A helper that will not start is a failure here, not a silent fallback: the
// point of the run is to compare engines.
func setReplayEngine(c *canceller) {
	want := os.Getenv("TECHO5_AEC")
	if want == "" || want == "builtin" {
		return
	}
	args := strings.Fields(os.Getenv("TECHO5_AEC_ARGS"))
	if len(args) == 0 {
		args = []string{"--ns", "low"}
	}
	e, err := startExternal(args...)
	if err != nil {
		panic("echo canceller helper " + want + ": " + err.Error())
	}
	c.ext, c.engine = e, "webrtc"
}

type replayed struct {
	residual   float64 // mean power of the canceled output with music alone, dBFS
	speechOver float64 // talk-section speech frames against that residual, dB
}

// replay runs a capture period by period through the path broadcast takes: decode, mix, cancel.
func replay(t *testing.T, raw []byte, mixer Mixer) replayed {
	t.Helper()
	c := newCanceller()
	setReplayEngine(c)
	if c == nil {
		t.Fatal("no canceller")
	}

	frameBytes := Channels * Bits / 8
	period := FrameSamples * frameBytes
	var out []int16
	for off := 0; off+period <= len(raw); off += period {
		block := raw[off : off+period]
		mics := Decode(block)
		frame := mixer.Mix(mics)
		if canceled := c.apply(block, cancelInput(mixer, mics, frame)); canceled != nil {
			frame = canceled
		}
		out = append(out, frame...)
	}

	// Music alone from 4 s (the filter has had three seconds) to 10 s; the talker from 12 s.
	quiet := out[4*Rate : 10*Rate]
	residual := power(quiet)

	// The louder half of the talk section's frames stands for speech, the same frames for both mixes
	// wherever they fall: a threshold over the residual would count fewer frames for whichever mix
	// cancels worse, and flatter it.
	talk := out[12*Rate:]
	var ps []float64
	for i := 0; i+FrameSamples <= len(talk); i += FrameSamples {
		ps = append(ps, power(talk[i:i+FrameSamples]))
	}
	sorted := append([]float64(nil), ps...)
	slices.Sort(sorted)
	median := sorted[len(sorted)/2]
	var speech, frames float64
	for _, p := range ps {
		if p >= median {
			speech += p
			frames++
		}
	}
	speech /= frames
	return replayed{
		residual:   10 * math.Log10(residual/(32768*32768)),
		speechOver: 10 * math.Log10(math.Max(speech-residual, 1e-9)/residual),
	}
}

func power(s []int16) float64 {
	var sum float64
	for _, v := range s {
		sum += float64(v) * float64(v)
	}
	return sum / float64(len(s))
}
