package detect

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/zserge/microwakeword"
	"github.com/zserge/microwakeword/audiofrontend"

	"github.com/HuskerMinion/techo5/echod/internal/lib/mwwfront"
	"github.com/HuskerMinion/techo5/echod/internal/lib/oww"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// backend runs the wake words of one Kind. It is the whole engine rather than one detector because
// openWakeWord's front end is shared: the mel and embedding chain has to consume each frame exactly
// once no matter how many wake words are listening to it, so scoring is per backend and the result
// is a score per wake word.
//
// Implementations are streaming. Every frame has to be fed, in order, or the next utterance is
// scored from a cold state.
type backend interface {
	// load makes a wake word scoreable, replacing one already loaded under the same id.
	load(m wake.Model) error

	// unload drops one.
	unload(id string)

	// feed consumes one frame and reports a score per loaded wake word, and whether those scores
	// are new. A backend that scores less often than once per frame reports false in between, and
	// the scores it returns then are meaningless.
	feed(frame []int16) (map[string]float64, bool)

	// failed is the wake words that fell over in feed and were unloaded because of it since the last
	// call, so the engine can take them out of their slots.
	failed() []string

	close()
}

// newBackend builds the engine for one of them.
func newBackend(k wake.Kind) (backend, error) {
	if k == wake.KindOpenWakeWord {
		front, err := oww.New()
		if err != nil {
			return nil, err
		}
		return &owwBackend{front: front, scores: map[string]float64{}}, nil
	}
	return newMicroBackend(ExactFrontEnd), nil
}

// ExactFrontEnd picks microWakeWord's feature front end. False, the default, is
// github.com/zserge/microwakeword's: a float approximation of TensorFlow Lite Micro's front end, which
// is what has always run. True is mwwfront, the reference's own integer arithmetic, bit for bit the
// features the models were trained on. They score differently enough that the sensitivity settings
// mean different things on each, so it is a switch for now: TECHO5_WAKE_FRONTEND=exact in the daemon's
// environment, read when it starts. A backend keeps the one it was made with.
var ExactFrontEnd = os.Getenv("TECHO5_WAKE_FRONTEND") == "exact"

// microBackend is microWakeWord. Each wake word is its own streaming model, but they all read the same
// features of the same audio, so the feature front end runs once per step size and every model is fed
// what it made: another wake word, or the stop word, costs its inference and not a second front end.
type microBackend struct {
	dets   map[string]microDetector
	step   map[string]int // each model's feature step, in ms
	fronts map[int]*microFront
	scores map[string]float64
	gone   []string // dropped by feed, for failed
	exact  bool     // mwwfront rather than zserge's audiofrontend; see ExactFrontEnd
}

// microDetector is what the backend asks of a loaded model: microwakeword.Detector, or a stand-in in
// a test.
type microDetector interface {
	ProcessFeatures(frames [][]int8) bool
	SlidingAverage() float64
}

// featureFront is a microWakeWord feature front end: one window of audio in, a frame of 40 features
// out. Both kinds keep state between windows (noise estimates), so each is fed every window in order.
type featureFront interface {
	ProcessFrame(window []int16) []uint16
}

// microFront is one feature front end and the audio it has not read yet.
type microFront struct {
	fe           featureFront
	buf          []int16
	window, step int // in samples
}

func newMicroBackend(exact bool) *microBackend {
	return &microBackend{
		dets:   map[string]microDetector{},
		step:   map[string]int{},
		fronts: map[int]*microFront{},
		scores: map[string]float64{},
		exact:  exact,
	}
}

// front is the front end for a feature step, made the first time a model wants it. A step of zero is
// the library's default, 20 ms.
func (b *microBackend) front(stepMs int) *microFront {
	if f, ok := b.fronts[stepMs]; ok {
		return f
	}
	var f *microFront
	if b.exact {
		fe := mwwfront.New(stepMs)
		f = &microFront{fe: fe, window: fe.WindowSamples(), step: fe.StepSamples()}
	} else {
		cfg := audiofrontend.DefaultConfig(stepMs)
		f = &microFront{
			fe:     audiofrontend.New(cfg),
			window: cfg.SampleRate * cfg.WindowSizeMs / 1000,
			step:   cfg.SampleRate * cfg.StepSizeMs / 1000,
		}
	}
	b.fronts[stepMs] = f
	return f
}

// features quantizes a front end's output exactly as the detector does its own (ProcessAudio in
// microwakeword.go), so a model fed here scores what it would have scored alone. mwwfront.Quantize is
// the same arithmetic, ESPHome's.
func features(raw []uint16) []int8 {
	frame := make([]int8, mwwfront.Channels)
	mwwfront.Quantize(raw, frame)
	return frame
}

// neverFires is the cutoff handed to the detector so its own threshold never triggers. The
// threshold is ours: it comes from Home Assistant and changes while running, and rebuilding the
// detector to apply it would wipe the streaming state a model needs to score well.
const neverFires = 1.1

func (b *microBackend) load(m wake.Model) (err error) {
	cfg := m.Config
	cfg.ProbabilityCutoff = neverFires

	// The parser trusts the file: a damaged model panics inside it (a slice out of range on a
	// truncated or altered flatbuffer), which took the whole daemon down in a loop on a unit whose
	// image carried corrupted models. A model that cannot be read is an error, not a crash.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("wake: loading %s: damaged model: %v", m.Path, r)
		}
	}()
	// The engine sizes and indexes everything by what the file says, and an allocation a file talked
	// it into is fatal, not a panic the guard above can catch. So every model, the image's, Home
	// Assistant's or the setup page's, is held to the setup page's checks before the engine reads it.
	if err := wake.CheckMicro(m.Path); err != nil {
		return fmt.Errorf("wake: loading %s: %w", m.Path, err)
	}
	det, err := microwakeword.NewDetector(cfg)
	if err != nil {
		return fmt.Errorf("wake: loading %s: %w", m.Path, err)
	}
	step := mwwfront.StepMs(cfg.FeaturesStepMs)
	if _, ok := b.dets[m.ID]; ok {
		b.unload(m.ID) // a reload may change the step
	}
	b.dets[m.ID], b.step[m.ID] = det, step
	b.front(step)
	return nil
}

// run hands one frame of features to one model, and reports false if the model panicked on it. A model
// that loaded can still fall over on real audio, and the panic would otherwise take the wake service
// down with every other word in it, and again each time it came back with the same model. That model
// is dropped instead, said once in the log, and the slot it was in stays deaf until it is chosen again.
func (b *microBackend) run(id string, det microDetector, feat [][]int8) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("wake word dropped: its model failed on live audio", "id", id, "err", r)
			ok = false
		}
	}()
	det.ProcessFeatures(feat)
	return true
}

func (b *microBackend) unload(id string) {
	step := b.step[id]
	delete(b.dets, id)
	delete(b.step, id)
	delete(b.scores, id)
	for _, s := range b.step {
		if s == step {
			return
		}
	}
	delete(b.fronts, step)
}

// feed runs each front end over the frame once and hands every frame of features it makes to each
// model on that step. The cutoff is neverFires, so a detector never stops partway through a frame.
func (b *microBackend) feed(frame []int16) (map[string]float64, bool) {
	for stepMs, f := range b.fronts {
		f.buf = append(f.buf, frame...)
		for len(f.buf) >= f.window {
			feat := [][]int8{features(f.fe.ProcessFrame(f.buf[:f.window]))}
			for id, det := range b.dets {
				if b.step[id] == stepMs && !b.run(id, det, feat) {
					b.unload(id)
					b.gone = append(b.gone, id)
				}
			}
			f.buf = f.buf[f.step:]
		}
	}
	for id, det := range b.dets {
		b.scores[id] = det.SlidingAverage()
	}
	return b.scores, true
}

func (b *microBackend) failed() []string {
	gone := b.gone
	b.gone = nil
	return gone
}

func (b *microBackend) close() {
	clear(b.dets)
	clear(b.step)
	clear(b.fronts)
}

// owwBackend is openWakeWord: one mel and embedding chain, and a small classifier per wake word
// reading the same window of embeddings. A second wake word costs a classifier, not a front end.
type owwBackend struct {
	front  *oww.Engine
	scores map[string]float64
}

func (b *owwBackend) load(m wake.Model) (err error) {
	// The same guard the micro backend needs, for the same reason: a model file is not a trustworthy
	// document, and the interpreter builds a graph out of indices and shapes the file supplies. It
	// turns what it can name into an error itself, and this is the backstop for whatever it cannot.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("wake: loading %s: damaged model: %v", m.Path, r)
		}
	}()

	model, err := os.ReadFile(m.Path)
	if err != nil {
		return fmt.Errorf("wake: reading %s: %w", m.Path, err)
	}
	if _, err := b.front.Load(m.ID, model); err != nil {
		return fmt.Errorf("wake: loading %s: %w", m.Path, err)
	}
	return nil
}

func (b *owwBackend) unload(id string) {
	b.front.Unload(id)
	delete(b.scores, id)
}

func (b *owwBackend) feed(frame []int16) (map[string]float64, bool) {
	scores, err := b.front.Process(frame)
	if err != nil {
		slog.Error("wake scoring failed", "err", err)
		return nil, false
	}
	if len(scores) == 0 {
		return nil, false
	}

	for id, v := range scores {
		b.scores[id] = float64(v)
	}
	return b.scores, true
}

func (b *owwBackend) failed() []string { return nil }

func (b *owwBackend) close() {}
