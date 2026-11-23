//go:build dot && cannoli

package mic

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// The Echo Dot 5th gen's wake bench recordings (techo5-dot tools/micbench/wakebench.sh -r) replayed
// through every mix this board offers, so they are compared on the same utterances over the same music.
// Each raw-volNNN.s24 is the capture as the daemon had it (16 kHz, the three microphones and the
// reference), raw-volNNN.prompts the prompt times in device seconds, and log-volNNN.txt the log that
// says when the dump began.
//
// TECHO5_WAKEBENCH is one or more bench directories, comma separated; TECHO5_MODELS the models, and
// TECHO5_WAKE the word (alexa). Scored on the exact front end, as the daemon scores on this board, at
// the daemon's cutoffs: 0.85 without music, 0.55 over it. Reports only.
func TestReplayWakebench(t *testing.T) {
	dirs, models := os.Getenv("TECHO5_WAKEBENCH"), os.Getenv("TECHO5_MODELS")
	if dirs == "" || models == "" {
		t.Skip("TECHO5_WAKEBENCH or TECHO5_MODELS not set")
	}
	id := os.Getenv("TECHO5_WAKE")
	if id == "" {
		id = "alexa"
	}
	installed, err := wake.Installed(models)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := wake.Find(installed, id)
	if !ok {
		t.Fatalf("no model %q in %s", id, models)
	}
	os.Setenv("TECHO5_WAKE_FRONTEND", "exact")

	mixes := []struct {
		name string
		make func() Mixer
	}{
		{"center", func() Mixer { return Center{} }},
		{"all", func() Mixer { return Average{} }},
		{"superdirective", func() Mixer { return newSteered() }},
	}
	// TECHO5_BEAMS=all adds every fixed beam, canceled as a fixed path: what a beam held through the
	// playback would hear.
	if os.Getenv("TECHO5_BEAMS") == "all" {
		for b := range steerBeams {
			mixes = append(mixes, struct {
				name string
				make func() Mixer
			}{fmt.Sprintf("beam%02d", b), func() Mixer { return newFixedBeam(b) }})
		}
	}
	// TECHO5_NULLBEAMS is a list of beams to try with the speaker nulled.
	for _, f := range strings.Split(os.Getenv("TECHO5_NULLBEAMS"), ",") {
		if b, err := strconv.Atoi(f); err == nil {
			mixes = append(mixes, struct {
				name string
				make func() Mixer
			}{fmt.Sprintf("null%02d", b), func() Mixer {
				s := newSteered()
				s.beam, s.bf.Beam, s.hold = b, b, true
				return s
			}})
		}
	}
	totals := map[string][2]int{}
	for _, dir := range strings.Split(dirs, ",") {
		takes, _ := filepath.Glob(filepath.Join(dir, "raw-vol*.s24"))
		sort.Strings(takes)
		for _, take := range takes {
			tag := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(take), "raw-"), ".s24")
			prompts := benchPrompts(t, dir, tag)
			if len(prompts) == 0 {
				continue
			}
			raw, err := os.ReadFile(take)
			if err != nil {
				t.Fatal(err)
			}
			cut := 0.55
			if tag == "vol000" {
				cut = 0.85
			}
			line := fmt.Sprintf("%-40s", filepath.Base(dir)+"/"+tag)
			for _, mx := range mixes {
				trace := scoreWake(t, raw, mx.make(), m)
				hit, falses := detections(trace, prompts, cut)
				var best float64
				for _, p := range prompts {
					best += windowBest(trace, p)
				}
				line += fmt.Sprintf("  %s %d/%d (%d false, mean best %.2f)", mx.name, hit, len(prompts), falses,
					best/float64(len(prompts)))
				tot := totals[mx.name]
				totals[mx.name] = [2]int{tot[0] + hit, tot[1] + len(prompts)}
			}
			t.Log(line)
		}
	}
	for _, mx := range mixes {
		t.Logf("total %-15s %d/%d", mx.name, totals[mx.name][0], totals[mx.name][1])
	}
}

// benchPrompts is a take's prompts in seconds from the start of its dump.
func benchPrompts(t *testing.T, dir, tag string) []float64 {
	t.Helper()
	start := -1.0
	re := regexp.MustCompile(`\[(\d+\.\d+)\] raw dump started`)
	if f, err := os.Open(filepath.Join(dir, "log-"+tag+".txt")); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			if m := re.FindStringSubmatch(sc.Text()); m != nil {
				start, _ = strconv.ParseFloat(m[1], 64)
				break
			}
		}
		f.Close()
	}
	if start < 0 {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(dir, "raw-"+tag+".prompts"))
	if err != nil {
		return nil
	}
	var out []float64
	for _, s := range strings.Fields(string(b)) {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			out = append(out, v-start)
		}
	}
	return out
}

// fixedBeam is one superdirective beam that never steers, which the canceller can learn like any other
// fixed path.
type fixedBeam struct{ s *steered }

func newFixedBeam(b int) fixedBeam {
	s := newSteered()
	s.bf.Beam = b
	s.bf.Spectrum = nil
	return fixedBeam{s}
}

func (f fixedBeam) Mix(mics [][]int16) []int16 { return f.s.Mix(mics) }
func (fixedBeam) fixedPath()                   {}
