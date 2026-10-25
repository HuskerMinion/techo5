package detect

import (
	"math"
	"testing"

	"github.com/zserge/microwakeword"

	"github.com/zserge/microwakeword/audiofrontend"

	"github.com/HuskerMinion/techo5/echod/internal/feature/detect/assets"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mwwfront"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// speechish is a few seconds of 16 kHz audio with words' worth of change in it: a gliding tone under
// noise, in bursts. What it sounds like does not matter; that the models' scores move does.
func speechish(seconds int) []int16 {
	out := make([]int16, 16000*seconds)
	seed := uint32(1)
	for i := range out {
		seed = seed*1664525 + 1013904223
		noise := float64(int32(seed)>>20) / 2048
		t := float64(i) / 16000
		burst := 0.0
		if math.Mod(t, 0.8) < 0.45 {
			burst = 1
		}
		tone := math.Sin(2 * math.Pi * (300 + 400*math.Sin(3*t)) * t)
		out[i] = int16(6000*burst*tone + 800*noise)
	}
	return out
}

// Sharing the front end must not change a single score: each model fed the shared features scores
// exactly what its own detector, reading the audio itself, scores on the same frames.
func TestSharedFrontEndScoresExactlyAsAlone(t *testing.T) {
	path, err := assets.Stop(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	models := []wake.Model{
		{ID: "a", Kind: wake.KindMicroWakeWord, Path: path, Config: microwakeword.Config{ModelPath: path, SlidingWindowSize: 5, FeaturesStepMs: 10}},
		{ID: "b", Kind: wake.KindMicroWakeWord, Path: path, Config: microwakeword.Config{ModelPath: path, SlidingWindowSize: 3, FeaturesStepMs: 10}},
		{ID: "c", Kind: wake.KindMicroWakeWord, Path: path, Config: microwakeword.Config{ModelPath: path, SlidingWindowSize: 5, FeaturesStepMs: 20}},
	}

	shared := newMicroBackend(false)
	alone := map[string]*microwakeword.Detector{}
	for _, m := range models {
		if err := shared.load(m); err != nil {
			t.Fatal(err)
		}
		cfg := m.Config
		cfg.ProbabilityCutoff = neverFires
		d, err := microwakeword.NewDetector(cfg)
		if err != nil {
			t.Fatal(err)
		}
		alone[m.ID] = d
	}
	if len(shared.fronts) != 2 {
		t.Fatalf("%d front ends for two feature steps", len(shared.fronts))
	}

	audio := speechish(6)
	moved := map[string]bool{}
	for at := 0; at+160 <= len(audio); at += 160 {
		frame := audio[at : at+160]
		scores, _ := shared.feed(frame)
		for id, d := range alone {
			d.ProcessAudio(frame)
			want := d.SlidingAverage()
			if scores[id] != want {
				t.Fatalf("model %s at sample %d: shared %v, alone %v", id, at, scores[id], want)
			}
			if want > 0 {
				moved[id] = true
			}
		}
	}
	for _, m := range models {
		if !moved[m.ID] {
			t.Logf("model %s never scored above zero, so this compared zeros for it", m.ID)
		}
	}
}

// Unloading the last model on a step drops that front end; the others keep theirs.
func TestUnloadDropsAnUnusedFrontEnd(t *testing.T) {
	path, err := assets.Stop(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := newMicroBackend(false)
	for id, step := range map[string]int{"a": 10, "b": 10, "c": 20} {
		m := wake.Model{ID: id, Path: path, Config: microwakeword.Config{ModelPath: path, SlidingWindowSize: 5, FeaturesStepMs: step}}
		if err := b.load(m); err != nil {
			t.Fatal(err)
		}
	}
	b.unload("c")
	if _, ok := b.fronts[20]; ok || len(b.fronts) != 1 {
		t.Errorf("fronts after unloading the only 20 ms model: %v", b.fronts)
	}
	b.unload("a")
	if _, ok := b.fronts[10]; !ok {
		t.Error("the 10 ms front end went while b still uses it")
	}
}

// A wake word and the stop word, the usual pair, over one 10 ms frame: shared, and each alone.
func BenchmarkMicroFeed(b *testing.B) {
	path, err := assets.Stop(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	cfg := microwakeword.Config{ModelPath: path, SlidingWindowSize: 5, FeaturesStepMs: 10, ProbabilityCutoff: neverFires}
	audio := speechish(4)
	frames := len(audio) / 160

	b.Run("shared", func(b *testing.B) {
		s := newMicroBackend(false)
		for _, id := range []string{"wake", "stop"} {
			if err := s.load(wake.Model{ID: id, Path: path, Config: cfg}); err != nil {
				b.Fatal(err)
			}
		}
		for i := range b.N {
			at := (i % frames) * 160
			s.feed(audio[at : at+160])
		}
	})
	b.Run("shared-exact", func(b *testing.B) {
		s := newMicroBackend(true)
		for _, id := range []string{"wake", "stop"} {
			if err := s.load(wake.Model{ID: id, Path: path, Config: cfg}); err != nil {
				b.Fatal(err)
			}
		}
		for i := range b.N {
			at := (i % frames) * 160
			s.feed(audio[at : at+160])
		}
	})
	b.Run("alone", func(b *testing.B) {
		var ds []*microwakeword.Detector
		for range 2 {
			d, err := microwakeword.NewDetector(cfg)
			if err != nil {
				b.Fatal(err)
			}
			ds = append(ds, d)
		}
		for i := range b.N {
			at := (i % frames) * 160
			for _, d := range ds {
				d.ProcessAudio(audio[at : at+160])
			}
		}
	})
}

// The exact front end is what an exact backend feeds its models: each scores what a detector handed
// mwwfront's features of the same audio directly scores, frame for frame. And it is not the old one.
func TestExactBackendFeedsTheExactFeatures(t *testing.T) {
	path, err := assets.Stop(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := microwakeword.Config{ModelPath: path, SlidingWindowSize: 5, FeaturesStepMs: 10}
	b := newMicroBackend(true)
	if err := b.load(wake.Model{ID: "stop", Path: path, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.fronts[10].fe.(*mwwfront.Frontend); !ok {
		t.Fatalf("an exact backend runs %T", b.fronts[10].fe)
	}
	cfg.ProbabilityCutoff = neverFires
	ref, err := microwakeword.NewDetector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fe := mwwfront.New(10)
	audio := speechish(6)
	var pending []int16
	for at := 0; at+160 <= len(audio); at += 160 {
		frame := audio[at : at+160]
		scores, _ := b.feed(frame)
		pending = append(pending, frame...)
		for len(pending) >= fe.WindowSamples() {
			ref.ProcessFeatures([][]int8{features(fe.ProcessFrame(pending[:fe.WindowSamples()]))})
			pending = pending[fe.StepSamples():]
		}
		if scores["stop"] != ref.SlidingAverage() {
			t.Fatalf("at sample %d: backend %v, mwwfront fed directly %v", at, scores["stop"], ref.SlidingAverage())
		}
	}

	old := newMicroBackend(false)
	old.front(10)
	if _, ok := old.fronts[10].fe.(*audiofrontend.Frontend); !ok {
		t.Fatalf("the default backend runs %T", old.fronts[10].fe)
	}
}

// BenchmarkFrontEnds is one 30 ms window's features on each front end: the cost per 10 ms step,
// without the models.
func BenchmarkFrontEnds(b *testing.B) {
	audio := speechish(4)
	for _, fe := range []struct {
		name string
		fe   featureFront
	}{
		{"zserge", audiofrontend.New(audiofrontend.DefaultConfig(10))},
		{"exact", mwwfront.New(10)},
	} {
		b.Run(fe.name, func(b *testing.B) {
			n := (len(audio) - 480) / 160
			for i := range b.N {
				at := (i % n) * 160
				fe.fe.ProcessFrame(audio[at : at+480])
			}
		})
	}
}
