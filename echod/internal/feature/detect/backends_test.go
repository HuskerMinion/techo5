package detect

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zserge/microwakeword"

	"github.com/HuskerMinion/techo5/echod/internal/lib/oww"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// fake is an engine that scores whatever is loaded into it, so the slot bookkeeping can be tested
// without model files or inference.
type fake struct {
	loaded map[string]float64
	fed    int
	closed bool
}

func (f *fake) load(m wake.Model) error {
	f.loaded[m.ID] = 0
	return nil
}

func (f *fake) unload(id string) { delete(f.loaded, id) }

func (f *fake) feed([]int16) (map[string]float64, bool) {
	f.fed++
	return f.loaded, true
}

func (f *fake) close() { f.closed = true }

// fakes replaces the engine builder for one test and reports what it handed out.
func fakes(t *testing.T) map[wake.Kind]*fake {
	t.Helper()

	made := map[wake.Kind]*fake{}
	was := build
	build = func(k wake.Kind) (backend, error) {
		f := &fake{loaded: map[string]float64{}}
		made[k] = f
		return f, nil
	}
	t.Cleanup(func() { build = was })
	return made
}

func model(id string, k wake.Kind) wake.Model {
	return wake.Model{ID: id, Phrase: id, Kind: k, Path: id + ".tflite"}
}

// Two wake words can be models of different kinds, and then both engines run and each slot is judged
// on the scores of its own. Nothing chooses: the models do.
func TestSlotsOfDifferentKindsBothRun(t *testing.T) {
	made := fakes(t)
	e := New(2, nil)

	fired := make(chan int, 2)
	e.OnDetect = func(slot int) { fired <- slot }

	if err := e.Use(0, model("glados", wake.KindOpenWakeWord)); err != nil {
		t.Fatalf("Use(0): %v", err)
	}
	if err := e.Use(1, model("hey_jarvis", wake.KindMicroWakeWord)); err != nil {
		t.Fatalf("Use(1): %v", err)
	}
	if len(made) != 2 {
		t.Fatalf("built %d engines, want one per kind", len(made))
	}

	// Only the microWakeWord model is over the threshold, so only its slot fires.
	made[wake.KindOpenWakeWord].loaded["glados"] = 0.1
	made[wake.KindMicroWakeWord].loaded["hey_jarvis"] = 0.99
	e.score([]int16{0}, nil)

	for k, f := range made {
		if f.fed != 1 {
			t.Errorf("%s fed %d times, want 1", k, f.fed)
		}
	}

	select {
	case slot := <-fired:
		if slot != 1 {
			t.Errorf("slot %d fired, want 1", slot+1)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nothing fired")
	}
	select {
	case slot := <-fired:
		t.Errorf("slot %d fired too, want only the one over its threshold", slot+1)
	case <-time.After(100 * time.Millisecond):
	}
}

// An engine costs a front end for every frame, so the last slot to leave takes it with it.
func TestAnEngineGoesWithItsLastSlot(t *testing.T) {
	made := fakes(t)
	e := New(2, nil)

	if err := e.Use(0, model("glados", wake.KindOpenWakeWord)); err != nil {
		t.Fatalf("Use(0): %v", err)
	}
	if err := e.Use(1, model("hey_jarvis", wake.KindMicroWakeWord)); err != nil {
		t.Fatalf("Use(1): %v", err)
	}

	e.Clear(1)
	if !made[wake.KindMicroWakeWord].closed {
		t.Error("microWakeWord stayed up with no slot using it")
	}
	if made[wake.KindOpenWakeWord].closed {
		t.Error("openWakeWord went with the other engine's slot")
	}
	if len(e.backends) != 1 {
		t.Errorf("%d engines still up, want 1", len(e.backends))
	}

	e.Clear(0)
	if !made[wake.KindOpenWakeWord].closed {
		t.Error("openWakeWord stayed up with nothing loaded")
	}
	if len(e.backends) != 0 {
		t.Errorf("%d engines still up, want none", len(e.backends))
	}
}

// The same wake word in both slots is one model in one engine, and one slot letting go of it must
// not take it away from the other.
func TestOneModelInTwoSlots(t *testing.T) {
	made := fakes(t)
	e := New(2, nil)

	for slot := range 2 {
		if err := e.Use(slot, model("glados", wake.KindOpenWakeWord)); err != nil {
			t.Fatalf("Use(%d): %v", slot, err)
		}
	}

	e.Clear(0)
	f := made[wake.KindOpenWakeWord]
	if f.closed {
		t.Error("the engine closed while a slot was still using it")
	}
	if _, ok := f.loaded["glados"]; !ok {
		t.Error("the model was unloaded while a slot was still using it")
	}

	e.Clear(1)
	if _, ok := f.loaded["glados"]; ok {
		t.Error("the model stayed loaded with no slot using it")
	}
	if !f.closed {
		t.Error("the engine stayed up with nothing loaded")
	}
}

// Reloading a slot with another model of the same kind keeps that engine: it is still wanted, and
// rebuilding it would throw away streaming state for nothing.
func TestSwappingWithinAKindKeepsTheEngine(t *testing.T) {
	made := fakes(t)
	e := New(1, nil)

	if err := e.Use(0, model("glados", wake.KindOpenWakeWord)); err != nil {
		t.Fatalf("Use: %v", err)
	}
	if err := e.Use(0, model("wall-e", wake.KindOpenWakeWord)); err != nil {
		t.Fatalf("Use again: %v", err)
	}

	f := made[wake.KindOpenWakeWord]
	if f.closed {
		t.Fatal("the engine was closed under the model that replaced it")
	}
	if _, ok := f.loaded["glados"]; ok {
		t.Error("the model that was replaced is still loaded")
	}
	if _, ok := f.loaded["wall-e"]; !ok {
		t.Error("the model that replaced it is not loaded")
	}
}

// A damaged model file is an error from load, never a panic: the parser trusts the file, and the
// v0.2.5 image shipped models whose CR bytes before LF had been stripped.
func TestMicroLoadSurvivesDamagedModels(t *testing.T) {
	good, err := os.ReadFile("assets/stop.tflite")
	if err != nil {
		t.Skip("no model to damage:", err)
	}
	dir := t.TempDir()
	damaged := map[string][]byte{
		"crlf-stripped": bytes.ReplaceAll(good, []byte{'\r', '\n'}, []byte{'\n'}),
		"truncated":     good[:len(good)/3],
		"flipped":       func() []byte { b := bytes.Clone(good); b[4], b[5], b[6], b[7] = 0xff, 0xff, 0xff, 0x7f; return b }(),
		"empty":         nil,
	}
	for name, data := range damaged {
		path := filepath.Join(dir, name+".tflite")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		b := &microBackend{dets: map[string]*microwakeword.Detector{}, scores: map[string]float64{}}
		m := wake.Model{ID: name, Path: path, Config: microwakeword.Config{ModelPath: path, SlidingWindowSize: 5, FeaturesStepMs: 10}}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: load panicked: %v", name, r)
				}
			}()
			if err := b.load(m); err == nil && !bytes.Equal(data, good) {
				t.Logf("%s: parsed without error (the damage missed anything the parser reads)", name)
			}
		}()
	}
}

// The openWakeWord backend needs the same guard the micro one has, for the same reason: the model is a
// file from outside, and the interpreter builds a graph out of the indices and shapes it supplies. A
// file that cannot be made into a classifier is an error from load, never a panic out of it.
func TestOWWLoadSurvivesDamagedModels(t *testing.T) {
	good, err := os.ReadFile("assets/stop.tflite")
	if err != nil {
		t.Skip("no model to damage:", err)
	}
	front, err := oww.New()
	if err != nil {
		t.Skip("no front end:", err)
	}

	dir := t.TempDir()
	damaged := map[string][]byte{
		"whole":     good, // not an openWakeWord classifier at all, which is its own refusal
		"truncated": good[:len(good)/3],
		"flipped":   func() []byte { b := bytes.Clone(good); b[4], b[5], b[6], b[7] = 0xff, 0xff, 0xff, 0x7f; return b }(),
		"zeroed":    make([]byte, len(good)),
		"empty":     nil,
	}
	for name, data := range damaged {
		path := filepath.Join(dir, name+".tflite")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		b := &owwBackend{front: front, scores: map[string]float64{}}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: load panicked: %v", name, r)
				}
			}()
			if err := b.load(wake.Model{ID: name, Path: path}); err == nil {
				t.Errorf("%s: loaded as a classifier", name)
			}
		}()
	}
}
