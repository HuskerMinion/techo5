//go:build dot

package mic

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/zserge/microwakeword"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// Wake word scores on recorded Dot captures, through the path the daemon runs: mix, cancel while the
// loopback plays, level, detect. Both front ends hear the same audio, which is the only fair comparison:
// detection varies too much between utterances for one-at-a-time tests to mean anything.
//
// TECHO5_MICBENCH is a capture directory (tools/micbench in techo5-dot) and TECHO5_MODELS a directory of
// microWakeWord models; TECHO5_WAKE picks one (default okay_nabu). When the directory's session.log has
// prompts for a take, each prompt is scored on its own: the window from the prompt to promptWindow after
// it holds one utterance, and a crossing anywhere else is a false accept. Reports only.
func TestReplayWakeScores(t *testing.T) {
	dir, models := os.Getenv("TECHO5_MICBENCH"), os.Getenv("TECHO5_MODELS")
	if dir == "" || models == "" {
		t.Skip("TECHO5_MICBENCH or TECHO5_MODELS not set")
	}
	id := os.Getenv("TECHO5_WAKE")
	if id == "" {
		id = "okay_nabu"
	}
	installed, err := wake.Installed(models)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := wake.Find(installed, id)
	if !ok {
		t.Fatalf("no model %q in %s", id, models)
	}
	// On the device the threshold is Home Assistant's; here it is the model's own published cutoff.
	var manifest struct {
		Micro struct {
			Cutoff float64 `json:"probability_cutoff"`
		} `json:"micro"`
	}
	if data, err := os.ReadFile(filepath.Join(models, id+".json")); err == nil && json.Unmarshal(data, &manifest) == nil {
		m.Config.ProbabilityCutoff = manifest.Micro.Cutoff
	}
	if m.Config.ProbabilityCutoff <= 0 {
		t.Fatalf("no probability_cutoff in %s.json", id)
	}
	// config.DefaultThreshold is what the daemon ships with; 0.83 is what the Echo Show 5 runs with in
	// Home Assistant. The model's published cutoff is the strictest of the three.
	cutoffs := []float64{m.Config.ProbabilityCutoff, config.DefaultThreshold, 0.83}
	t.Logf("%s, published cutoff %.2f; also scored at the daemon default %.2f and the Show's %.2f",
		m.Phrase, cutoffs[0], cutoffs[1], cutoffs[2])

	prompts := sessionPrompts(t, filepath.Join(dir, "session.log"))

	takes, _ := filepath.Glob(filepath.Join(dir, "*.s24"))
	sort.Strings(takes)
	for _, take := range takes {
		name := strings.TrimSuffix(filepath.Base(take), ".s24")
		raw, err := os.ReadFile(take)
		if err != nil {
			t.Fatal(err)
		}
		at := prompts[name]
		t.Logf("%s (%d prompts)", name, len(at))
		for _, fe := range []struct {
			name  string
			mixer Mixer
		}{{"center (old)", Center{}}, {"average of 7", Average{}}} {
			trace := scoreWake(t, raw, fe.mixer, m)
			line := fmt.Sprintf("  %-13s", fe.name)
			for _, cut := range cutoffs {
				hit, falses := detections(trace, at, cut)
				if len(at) > 0 {
					line += fmt.Sprintf("  @%.2f %d/%d hit, %d false", cut, hit, len(at), falses)
				} else {
					line += fmt.Sprintf("  @%.2f %d crossings", cut, falses)
				}
			}
			if len(at) > 0 {
				line += "  per prompt best:"
				for _, p := range at {
					line += fmt.Sprintf(" %.2f", windowBest(trace, p))
				}
			}
			t.Log(line)
			if dir := os.Getenv("TECHO5_TRACE"); dir != "" {
				out, err := os.Create(filepath.Join(dir, name+"-"+strings.Fields(fe.name)[0]+".csv"))
				if err != nil {
					t.Fatal(err)
				}
				for i, v := range trace {
					fmt.Fprintf(out, "%.3f,%.4f\n", float64(i)*float64(FrameSamples)/Rate, v)
				}
				out.Close()
			}
		}
	}
}

// promptWindow is how long after a prompt its utterance can end: reaction, "Okay Nabu", and the model's
// sliding window.
const promptWindow = 4.0

// sessionPrompts reads micsession.sh's log: for every take, its prompts in seconds from the take's start.
func sessionPrompts(t *testing.T, path string) map[string][]float64 {
	t.Helper()
	out := map[string][]float64{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()

	start := map[string]float64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		when, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			continue
		}
		switch fields[1] {
		case "start":
			start[fields[2]] = when
		case "prompt":
			if s, ok := start[fields[2]]; ok {
				out[fields[2]] = append(out[fields[2]], when-s)
			}
		}
	}
	return out
}

// detections counts prompts whose window the score crossed cut in, and crossings outside every window.
func detections(trace []float64, prompts []float64, cut float64) (hit, falses int) {
	frame := float64(FrameSamples) / Rate
	inWindow := func(i int) int {
		t := float64(i) * frame
		for k, p := range prompts {
			if t >= p && t <= p+promptWindow {
				return k
			}
		}
		return -1
	}
	got := make([]bool, len(prompts))
	above := false
	for i, s := range trace {
		if s >= cut && !above {
			if k := inWindow(i); k >= 0 {
				got[k] = true
			} else {
				falses++
			}
		}
		above = s >= cut
	}
	for _, g := range got {
		if g {
			hit++
		}
	}
	return hit, falses
}

func windowBest(trace []float64, prompt float64) float64 {
	frame := float64(FrameSamples) / Rate
	best := 0.0
	for i := int(prompt / frame); i < len(trace) && float64(i)*frame <= prompt+promptWindow; i++ {
		best = max(best, trace[i])
	}
	return best
}

// replayGain is TECHO5_GAIN: a fixed gain ahead of the leveler, standing in for more analog gain than
// the capture was taken with.
var replayGain = func() float64 {
	var g float64
	if _, err := fmt.Sscan(os.Getenv("TECHO5_GAIN"), &g); err != nil || g <= 0 {
		return 1
	}
	return g
}()

var replayPostGain = func() float64 {
	var g float64
	if _, err := fmt.Sscan(os.Getenv("TECHO5_POSTGAIN"), &g); err != nil || g <= 0 {
		return 1
	}
	return g
}()

// scoreWake is the sliding-window score after every 20 ms frame.
func scoreWake(t *testing.T, raw []byte, mixer Mixer, m wake.Model) []float64 {
	t.Helper()
	det, err := microwakeword.NewDetector(m.Config)
	if err != nil {
		t.Fatal(err)
	}
	c, l := newCanceller(), newLeveler()
	setReplayEngine(c)

	frameBytes := Channels * Bits / 8
	period := FrameSamples * frameBytes
	var trace []float64
	for off := 0; off+period <= len(raw); off += period {
		block := raw[off : off+period]
		mics := Decode(block)
		frame := mixer.Mix(mics)
		if canceled := c.apply(block, cancelInput(mixer, mics, frame)); canceled != nil {
			frame = canceled
		}
		frame = append([]int16(nil), frame...)
		if replayGain != 1 {
			for i, v := range frame {
				frame[i] = int16(max(-32768, min(32767, float64(v)*replayGain)))
			}
		}
		// TECHO5_LEVEL=off skips the leveler; TECHO5_POSTGAIN is gain in front of the detector, after
		// whatever leveling ran. Both exist because the leveler adapts to the room after the canceller,
		// and over music the residual is what it adapts to.
		if os.Getenv("TECHO5_LEVEL") != "off" {
			l.apply(frame)
		}
		if replayPostGain != 1 {
			for i, v := range frame {
				frame[i] = int16(max(-32768, min(32767, float64(v)*replayPostGain)))
			}
		}
		// The daemon reads the sliding average against the cutoff, not ProcessAudio's result.
		det.ProcessAudio(frame)
		trace = append(trace, det.SlidingAverage())
	}
	return trace
}
