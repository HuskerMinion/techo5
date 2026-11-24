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
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
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
// crossed reports successes, not failures. A slot may have a lower floor of its own for the log
// (Engine.NearMissFloor); NearMiss stays what anything acting on a near miss is told about.
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

	// board is what the words have scored lately, for the setup page (board.go).
	board board

	// keepers keep the slots' highest scores, each for whoever asked for it (NewPeaks): a call's, a
	// reply's.
	keepers []*PeakKeeper

	// Threshold is asked for every score, so a change in Home Assistant takes effect at once. It is
	// per slot because the models disagree on scale.
	Threshold func(slot int) float64

	// OnDetect runs on a detection, off the audio path.
	OnDetect func(slot int)

	// OnNearMiss runs when an utterance peaked over NearMiss and fell away without firing, with the peak
	// it reached, off the audio path. A near miss is the only sign the device gets that somebody tried
	// and was not heard, which is the one moment when getting out of their way is worth something.
	//
	// It is NearMiss, whatever NearMissFloor says: a peak under it that the floor lets through is
	// logged and listed for the setup page, and nothing else. What acts on a near miss (the ducker, the
	// ring's hush, the wake clips) was tuned for NearMiss and keeps it.
	OnNearMiss func(slot int, peak float64)

	// NearMissFloor is the lowest peak worth a near miss line for a slot, asked when the utterance peaks,
	// with the cutoff then in force; nil, or a slot it has no opinion on, is NearMiss. It only ever
	// matters under NearMiss: see OnNearMiss. A line for a peak under NearMiss gives that cutoff, not the
	// one in force when the near miss settles.
	NearMissFloor func(slot int, cutoff float64) float64

	// OnScored runs once the hold after a detection ends, with the peak the utterance reached and the
	// cutoff it was judged against, off the audio path. A false wake's score is what says how far the
	// threshold has to go up to be rid of it.
	OnScored func(slot int, peak, cutoff float64)

	// OnFrames runs every framesEvery, off the audio path, with how the loop kept up since the last
	// time: what the "wake frames" log line says, which adds these up over framesLogEvery.
	OnFrames func(Frames)

	// Candidate is the lowest score worth asking Home Assistant about, per slot; zero, or nil, asks
	// about nothing. OnCandidate runs, off the audio path, when an utterance went over it and fell
	// away again without reaching the slot's threshold, with the peak it reached: a wake that is
	// either a word said unlike the voices the model learned from or a sound like it, which Home
	// Assistant's engine can tell apart (see voice's confirm).
	// OnCandidateStart runs, off the audio path, as an utterance first goes over it: the moment a
	// device sure of the word would claim it (feature/nearest), where OnCandidate only comes once the
	// utterance has fallen away, up to CandidateFor later (#685).
	Candidate        func(slot int) float64
	OnCandidate      func(slot int, peak float64)
	OnCandidateStart func(slot int)
	// OnFrame sees every frame the detector scores, with the engine's lock held: it must only copy
	// (clips.go).
	OnFrame func(frame []int16)

	// OnReady runs once per slot, the first time its model produces a score. That is the first moment
	// the wake word could be detected at all: these are streaming models with a window to fill, and
	// openWakeWord gives nothing until it has about two seconds of audio. Loading is not hearing, and
	// this is the difference.
	OnReady func(slot int)

	// OnSlots is told, on a goroutine of its own, that the word in a slot was loaded, cleared or dropped:
	// for what keeps a copy of it rather than asking Loaded each time (a run of wake word takes).
	OnSlots func()

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

	// peakFloor is the near miss floor (nearMissFloor) as it stood when the peak was reached, and
	// peakCutoff the cutoff it came from. The floor follows the cutoff, and the cutoff moves as the
	// speaker starts and stops: a word said as a reply ends peaks against the cutoff for playing, and is
	// settled against the quiet room's a moment later. What it came close to is the cutoff it was said
	// against.
	peakFloor, peakCutoff float64

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
	e.board.forgetAll()
	e.slotsChanged()
}

// slotsChanged tells OnSlots that a slot's word changed, off the caller's goroutine: it is called with
// mu held, and whoever hears it asks Loaded. Each look is at the slots as they are when it runs, so the
// last one sees the last change.
func (e *Engine) slotsChanged() {
	if f := e.OnSlots; f != nil {
		safe.Go("wake word slots changed", f)
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
	e.board.forget(n)
	e.slotsChanged()

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
	e.board.forget(n)
	e.slotsChanged()
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

// FrontOf is the front end the model in slot n is scored on: "tflm" (microWakeWord's exact one,
// mwwfront), "zserge" (its approximation) or "oww" (openWakeWord), or "" with nothing loaded there. All
// of them read the same frame here (mic.Listen); a run of wake word takes says which one its word is
// on (feature/takes run.go).
func (e *Engine) FrontOf(n int) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if n < 0 || n >= len(e.slots) || !e.slots[n].loaded {
		return ""
	}
	m := e.slots[n].model
	switch b := e.backends[m.Kind].(type) {
	case *microBackend:
		k, ok := b.step[m.ID]
		if !ok {
			return ""
		}
		if k.exact {
			return "tflm"
		}
		return "zserge"
	case *owwBackend:
		return "oww"
	}
	return ""
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

// Margin is how far a slot's latest detection crossed over the cutoff it was judged against, so that words
// with cutoffs of their own can be compared by how surely each was heard.
func (e *Engine) Margin(n int) float64 {
	e.mu.Lock()
	defer e.mu.Unlock()

	if n < 0 || n >= len(e.slots) {
		return 0
	}
	return e.slots[n].crossing - e.slots[n].crossCutoff
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
		logged      = newFrameTally(since)
		// first is the first window, whose line is written at once: it says the loop is keeping up
		// from the start, not ten minutes in.
		first = true
	)
	// What came since the last line is written as the loop stops, so the minutes before a restart or
	// the microphones going away are in the log too.
	defer func() {
		e.mu.Lock()
		by := maps.Clone(e.spent)
		e.mu.Unlock()
		logged.add(got, took, worst, by)
		if logged.got > 0 {
			slog.Info("wake frames", logged.attrs(time.Now())...)
		}
	}()

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

			if elapsed := time.Since(since); elapsed >= framesEvery {
				e.mu.Lock()
				by := make(map[wake.Kind]time.Duration, len(e.spent))
				for k, d := range e.spent {
					by[k] = d
					e.spent[k] = 0
				}
				e.mu.Unlock()

				f := Frames{PerSecond: float64(got) / elapsed.Seconds(), Busy: int(100 * took.Seconds() / elapsed.Seconds()),
					Worst: worst}
				if e.OnFrames != nil {
					safe.Go("wake frames", func() { e.OnFrames(f) })
				}
				now := time.Now()
				logged.add(got, took, worst, by)
				if first || logged.due(now) {
					slog.Info("wake frames", logged.attrs(now)...)
					logged, first = newFrameTally(now), false
				}
				took, worst, got, since = 0, 0, 0, now
			}
		}
	}
}

// framesEvery is how often the loop says how it kept up to OnFrames, which the wake frames entities
// show.
const framesEvery = 30 * time.Second

// framesLogEvery is how often the "wake frames" log line is written, covering everything since the
// last one. Written every 30 s it was most of a Dot's log, and on eMMC every log line costs flash; a
// line every ten minutes still shows a loop that falls behind, and the entities keep their 30 s view.
const framesLogEvery = 10 * time.Minute

// frameTally adds framesEvery's windows up into the one the log line reports.
type frameTally struct {
	since       time.Time
	got         int
	took, worst time.Duration
	by          map[wake.Kind]time.Duration
}

func newFrameTally(since time.Time) frameTally {
	return frameTally{since: since, by: make(map[wake.Kind]time.Duration)}
}

func (t *frameTally) add(got int, took, worst time.Duration, by map[wake.Kind]time.Duration) {
	t.got += got
	t.took += took
	t.worst = max(t.worst, worst)
	for k, d := range by {
		t.by[k] += d
	}
}

// due is whether the tally covers framesLogEvery, and so whether the line is written.
func (t *frameTally) due(now time.Time) bool { return now.Sub(t.since) >= framesLogEvery }

// attrs is the log line's fields: the same ones the 30 s line had, over the longer window.
func (t *frameTally) attrs(now time.Time) []any {
	elapsed := now.Sub(t.since).Seconds()
	n := int64(max(t.got, 1))
	out := []any{
		"per_second", float64(t.got) / elapsed,
		"mean_us", t.took.Microseconds() / n,
		"worst_ms", t.worst.Milliseconds(),
		"busy_pct", int(100 * t.took.Seconds() / elapsed),
	}
	for k, d := range t.by {
		out = append(out, string(k)+"_us", d.Microseconds()/n)
	}
	return out
}

// Frames is how the wake word loop kept up over framesEvery: frames scored a second (fifty when none
// are lost), the share of the time scoring took, and the slowest frame. Busy is what a wake word costs
// the device: an openWakeWord word runs around 75 percent of a core on a Dot, a microWakeWord one 10.
type Frames struct {
	PerSecond float64
	Busy      int
	Worst     time.Duration
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
			e.board.event(wakeword.ScoreEvent{At: now, Slot: i + 1, What: "ready", ID: s.model.ID})
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
					e.board.event(wakeword.ScoreEvent{At: time.Now(), Slot: i + 1, What: "dropped", ID: id})
					e.slots[i] = slot{}
					e.drop(s)
					e.board.forget(i)
					e.slotsChanged()
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
	// Every score, the held back ones too, for somebody watching on the setup page, and for whoever is
	// keeping the peaks. To them a score in the hold after a detection is held back as well: it could
	// not have fired either.
	settling := now.Before(s.quiet)
	e.board.heard(n, score, now, settling)
	for _, k := range e.keepers {
		k.note(n, s.model.ID, score, cutoff, settling || s.holding, now)
	}

	if settling {
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
		e.board.event(wakeword.ScoreEvent{At: now, Slot: n + 1, What: "detected", ID: s.model.ID, Peak: s.peak,
			Crossing: s.crossing, Cutoff: s.crossCutoff})
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
			s.peakFloor, s.peakCutoff = e.nearMissFloor(n, cutoff), cutoff
		}
		if now.Sub(s.peakAt) <= NearMissSettle {
			return
		}
		// The utterance peaked and fell away without firing.
		if s.peak > 0 && s.peak >= s.peakFloor {
			// A peak under NearMiss is only here for a floor of its slot's own. Its line has that floor
			// in it, to say why it is there, and the cutoff the floor came from, the one in force at the
			// peak, so the two agree and say how close the try came. Every line there was before reads
			// as it always has, with the cutoff in force now.
			against, floor := cutoff, []any(nil)
			if s.peak < NearMiss {
				against, floor = s.peakCutoff, []any{"floor", s.peakFloor}
			}
			slog.Info("wake near miss", append([]any{"slot", n + 1, "id", s.model.ID, "peak", s.peak,
				"cutoff", against, "dropped", source.Dropped()}, floor...)...)
			e.board.event(wakeword.ScoreEvent{At: now, Slot: n + 1, What: "near miss", ID: s.model.ID, Peak: s.peak,
				Cutoff: against})
			if e.OnNearMiss != nil && s.peak >= NearMiss {
				i, peak := n, s.peak
				safe.Go("wake near miss", func() { e.OnNearMiss(i, peak) })
			}
		}
		// A peak that settled under its floor is let go of as well. With the one floor every slot used to
		// have this changed nothing, since a peak under it could never be reported. With a floor that
		// moves, a peak left over from a quiet room would stand over the next try, said over a reply,
		// that peaked lower than it but over the reply's floor: that try would set no peak of its own,
		// and leave no line.
		s.peak = 0
		return
	}

	// The word reached the threshold on its own: nothing to ask about.
	s.candidate = false

	// Feedback fires now; the peak is only for the log, and the slot settles when the hold ends.
	s.holding, s.holdEnds = true, now.Add(Hold)
	s.crossing, s.peak, s.crossCutoff = score, score, cutoff
	for _, k := range e.keepers {
		k.fired(n)
	}

	if e.OnDetect != nil {
		safe.Go("wake detected", func() { e.OnDetect(n) })
	}
}

// nearMissFloor is the lowest peak worth a near miss line for slot n against cutoff: NearMissFloor's
// answer, never over NearMiss, so a floor of a slot's own can only ever add lines.
func (e *Engine) nearMissFloor(n int, cutoff float64) float64 {
	if e.NearMissFloor == nil {
		return NearMiss
	}
	return min(e.NearMissFloor(n, cutoff), NearMiss)
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
			if e.OnCandidateStart != nil {
				i := n
				safe.Go("wake candidate start", func() { e.OnCandidateStart(i) })
			}
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
	e.board.event(wakeword.ScoreEvent{At: now, Slot: n + 1, What: "unsure", ID: s.model.ID, Peak: peak, AskedFrom: bound,
		Cutoff: cutoff})
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

// Quieted is whether the engines are down for a cut microphone (Quiet).
func (e *Engine) Quieted() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.quiet
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
