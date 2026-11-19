package takes

// Record wake word: a counted run of takes of the device's own wake word, one after each short beep,
// each sent to the owner's wakesmith as a positive example (feature/detect clipsend.go, kind "take").
//
// It is the recorder's, with everything that goes with it: a start at the device (Start in the
// screen's settings or on the card the setup page puts up, or the action button on a Dot), the Record
// takes of a word switch when the setup page asked for it (Start in the screen's own settings is the
// consent by itself), the microphones held from the start to the end, gaps and all, so that no take opens
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

	// runEcho is how long from the beep its own sound may still be heard: the 70 ms note, the playback
	// buffer after it (speaker.HardwareTail, 150 ms) and a little for the way back into the microphones.
	// Its frames count as the device making a sound, so a take is never the beep, and "say it now" is
	// shown once it is over. It was 450 ms at first; on a Show 8 people said the word sooner than that
	// and most captures were thrown away (pickTake now also keeps a word that began inside it).
	runEcho = 250 * time.Millisecond

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

	sendTo    string // the wakesmith it started sending to: a run ends when that changes (over)
	switchWas bool   // Record takes of a word was on as it started: it going off ends the run
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
	RunClockNotSet
	RunNoWakeWord
	RunTakesKept
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
	case RunClockNotSet:
		return "The clock isn't set yet"
	case RunNoWakeWord:
		return "no wake word is loaded yet; try again in a minute"
	case RunTakesKept:
		return "takes of a word are kept on this device for the setup page to download; download or throw them away first"
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

// SlotWord is a wake word loaded in one of the device's slots, which a run's takes can be of.
type SlotWord struct {
	Slot   int    // from 0
	ID     string // the model's id, as the wake clips name it
	Front  string // the front end it is scored on: "tflm", "zserge" or "oww"
	Phrase string // what is said, for the screen and the setup page; the id when the model has none
}

// Name is the word as the screen and the setup page show it.
func (w SlotWord) Name() string { return cmp.Or(w.Phrase, w.ID) }

// SetWakeWords is feature/detect giving the recorder the wake words loaded, slot by slot, which a run's
// takes can be of, and where the takes go: its clip store, which sends them on.
func (f *Feature) SetWakeWords(words func() []SlotWord, keep func(Capture)) {
	f.wakeWords.Store(&words)
	f.keepTake.Store(&keep)
	f.WakeWordChanged()
}

// WakeWordChanged is feature/detect saying a wake word was loaded, cleared or dropped: the copy
// RunBlocked and a run's start read is taken again. Asked here, off the screen's tap: the engine holds
// its lock across a model load.
func (f *Feature) WakeWordChanged() {
	var w []SlotWord
	if p := f.wakeWords.Load(); p != nil {
		for _, s := range (*p)() {
			if s.ID != "" {
				w = append(w, s)
			}
		}
	}
	f.wordsNow.Store(&w)
	f.Changed.Emit(struct{}{})
}

// RunWords are the wake words a run can be of now, in slot order: a copy, which nothing waits on.
func (f *Feature) RunWords() []SlotWord {
	if p := f.wordsNow.Load(); p != nil {
		return *p
	}
	return nil
}

// RunWord is the word the next run is of: the one chosen (SetRunWord) while it is still loaded, else
// the first loaded, as before there was a choice. False with none loaded.
func (f *Feature) RunWord() (SlotWord, bool) {
	words := f.RunWords()
	if len(words) == 0 {
		return SlotWord{}, false
	}
	f.mu.Lock()
	pick := f.pickSlot
	f.mu.Unlock()
	for _, w := range words {
		if pick != 0 && w.Slot == pick-1 {
			return w, true
		}
	}
	return words[0], true
}

// SetRunWord chooses the word in slot for the next run. Like the count and the gap it is memory only:
// not a setting, and a restart is the first loaded word again. A slot with no word is left alone, and
// false.
func (f *Feature) SetRunWord(slot int) bool {
	for _, w := range f.RunWords() {
		if w.Slot == slot {
			f.mu.Lock()
			f.pickSlot = slot + 1
			f.mu.Unlock()
			f.Changed.Emit(struct{}{})
			return true
		}
	}
	return false
}

// StepRunWord is the screen's stepper: the next loaded word (step 1) or the one before (-1), round.
func (f *Feature) StepRunWord(step int) {
	words := f.RunWords()
	if len(words) < 2 || step == 0 {
		return
	}
	now, _ := f.RunWord()
	i := 0
	for j, w := range words {
		if w.Slot == now.Slot {
			i = j
		}
	}
	n := len(words)
	f.SetRunWord(words[((i+step)%n+n)%n].Slot)
}

// The clock is set once it reads this year or later; before, a take would be stamped 1970 (the same
// rule as the moment the sender counts from, feature/detect clipsendset.go).
const clockSetYear = 2025

// RunBlocked is why a run cannot be started on the device's own screen, RunReady when it can. Start
// there is the press at the device and the consent, so the Record takes of a word switch is not asked;
// a run asked for on the setup page is (PageRunBlocked). The screen asks this each frame of its
// settings, so it reads only copies: nothing in it waits on the wake word engine.
func (f *Feature) RunBlocked() RunBlock {
	switch {
	case !layout.HasMicrophone():
		return RunNoMicrophone
	case f.dev.sendTo == nil || f.dev.sendTo() == "":
		return RunNotWakesmith
	case f.dev.now().Year() < clockSetYear:
		return RunClockNotSet
	}
	if len(f.RunWords()) == 0 || f.keepTake.Load() == nil {
		return RunNoWakeWord
	}
	f.mu.Lock()
	kept := f.phase == Kept
	f.mu.Unlock()
	if kept {
		return RunTakesKept
	}
	return RunReady
}

// PageRunBlocked is why a run cannot be asked for on the setup page: what RunBlocked says, and the
// Record takes of a word switch before it, since a browser is asking.
func (f *Feature) PageRunBlocked() RunBlock {
	if layout.HasMicrophone() && !f.dev.switchedOn() {
		return RunSwitchOff
	}
	return f.RunBlocked()
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

// LastRefusal is the last start from the screen that could not be made, and why. Kept apart from
// LastRun, so that a Start refused while a run goes on says nothing about that run.
func (f *Feature) LastRefusal() RunResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refused
}

// plan is a run of count takes gap seconds apart, if one can be asked for: on the screen, or with
// page on the setup page. With op held.
func (f *Feature) plan(count, gap int, page bool) (*run, error) {
	b := f.RunBlocked()
	if page {
		b = f.PageRunBlocked()
	}
	if b != RunReady {
		return nil, errors.New(b.Text())
	}
	// The copy, not the engine: a tap on Start must not wait on a model loading.
	w, ok := f.RunWord()
	if !ok {
		return nil, errors.New(RunNoWakeWord.Text())
	}
	word, front := w.ID, w.Front
	count, gap = ClampRun(count, gap)
	f.mu.Lock()
	f.lastCount, f.lastGap = count, gap
	f.mu.Unlock()
	return &run{id: newRunID(), count: count, gap: time.Duration(gap) * time.Second, word: word, front: front, take: 1,
		sendTo: f.dev.sendTo(), switchWas: f.dev.switchedOn()}, nil
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

// errWordGone is a word chosen on the setup page that is no longer loaded when the run is asked for.
const errWordGone = "that wake word is not loaded any more; reload the page and choose again"

// ArmRun asks for a run from the setup page: it waits a minute for the press on the device, as Arm does.
// slot is the word chosen on the page (SetRunWord), or below 0 for the one chosen already.
func (f *Feature) ArmRun(count, gap, slot int, session string) error {
	if session == "" {
		return errors.New("no browser asked for it")
	}
	f.op.Lock()
	defer f.op.Unlock()
	// The word the page showed may have gone since: a run of another word is not what was asked for.
	if slot >= 0 && !f.SetRunWord(slot) {
		return errors.New(errWordGone)
	}
	r, err := f.plan(count, gap, true)
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
		r, err := f.plan(count, gap, false)
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
		f.refused = RunResult{Refused: true, Why: err.Error(), At: f.dev.now()}
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
	t := time.NewTicker(check)
	// Let go of the microphones however this ends: a panic must not leave them the recorder's, with no
	// turn ever opening again. Done by hand on the way out below, and here only if that was never reached.
	let := false
	letGo := func() {
		if let {
			return
		}
		let = true
		t.Stop()
		stop()
		f.release(id)
	}
	defer func() {
		if let {
			return
		}
		letGo()
		f.mu.Lock()
		if f.id == id {
			f.phase, f.why, f.cancel, f.run = Idle, "the run failed", nil, nil
		}
		f.mu.Unlock()
		f.Changed.Emit(struct{}{})
	}()
	f.note(startNotes...)

	preN, listenN, echoN, gapN := framesIn(runPreRoll), framesIn(runListen), framesIn(runEcho), framesIn(r.gap)
	back := make([]heard, 0, preN)
	var capture []heard // from a second before the beep; nil between captures
	beepAt := 0
	// The first beep once the start note has been heard out.
	wait := max(gapN, framesIn(ownHeard))
	take, kept, misses := 1, 0, 0

	last := f.dev.now()
	why := ""
	rang := false // a ring ended it: no stop note over the alarm
	look := func() bool {
		if w, ringing := f.over(); w != "" {
			why, rang = w, ringing
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
			// From the beep on, the device's own sound is the beep and its echo, and anything else playing:
			// an announcement, a reminder, a chime or the music must never be kept as a take. Not the echo
			// canceller's word for it, which stays on for about a second after the beep's note and threw
			// away the words said promptly after it (seen on a Show 8: 58 frames counted as its own sound,
			// not 12).
			h.over = len(capture)-beepAt < echoN || (f.dev.playing != nil && f.dev.playing())
			capture = append(capture, h)
			if len(capture)-beepAt == echoN {
				set(func(r *run) { r.listening = true })
			}
			if len(capture)-beepAt < listenN {
				continue
			}
			pcm, found := pickTake(capture, beepAt)
			if !found {
				missLog(capture, beepAt)
			}
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
	letGo()
	if !rang {
		f.note(stopNotes...)
	}

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
// heard after the beep. One the cutter left out only for being near the beep is taken too, from where
// the beep's sound ends: a word said as the beep fades loses only the start that overlapped it, and is
// kept when what is left is still a word (shortest). A take before the beep is somebody talking over
// the gap, and is not the one asked for.
func pickTake(c []heard, beepAt int) ([]int16, bool) {
	n := len(c)
	level, over := make([]float32, n), make([]bool, n)
	for i, h := range c {
		level[i], over[i] = h.level, h.over
	}
	found, left := cut(level, over)
	padF := framesIn(pad)

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
	shortestF := framesIn(shortest)
	for _, l := range left {
		if l.why != whySpeaker || l.to <= beepAt {
			continue
		}
		// The word as far as it is clear of the device's own sound: it starts after the last such frame
		// inside it, and ends at the first after that.
		word := span{max(l.from, beepAt), l.to}
		for i := word.to - 1; i >= word.from; i-- {
			if over[i] {
				word.from = i + 1
				break
			}
		}
		for i := word.from; i < word.to; i++ {
			if over[i] {
				word.to = i
				break
			}
		}
		if word.to-word.from < shortestF {
			continue
		}
		l.span = word
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

// missLog says what a capture with no take in it held, for tuning the capture on real devices: how
// much of it counted as the device's own sound, the room and the loudest after the beep, and why each
// stretch the cutter found was left out.
func missLog(c []heard, beepAt int) {
	level, over := make([]float32, len(c)), make([]bool, len(c))
	overN := 0
	for i, h := range c {
		level[i], over[i] = h.level, h.over
		if h.over {
			overN++
		}
	}
	var before, after float32
	for i, v := range level {
		if i < beepAt {
			before = max(before, v)
		} else {
			after = max(after, v)
		}
	}
	found, left := cut(level, over)
	whys := make([]string, 0, len(left))
	for _, l := range left {
		whys = append(whys, fmt.Sprintf("%d-%d %s", l.from-beepAt, l.to-beepAt, l.why))
	}
	slog.Info("takes: the capture", "frames", len(c), "beep_at", beepAt, "own_sound", overN,
		"loudest_before", before, "loudest_after", after, "found", len(found), "left", whys)
}
