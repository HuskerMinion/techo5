// Package detect listens for wake words in the microphone frames.
//
// The engine is the loop; around it sits what the user chose, which the engine does not know: which
// words go in which slot, how sensitive each one is, and what to tell Home Assistant about the ones
// that would not load.
package detect

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// Hold is how long scoring continues after a detection fires, to record the peak the utterance
// reached. Feedback happens at once; this only decides what gets logged.
const Hold = 300 * time.Millisecond

// Refractory is how long detection pauses after a hit, so one utterance reports once rather than
// once per frame. Keep it short: anything longer swallows a genuine second attempt, which looks
// exactly like a failure to detect.
const Refractory = 800 * time.Millisecond

// NearMiss is the peak score worth reporting for an utterance that never fired, and NearMissSettle
// how long after that peak to wait before deciding it is not going to. The sliding average ramps
// through the low scores on its way to a real detection, so reporting as soon as the threshold is
// crossed reports successes, not failures.
const (
	NearMiss       = 0.5
	NearMissSettle = 700 * time.Millisecond
)

// Engine feeds microphone frames to the wake words of one backend.
//
// Slots are Home Assistant's: it offers a fixed number of wake word choices, each paired with its
// own pipeline, and a detection has to say which one fired so the right pipeline runs. A slot with
// no model is off, and all slots off is detection off — there is nothing else to switch.
type Engine struct {
	source *mic.Source

	mu sync.Mutex

	// backends is one engine per kind of model in use, built when a slot first wants it and dropped
	// when the last slot using it goes. Both can be up at once, a slot each, and each costs a front
	// end per frame — so which are running follows what is loaded rather than a setting.
	backends map[wake.Kind]backend

	// spent is how long each kind of model has taken since the last report, which is what says where
	// a frame's thirty milliseconds go.
	spent map[wake.Kind]time.Duration
	slots []slot

	// quiet is the microphones being cut: the backends are dropped while it holds. See Quiet.
	quiet bool

	// Threshold is asked for every score, so a change in Home Assistant takes effect at once. It is
	// per slot because the models disagree on scale.
	Threshold func(slot int) float64

	// OnDetect runs on a detection, off the audio path.
	OnDetect func(slot int)

	// OnNearMiss runs when an utterance peaked over NearMiss and fell away without firing, with the peak
	// it reached, off the audio path. A near miss is the only sign the device gets that somebody tried
	// and was not heard, which is the one moment when getting out of their way is worth something.
	OnNearMiss func(slot int, peak float64)

	// OnScored runs once the hold after a detection ends, with the peak the utterance reached and the
	// cutoff it was judged against, off the audio path. A false wake's score is what says how far the
	// threshold has to go up to be rid of it.
	OnScored func(slot int, peak, cutoff float64)

	// Candidate is the lowest score worth asking Home Assistant about, per slot; zero, or nil, asks
	// about nothing. OnCandidate runs, off the audio path, when an utterance went over it and fell
	// away again without reaching the slot's threshold, with the peak it reached: a wake that is
	// either a word said unlike the voices the model learned from or a sound like it, which Home
	// Assistant's engine can tell apart (see voice's confirm).
	Candidate   func(slot int) float64
	OnCandidate func(slot int, peak float64)
	// OnFrame sees every frame the detector scores, with the engine's lock held: it must only copy
	// (clips.go).
	OnFrame func(frame []int16)

	// OnReady runs once per slot, the first time its model produces a score. That is the first moment
	// the wake word could be detected at all: these are streaming models with a window to fill, and
	// openWakeWord gives nothing until it has about two seconds of audio. Loading is not hearing, and
	// this is the difference.
	OnReady func(slot int)

	// OnDropped runs, off the audio path, when a slot's model failed on the audio and was taken out of
	// it (dropFailed), with the id it held.
	OnDropped func(slot int, id string)

	// Load puts the wake words in once the engine is up. It runs on every start, including a restart,
	// because an engine that came back empty would leave the device deaf while claiming otherwise.
	// Which words those are depends on what the user last chose, which is not this package's business.
	Load func() error
}

// slot is one wake word and how its scores are being read. The tracking is per slot: two wake words
// listening to the same audio peak at different moments, and one firing must not silence the other.
type slot struct {
	model  wake.Model
	loaded bool

	// scored is whether this slot has seen a score yet, which is what makes it able to detect anything.
	// It goes back to false with the slot, so a wake word that is swapped out has to warm up again.
	scored bool

	quiet      time.Time
	peak       float64
	peakAt     time.Time
	suppressed int

	// While holding, a detection has already fired and scoring continues only to find the peak the
	// utterance reached, so hits and misses are both reported as peaks.
	holding  bool
	holdEnds time.Time
	crossing float64
	// crossCutoff is the cutoff the crossing was judged against. By the time the hold ends the wake tone
	// is playing, and a threshold read then carries the canceller's slack (thresholdFor), which is not
	// what the word had to reach.
	crossCutoff float64

	// candidate is an utterance over Candidate and under the threshold so far: when it went over, and
	// the highest it has reached.
	candidate     bool
	candidateAt   time.Time
	candidatePeak float64

	// askedUntil keeps one utterance from being asked about twice. It is the candidates' own pause, not
	// quiet: a clear repeat of the word straight after one that was asked about still wakes.
	askedUntil time.Time
}

// MinCandidate is the lowest Candidate can be (the Confirm threshold's minimum). A score under it is
// no candidate whatever the setting, so the setting is not looked at for it: that is nearly every frame.
const MinCandidate = 0.5

// CandidateFor is the longest an utterance stays a candidate before it is asked about anyway. A word
// over the line falls back within a few frames; one that stays there is a sound that goes on, and
// waiting on it would hold the question back for as long as it lasts.
const CandidateFor = 600 * time.Millisecond

// New describes an engine. Nothing is built until Start.
func New(slots int, source *mic.Source) *Engine {
	return &Engine{
		backends: map[wake.Kind]backend{},
		spent:    map[wake.Kind]time.Duration{},
		slots:    make([]slot, slots),
		source:   source,
	}
}

func (e *Engine) Name() string { return "wake" }

// Start empties the engine and loads the wake words. A restart comes back through here, so a detector
// that broke is rebuilt from scratch rather than resumed from whatever state it died in.
func (e *Engine) Start(context.Context) error {
	e.mu.Lock()
	e.reset()
	e.mu.Unlock()

	if e.Load == nil {
		return nil
	}
	return e.Load()
}

// Close drops the engines and everything loaded in them.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.reset()
	return nil
}

// reset closes every engine and empties every slot. Held with mu.
func (e *Engine) reset() {
	for k, b := range e.backends {
		b.close()
		delete(e.backends, k)
	}
	for i := range e.slots {
		e.slots[i] = slot{}
	}
}

// build makes an engine of one kind. A test replaces it with one that needs no model files.
var build = newBackend

// engineFor is the backend that runs a kind of model, built the first time one is wanted.
func (e *Engine) engineFor(k wake.Kind) (backend, error) {
	if b, ok := e.backends[k]; ok {
		return b, nil
	}
	b, err := build(k)
	if err != nil {
		return nil, err
	}
	e.backends[k] = b
	return b, nil
}

// Use loads a wake word into a slot, in whichever engine runs that model.
func (e *Engine) Use(n int, m wake.Model) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if n < 0 || n >= len(e.slots) {
		return fmt.Errorf("wake: slot %d out of range", n)
	}
	if s := e.slots[n]; s.loaded && s.model.ID == m.ID {
		return nil
	}

	b, err := e.engineFor(m.Kind)
	if err != nil {
		return err
	}
	if err := b.load(m); err != nil {
		e.drop(slot{model: m, loaded: true})
		return err
	}

	old := e.slots[n]
	e.slots[n] = slot{model: m, loaded: true}
	e.drop(old)

	slog.Info("wake word loaded", "slot", n+1, "phrase", m.Phrase, "id", m.ID, "engine", m.Kind)
	return nil
}

// Clear switches a slot off.
func (e *Engine) Clear(n int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if n < 0 || n >= len(e.slots) {
		return
	}
	if e.slots[n].loaded {
		slog.Info("wake word cleared", "slot", n+1, "id", e.slots[n].model.ID)
	}

	old := e.slots[n]
	e.slots[n] = slot{}
	e.drop(old)
}

// drop lets go of what a slot was using: the model, unless another slot still wants it, and then the
// engine itself, unless another slot still needs that. An engine with nothing loaded would go on
// costing a front end for every frame. Called with the slot table already updated, so what it asks
// about the other slots is the truth. Held with mu.
func (e *Engine) drop(was slot) {
	if !was.loaded {
		return
	}
	b := e.backends[was.model.Kind]
	if b == nil {
		return
	}

	if !e.uses(was.model.ID) {
		b.unload(was.model.ID)
	}
	if !e.runs(was.model.Kind) {
		b.close()
		delete(e.backends, was.model.Kind)
	}
}

// uses reports whether a slot is listening for a model. Held with mu.
func (e *Engine) uses(id string) bool {
	for _, s := range e.slots {
		if s.loaded && s.model.ID == id {
			return true
		}
	}
	return false
}

// runs reports whether a slot needs an engine. Held with mu.
func (e *Engine) runs(k wake.Kind) bool {
	for _, s := range e.slots {
		if s.loaded && s.model.Kind == k {
			return true
		}
	}
	return false
}

// Loaded reports the wake word in a slot, and whether the slot is on.
func (e *Engine) Loaded(n int) (wake.Model, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if n < 0 || n >= len(e.slots) {
		return wake.Model{}, false
	}
	return e.slots[n].model, e.slots[n].loaded
}

// Crossing is the score a slot's latest detection crossed its threshold at: what OnDetect fired on.
// The peak the utterance went on to reach is only known when the hold ends (OnScored).
func (e *Engine) Crossing(n int) float64 {
	e.mu.Lock()
	defer e.mu.Unlock()

	if n < 0 || n >= len(e.slots) {
		return 0
	}
	return e.slots[n].crossing
}

// Ready reports whether a slot has scored, and so whether its wake word can be heard yet. A slot with
// nothing loaded is never ready.
func (e *Engine) Ready(n int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	if n < 0 || n >= len(e.slots) {
		return false
	}
	return e.slots[n].scored
}

// Listening reports whether any slot has a wake word.
func (e *Engine) Listening() bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, s := range e.slots {
		if s.loaded {
			return true
		}
	}
	return false
}

// Run scores frames until ctx is canceled. The microphones going away is reported rather than
// returned quietly: detection stopping is the device going deaf, which should not be silent.
func (e *Engine) Run(ctx context.Context) error {
	frames, unlisten := e.source.Listen("wake")
	defer unlisten()

	// The microphones produce fifty frames a second. Counting what arrives against what scoring costs
	// says whether frames are being lost because this loop is slow or because it never sees them.
	var (
		took, worst time.Duration
		got         int
		since       = time.Now()
	)

	for {
		select {
		case <-ctx.Done():
			return nil
		case frame, ok := <-frames:
			if !ok {
				return errors.New("wake: the microphones stopped")
			}

			began := time.Now()
			e.score(frame, e.source)
			spent := time.Since(began)

			got++
			took += spent
			worst = max(worst, spent)

			if elapsed := time.Since(since); elapsed >= 30*time.Second {
				e.mu.Lock()
				by := make([]any, 0, 8)
				for k, d := range e.spent {
					by = append(by, string(k)+"_us", d.Microseconds()/int64(max(got, 1)))
					e.spent[k] = 0
				}
				e.mu.Unlock()

				slog.Info("wake frames", append([]any{
					"per_second", float64(got) / elapsed.Seconds(),
					"mean_us", took.Microseconds() / int64(max(got, 1)),
					"worst_ms", worst.Milliseconds(),
					"busy_pct", int(100 * took.Seconds() / elapsed.Seconds()),
				}, by...)...)
				took, worst, got, since = 0, 0, 0, time.Now()
			}
		}
	}
}

// score feeds one frame and acts on what came back. The lock is held across the whole of it: an
// engine is shared with whatever Use installs next, and it is not safe to swap a wake word out from
// under an inference.
func (e *Engine) score(frame []int16, source *mic.Source) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Nothing to hear and nothing to do. See Quiet.
	if e.quiet {
		return
	}
	if e.OnFrame != nil {
		e.OnFrame(frame)
	}

	// Always feed. These are streaming models: a gap in the input is a gap in their history, and the
	// next utterance gets scored from a cold state. The refractory below suppresses reporting, not
	// audio. An engine only exists while a slot needs it, so nothing loaded feeds nothing.
	// Only what came back fresh is kept, so a slot whose engine has not scored this frame finds
	// nothing rather than reading a stale number. openWakeWord scores every 80 ms, so for it most
	// frames only advance the front end.
	heard := make(map[wake.Kind]map[string]float64, len(e.backends))
	for k, b := range e.backends {
		began := time.Now()
		scores, fresh := b.feed(frame)
		e.spent[k] += time.Since(began)
		if fresh {
			heard[k] = scores
		}
	}
	e.dropFailed()

	now := time.Now()
	for i := range e.slots {
		s := &e.slots[i]
		if !s.loaded {
			continue
		}
		score, ok := heard[s.model.Kind][s.model.ID]
		if !ok {
			continue
		}
		if !s.scored {
			s.scored = true
			slog.Info("wake word ready", "slot", i+1, "id", s.model.ID)
			if e.OnReady != nil {
				n := i
				safe.Go("wake word ready", func() { e.OnReady(n) })
			}
		}
		e.judge(i, s, score, now, source)
	}
}

// dropFailed takes the wake words a backend gave up on during feed out of their slots, so a slot does
// not go on claiming a word it can no longer hear, and choosing it again loads it afresh. Held with mu.
func (e *Engine) dropFailed() {
	for k, b := range e.backends {
		for _, id := range b.failed() {
			for i := range e.slots {
				if s := e.slots[i]; s.loaded && s.model.Kind == k && s.model.ID == id {
					slog.Error("wake word taken out of its slot: its model failed", "slot", i+1, "id", id)
					e.slots[i] = slot{}
					e.drop(s)
					if e.OnDropped != nil {
						n, id := i, id
						safe.Go("wake word dropped", func() { e.OnDropped(n, id) })
					}
				}
			}
		}
	}
}

// judge turns one slot's score into a detection, a near miss, or nothing. Held with mu.
func (e *Engine) judge(n int, s *slot, score float64, now time.Time, source *mic.Source) {
	cutoff := config.DefaultThreshold
	if e.Threshold != nil {
		cutoff = e.Threshold(n)
	}

	if now.Before(s.quiet) {
		s.suppressed++
		return
	}
	if s.suppressed > 0 {
		slog.Debug("detections suppressed while settling", "slot", n+1, "frames", s.suppressed)
		s.suppressed = 0
	}

	if s.holding {
		if score > s.peak {
			s.peak = score
		}
		if now.Before(s.holdEnds) {
			return
		}

		slog.Info("wake detected", "slot", n+1, "id", s.model.ID, "peak", s.peak,
			"crossing", s.crossing, "cutoff", s.crossCutoff, "dropped", source.Dropped())
		if e.OnScored != nil {
			i, peak, cutoff := n, s.peak, s.crossCutoff
			safe.Go("wake scored", func() { e.OnScored(i, peak, cutoff) })
		}
		s.holding, s.peak = false, 0
		s.quiet = now.Add(Refractory)
		return
	}

	if score < cutoff {
		e.candidate(n, s, score, cutoff, now)
		if score > s.peak {
			s.peak, s.peakAt = score, now
		}
		// The utterance peaked and fell away without firing.
		if s.peak >= NearMiss && now.Sub(s.peakAt) > NearMissSettle {
			slog.Info("wake near miss", "slot", n+1, "id", s.model.ID, "peak", s.peak,
				"cutoff", cutoff, "dropped", source.Dropped())
			if e.OnNearMiss != nil {
				i, peak := n, s.peak
				safe.Go("wake near miss", func() { e.OnNearMiss(i, peak) })
			}
			s.peak = 0
		}
		return
	}

	// The word reached the threshold on its own: nothing to ask about.
	s.candidate = false

	// Feedback fires now; the peak is only for the log, and the slot settles when the hold ends.
	s.holding, s.holdEnds = true, now.Add(Hold)
	s.crossing, s.peak, s.crossCutoff = score, score, cutoff

	if e.OnDetect != nil {
		safe.Go("wake detected", func() { e.OnDetect(n) })
	}
}

// candidate follows an utterance between Candidate and the threshold, and reports it once it falls
// away, or has stayed there for CandidateFor. Held with mu.
func (e *Engine) candidate(n int, s *slot, score, cutoff float64, now time.Time) {
	if !s.candidate && (score < MinCandidate || now.Before(s.askedUntil)) {
		return
	}
	bound := 0.0
	if e.Candidate != nil {
		bound = e.Candidate(n)
	}
	if bound <= 0 || bound >= cutoff {
		s.candidate = false
		return
	}
	if score >= bound {
		if !s.candidate {
			s.candidate, s.candidateAt, s.candidatePeak = true, now, score
		}
		s.candidatePeak = max(s.candidatePeak, score)
		if now.Sub(s.candidateAt) < CandidateFor {
			return
		}
	} else if !s.candidate {
		return
	}

	peak := s.candidatePeak
	s.candidate = false
	s.askedUntil = now.Add(Refractory)
	slog.Info("wake candidate", "slot", n+1, "id", s.model.ID, "peak", peak, "asked_from", bound, "cutoff", cutoff)
	if e.OnCandidate != nil {
		safe.Go("wake candidate", func() { e.OnCandidate(n, peak) })
	}
}

// Settle holds every slot's detections back for d from now, but the spared ones, while scoring goes
// on: for a sound of the device's own about to play into microphones that cannot yet tell it from the
// room. A hold already running for longer is kept.
func (e *Engine) Settle(d time.Duration, spared ...int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	until := time.Now().Add(d)
	for i := range e.slots {
		if slices.Contains(spared, i) {
			continue
		}
		if e.slots[i].quiet.Before(until) {
			e.slots[i].quiet = until
		}
	}
}

// Quiet drops the engines while the microphones are cut, and builds them again when they come back.
//
// A cut microphone hands on silence, so nothing can be detected in it — but the models were still
// being run over that silence, frame after frame, which on a Dot is a third of the time it has.
// Reported as "muting does not disable wake word detection", and the reporter was right about what
// the CPU was doing even though nothing could ever have fired.
//
// The slots are kept. What is dropped is the engines under them, which is where both the work and
// the streaming state live, so coming back is the same cold start as a wake word freshly chosen
// rather than a window holding audio from before the mute and audio from after it, spliced.
func (e *Engine) Quiet(on bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if on == e.quiet {
		return
	}
	e.quiet = on

	if on {
		for k, b := range e.backends {
			b.close()
			delete(e.backends, k)
		}
		// The new backend will be cold. Keep the choice of model, but none of
		// the old backend's readiness, utterance, or refractory state.
		for i, s := range e.slots {
			e.slots[i] = slot{model: s.model, loaded: s.loaded}
		}
		slog.Info("wake words stopped: the microphones are cut")
		return
	}

	var back []string
	for i := range e.slots {
		s := e.slots[i]
		if !s.loaded {
			continue
		}
		b, err := e.engineFor(s.model.Kind)
		if err != nil {
			slog.Error("wake word engine unavailable after unmute", "id", s.model.ID, "err", err)
			continue
		}
		if err := b.load(s.model); err != nil {
			slog.Error("reloading a wake word after unmute failed", "id", s.model.ID, "err", err)
			continue
		}
		back = append(back, s.model.ID)
	}
	slog.Info("wake words listening again", "active", back)
}
