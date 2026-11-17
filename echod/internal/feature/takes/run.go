package takes

// Record wake word: a counted run of takes of the device's own wake word, one after each short beep,
// each sent to the owner's wakesmith as a positive example (feature/detect clipsend.go, kind "take").
//
// It is the recorder's, with everything that goes with it: the Record takes of a word switch, a start
// at the device (Start in the screen's settings or on the card the setup page puts up, or the action
// button on a Dot), the microphones held from the start to the end, gaps and all, so that no take opens
// a turn, reaches Home Assistant's wake word stream or keeps a wake clip, and everything that throws a
// recording away ending the run. What differs is the cutting: one capture at a time, a second of the
// room before the beep and a few seconds after it, of which exactly one take is kept. A capture with
// none in it is asked for again, with the same number, and nothing is sent for it: wakesmith refuses a
// silent take, and a run that hears nothing three times in a row is over.
//
// The takes are the same frame the wake word scores (mic.Listen), after the noise reduction when it is
// on: "heard" then, "plain" with it off, where the two are one frame.

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// How many takes a run asks for, and how many seconds apart.
const (
	RunCountDefault = 20
	RunCountMin     = 5
	RunCountMax     = 50

	RunGapDefault = 3
	RunGapMin     = 2
	RunGapMax     = 10
)

const (
	// runPreRoll is the room kept from before each beep: what the cutter measures a take against.
	runPreRoll = time.Second

	// runListen is how long a capture runs from its beep: a word of up to longestTake, said a moment
	// after the beep, with its padding.
	runListen = 3500 * time.Millisecond

	// runEcho is how long from the beep its own sound may still be heard: the note, the playback buffer
	// after it (speaker.HardwareTail) and the way back into the microphones. Its frames count as the
	// device making a sound, so a take is never the beep, and "say it now" is shown once it is over.
	runEcho = 450 * time.Millisecond

	// runMisses is how many captures in a row may hear no take before the run is given up: nobody is
	// saying it, or not near enough to be heard.
	runMisses = 3
)

// beepNotes is the beep before each capture: short, and higher than the recorder's other notes.
var beepNotes = []speaker.Note{{Freq: 1047, Ms: 70}}

// run is a run asked for or running. Its counts are changed under the recorder's mu.
type run struct {
	id                 string // one per run, so wakesmith keeps a set together
	count              int
	gap                time.Duration
	word, front, frame string // what the takes are of, the front end it is scored on, the frame they are

	take, kept        int  // the take asked for now, from 1, and how many are on their way
	missed, listening bool // the last capture heard none; the beep is over and the word is to be said
}

// Capture is one take of a run, for feature/detect to keep until it is sent to wakesmith.
type Capture struct {
	Run   string    // the run's id
	Index int       // which take of the run, from 1
	Word  string    // the wake word's model id, as the wake clips name it
	Front string    // the front end it is scored on: "tflm", "zserge" or "oww"
	Frame string    // "heard" (after the noise reduction) or "plain" (with it off)
	At    time.Time // UTC
	PCM   []int16   // 16 kHz mono
}

// RunBlock is why a run cannot be asked for now, whatever else is going on.
type RunBlock int

const (
	RunReady RunBlock = iota
	RunNoMicrophone
	RunSwitchOff
	RunNotWakesmith
	RunNoWakeWord
)

// Text is the block in the setup page's words.
func (b RunBlock) Text() string {
	switch b {
	case RunNoMicrophone:
		return "this device has no microphones"
	case RunSwitchOff:
		return errOff.Error()
	case RunNotWakesmith:
		return "Set Send wake clips to wakesmith first"
	case RunNoWakeWord:
		return "no wake word is loaded yet; try again in a minute"
	}
	return ""
}

// RunResult is how the last run went, or that the last start from the screen was refused, and when.
type RunResult struct {
	Kept, Of int
	Refused  bool
	Why      string
	At       time.Time
}

// SetWakeWord is feature/detect giving the recorder the wake word a run's takes are of, with its front
// end, and where the takes go: its clip store, which sends them on.
func (f *Feature) SetWakeWord(word func() (word, front string), keep func(Capture)) {
	f.wakeWord.Store(&word)
	f.keepTake.Store(&keep)
}

// RunBlocked is why a run cannot be asked for, RunReady when it can.
func (f *Feature) RunBlocked() RunBlock {
	switch {
	case !layout.HasMicrophone():
		return RunNoMicrophone
	case !f.dev.switchedOn():
		return RunSwitchOff
	case f.dev.toWakesmith == nil || !f.dev.toWakesmith():
		return RunNotWakesmith
	}
	w, k := f.wakeWord.Load(), f.keepTake.Load()
	if w == nil || k == nil {
		return RunNoWakeWord
	}
	if word, _ := (*w)(); word == "" {
		return RunNoWakeWord
	}
	return RunReady
}

// ClampRun is a count and a gap in seconds made into ones a run takes.
func ClampRun(count, gap int) (int, int) {
	return min(max(count, RunCountMin), RunCountMax), min(max(gap, RunGapMin), RunGapMax)
}

// RunPlan is what the next run starts from: the last asked for, or the defaults.
func (f *Feature) RunPlan() (count, gap int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	count, gap = f.lastCount, f.lastGap
	if count == 0 {
		count = RunCountDefault
	}
	if gap == 0 {
		gap = RunGapDefault
	}
	return ClampRun(count, gap)
}

// SetRunPlan is the screen's steppers changing what the next run starts from.
func (f *Feature) SetRunPlan(count, gap int) {
	count, gap = ClampRun(count, gap)
	f.mu.Lock()
	f.lastCount, f.lastGap = count, gap
	f.mu.Unlock()
	f.Changed.Emit(struct{}{})
}

// LastRun is how the last run went.
func (f *Feature) LastRun() RunResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastRun
}

// plan is a run of count takes gap seconds apart, if one can be asked for. With op held.
func (f *Feature) plan(count, gap int) (*run, error) {
	if b := f.RunBlocked(); b != RunReady {
		return nil, errors.New(b.Text())
	}
	word, front := (*f.wakeWord.Load())()
	count, gap = ClampRun(count, gap)
	f.mu.Lock()
	f.lastCount, f.lastGap = count, gap
	f.mu.Unlock()
	return &run{id: newRunID(), count: count, gap: time.Duration(gap) * time.Second, word: word, front: front, take: 1}, nil
}

// newRunID is a run's id: random hex, which says nothing about the device.
func newRunID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%012x", time.Now().UnixNano()&0xffffffffffff)
	}
	return hex.EncodeToString(b)
}

// frameOf is which frame the takes are, by the microphone's settings when the run started: the wake
// word's own (mic.Listen) is after the noise reduction while it is on, and the frame before it with it
// off.
func frameOf(m config.Microphone) string {
	if m.Denoise {
		return "heard"
	}
	return "plain"
}

// ArmRun asks for a run from the setup page: it waits a minute for the press on the device, as Arm does.
func (f *Feature) ArmRun(count, gap int, session string) error {
	if session == "" {
		return errors.New("no browser asked for it")
	}
	f.op.Lock()
	defer f.op.Unlock()
	r, err := f.plan(count, gap)
	if err != nil {
		return err
	}
	return f.arm(r.word, Word, session, r)
}

// StartRun is Start in the screen's settings: the tap is the press, so the run starts now or not at all,
// and why not is kept for the settings to say (LastRun).
func (f *Feature) StartRun(count, gap int) error {
	f.op.Lock()
	err := func() error {
		r, err := f.plan(count, gap)
		if err != nil {
			return err
		}
		if err := f.arm(r.word, Word, "", r); err != nil {
			return err
		}
		f.mu.Lock()
		id := f.id
		f.mu.Unlock()
		f.begin(id)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.id != id || f.phase != Recording {
			return errors.New(cmp.Or(f.why, "it could not start"))
		}
		return nil
	}()
	f.op.Unlock()
	if err != nil {
		f.mu.Lock()
		f.lastRun = RunResult{Refused: true, Why: err.Error(), At: f.dev.now()}
		f.mu.Unlock()
		slog.Info("takes: a run of the wake word not started", "why", err)
		f.Changed.Emit(struct{}{})
	}
	return err
}

// heard is one frame of a capture: its samples, its level, and whether the device was making a sound.
type heard struct {
	pcm   []int16
	level float32
	over  bool
}

// framesIn is how many frames d holds.
func framesIn(d time.Duration) int { return int(d / frameTime) }

// runTakes is a run: the gap, the beep, a capture, and again, until enough takes are on their way, it is
// stopped, it hears nothing too many times in a row, or something with a better claim to the
// microphones ends it. The loop copies frames as record's does; a capture is cut once, when it ends,
// which is a handful of seconds of levels.
func (f *Feature) runTakes(ctx context.Context, id uint64, finish <-chan struct{}, r *run) {
	frameCh, stop := f.dev.listen("takes")
	f.note(startNotes...)

	preN, listenN, echoN, gapN := framesIn(runPreRoll), framesIn(runListen), framesIn(runEcho), framesIn(r.gap)
	back := make([]heard, 0, preN)
	var capture []heard // from a second before the beep; nil between captures
	beepAt := 0
	// The first beep once the start note has been heard out.
	wait := max(gapN, framesIn(ownHeard))
	take, kept, misses := 1, 0, 0

	last := f.dev.now()
	t := time.NewTicker(check)
	why := ""
	look := func() bool {
		if w, _ := f.over(); w != "" {
			why = w
			return true
		}
		if f.dev.now().Sub(last) > gap {
			why = "the microphones paused"
			return true
		}
		return false
	}
	set := func(change func(*run)) {
		f.mu.Lock()
		if f.id == id {
			change(r)
		}
		f.mu.Unlock()
		f.Changed.Emit(struct{}{})
	}

loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-finish:
			why = "stopped"
			break loop
		case p, ok := <-frameCh:
			if !ok {
				why = "the microphones stopped"
				break loop
			}
			last = f.dev.now()
			h := heard{pcm: p, level: rms(p), over: f.dev.audible()} // p is shared, and only read
			if capture == nil {
				if len(back) == preN {
					copy(back, back[1:])
					back = back[:preN-1]
				}
				back = append(back, h)
				if wait--; wait > 0 {
					continue
				}
				capture = append(make([]heard, 0, preN+listenN), back...)
				beepAt = len(capture)
				f.note(beepNotes...)
				continue
			}
			if len(capture)-beepAt < echoN {
				h.over = true
			}
			capture = append(capture, h)
			if len(capture)-beepAt == echoN {
				set(func(r *run) { r.listening = true })
			}
			if len(capture)-beepAt < listenN {
				continue
			}
			pcm, found := pickTake(capture, beepAt)
			capture, back, wait = nil, back[:0], gapN
			if found {
				f.store(Capture{Run: r.id, Index: take, Word: r.word, Front: r.front, Frame: r.frame,
					At: f.dev.now().UTC(), PCM: pcm})
				kept++
				take++
				misses = 0
			} else {
				misses++
				slog.Info("takes: no take heard; asking for it again", "take", take)
			}
			done := kept >= r.count
			set(func(r *run) {
				r.kept, r.missed, r.listening = kept, !found, false
				if !done {
					r.take = take
				}
			})
			if done {
				why = "done"
				break loop
			}
			if misses >= runMisses {
				why = fmt.Sprintf("the wake word wasn't heard %d times in a row", runMisses)
				break loop
			}
		case <-f.poke:
			if look() {
				break loop
			}
		case <-t.C:
			if look() {
				break loop
			}
		}
	}
	t.Stop()
	stop()
	f.release(id)
	f.note(stopNotes...)

	var said string
	switch why {
	case "done":
		said = fmt.Sprintf("%d of %d takes recorded, on their way to wakesmith", kept, r.count)
	case "stopped":
		said = fmt.Sprintf("stopped after %d of %d takes", kept, r.count)
	case "":
		said = fmt.Sprintf("ended after %d of %d takes", kept, r.count)
	default:
		said = fmt.Sprintf("%s, after %d of %d takes", why, kept, r.count)
	}
	f.mu.Lock()
	f.lastRun = RunResult{Kept: kept, Of: r.count, Why: said, At: f.dev.now()}
	if f.id == id {
		f.phase, f.why, f.cancel, f.run = Idle, said, nil, nil
	}
	f.mu.Unlock()
	slog.Info("takes: the run of the wake word is over", "kept", kept, "of", r.count, "why", said)
	f.Changed.Emit(struct{}{})
}

// store hands a take to feature/detect, off the loop: writing it must not cost the microphones frames.
func (f *Feature) store(c Capture) {
	k := f.keepTake.Load()
	if k == nil {
		return
	}
	safe.Go("wake word take", func() { (*k)(c) })
}

// pickTake is the take in a capture whose beep is at beepAt, and whether it has one: the loudest take
// heard after the beep. One the cutter left out only for being near the beep is taken too, when the
// word itself is clear of it (by widen, as the cutter asks), its padding cut short at the beep's end. A
// take before the beep is somebody talking over the gap, and is not the one asked for.
func pickTake(c []heard, beepAt int) ([]int16, bool) {
	n := len(c)
	level, over := make([]float32, n), make([]bool, n)
	for i, h := range c {
		level[i], over[i] = h.level, h.over
	}
	found, left := cut(level, over)
	padF, widenF := framesIn(pad), framesIn(widen)

	best, loudest := span{}, float32(-1)
	consider := func(s, word span) {
		if l := slices.Max(level[word.from:word.to]); l > loudest {
			best, loudest = s, l
		}
	}
	for _, s := range found {
		if s.from >= beepAt {
			consider(s, s)
		}
	}
	for _, l := range left {
		if l.why != whySpeaker || l.from < beepAt {
			continue
		}
		if slices.Contains(over[max(l.from-widenF, 0):min(l.to+widenF, n)], true) {
			continue
		}
		from := max(l.from-padF, 0)
		for i := l.from - 1; i >= from; i-- {
			if over[i] {
				from = i + 1
				break
			}
		}
		to := min(l.to+padF, n)
		for i := l.to; i < to; i++ {
			if over[i] {
				to = i
				break
			}
		}
		consider(span{from, to}, l.span)
	}
	if loudest < 0 {
		return nil, false
	}
	pcm := make([]int16, 0, (best.to-best.from)*mic.FrameSamples)
	for _, h := range c[best.from:best.to] {
		pcm = append(pcm, h.pcm...)
	}
	return pcm, true
}
