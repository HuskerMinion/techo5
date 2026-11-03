package voice

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
	"github.com/HuskerMinion/techo5/echod/internal/lib/realtime"
)

// watcher is a conversation with Home Assistant subscribed, a clock the test moves, and the runs it
// opens at the wake word stage counted instead of sent. The loop is not running: the test hands it
// events, and flush runs what it queued for Home Assistant.
type watcher struct {
	*conversation
	t       *testing.T
	at      time.Time
	mu      sync.Mutex
	watches []string
	ready   bool
	busyMic bool
}

func newWatcher(t *testing.T, on config.OnHA) *watcher {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	set := config.Set().OnHA()
	if err := set.Stream(on.Stream); err != nil {
		t.Fatal(err)
	}
	if err := set.Confirm(on.Confirm); err != nil {
		t.Fatal(err)
	}

	w := &watcher{t: t, at: time.Now(), ready: true}
	c := newConversation(&esphome.VoiceSatellite{})
	c.claim = c.leds.Claim(led.PriorityTurn)
	c.now = func() time.Time { return w.at }
	c.busy = func() bool { return w.busyMic }
	c.ha.ready = func() bool { return w.ready }
	c.ha.watch = func(phrase string) error {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.watches = append(w.watches, phrase)
		return nil
	}
	w.conversation = c
	t.Cleanup(func() {
		c.handle(event{kind: evCancel})
		c.dropWatch()
		c.claim.Release()
		streams.Wait()
	})
	return w
}

// flush runs what was queued for Home Assistant, in order.
func (w *watcher) flush() {
	for {
		select {
		case fn := <-w.out:
			_ = fn()
		default:
			return
		}
	}
}

func (w *watcher) opened() int {
	w.flush()
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.watches)
}

func (w *watcher) later(d time.Duration) {
	w.at = w.at.Add(d)
	w.handle(event{kind: evWatch})
}

// Streaming opens a run at the wake word stage while the device is idle, and Home Assistant hearing
// the word opens the turn on that same run: no second run, and Home Assistant counts as listening only
// once its engine has said so.
func TestStreamingOpensTheTurnOnTheSameRun(t *testing.T) {
	w := newWatcher(t, config.OnHA{Stream: true})
	w.handle(event{kind: evWatch})
	if w.watch == nil || w.watch.mode != watchStream || w.opened() != 1 {
		t.Fatalf("streaming on and idle: watch %+v, runs %d", w.watch, w.opened())
	}
	if w.hears.Load() {
		t.Error("counted as listening before home assistant said it was")
	}
	w.handle(event{kind: evWakeStart})
	if !w.hears.Load() {
		t.Error("home assistant is listening and is not counted as such")
	}

	w.handle(event{kind: evWakeEnd})
	if w.phase != phaseListening || w.watch != nil || !w.woke {
		t.Fatalf("after home assistant heard the word: phase %s, watch %+v, woke %v", w.phase, w.watch, w.woke)
	}
	if w.hears.Load() {
		t.Error("the device is still standing down during the turn")
	}

	// The turn ends; the next run waits for this one to close, then opens.
	w.handle(event{kind: evCancel})
	if w.watch != nil {
		t.Fatal("a run opened before the turn's had closed")
	}
	w.handle(event{kind: evRunEnd})
	if w.watch == nil || w.opened() != 2 {
		t.Fatalf("after the turn's run closed: watch %+v, runs %d", w.watch, w.opened())
	}
}

// A run that ends the ordinary way is started again; one whose pipeline has no wake word engine leaves
// the device listening for itself and is tried again later, longer each time.
func TestARunIsRestartedOrBackedOffFrom(t *testing.T) {
	w := newWatcher(t, config.OnHA{Stream: true})
	w.handle(event{kind: evWatch})
	w.handle(event{kind: evWakeStart})
	w.at = w.at.Add(time.Minute)

	w.handle(event{kind: evError, code: "wake-word-timeout"})
	w.handle(event{kind: evRunEnd})
	if w.watch == nil || w.opened() != 2 || w.fallback {
		t.Fatalf("after an ordinary end: watch %+v, runs %d, fallback %v", w.watch, w.opened(), w.fallback)
	}

	w.handle(event{kind: evError, code: "wake-engine-missing", msg: "no engine"})
	if !w.fallback || w.hears.Load() {
		t.Fatalf("no engine: fallback %v, listening %v", w.fallback, w.hears.Load())
	}
	w.handle(event{kind: evRunEnd})
	if w.watch != nil {
		t.Fatal("a run was opened again at once after the engine was missing")
	}
	w.later(retryLong - time.Second)
	if w.watch != nil {
		t.Fatal("tried again before the wait was over")
	}
	w.later(2 * time.Second)
	if w.watch == nil || w.opened() != 3 {
		t.Fatalf("after the wait: watch %+v, runs %d", w.watch, w.opened())
	}

	// Missing again: twice as long.
	w.handle(event{kind: evError, code: "wake-engine-missing"})
	w.handle(event{kind: evRunEnd})
	w.later(retryLong + time.Second)
	if w.watch != nil {
		t.Fatal("the second wait was not longer than the first")
	}
	w.later(retryLong)
	if w.watch == nil {
		t.Fatal("not tried again after the second wait")
	}
	w.handle(event{kind: evWakeStart})
	if w.fallback || w.failures != 0 || !w.hears.Load() {
		t.Errorf("listening again: fallback %v, failures %d, listening %v", w.fallback, w.failures, w.hears.Load())
	}
}

// A wake the device was not sure of is asked about; one Home Assistant does not hear in time is dropped
// without a turn, and one it hears opens the turn and is told as confirmed, with the device's score.
func TestACandidateIsConfirmedOrDropped(t *testing.T) {
	confirmed := make(chan Confirmation, 2)
	t.Cleanup(Confirmed.Listen(func(c Confirmation) { confirmed <- c }))

	w := newWatcher(t, config.OnHA{Confirm: true})
	w.handle(event{kind: evWatch})
	if w.watch != nil {
		t.Fatal("a run opened with only confirming on")
	}

	w.handle(event{kind: evCandidate, slot: 0, peak: 0.74})
	if w.watch == nil || w.watch.mode != watchConfirm || w.opened() != 1 {
		t.Fatalf("a candidate: watch %+v, runs %d", w.watch, w.opened())
	}
	if w.hears.Load() {
		t.Error("confirming stood the device down")
	}
	w.handle(event{kind: evUnconfirmed})
	if w.watch == nil || !w.watch.closing || w.phase != phaseIdle {
		t.Fatalf("not heard in time: watch %+v, phase %s", w.watch, w.phase)
	}
	w.handle(event{kind: evRunEnd})
	if w.watch != nil || w.phase != phaseIdle {
		t.Fatalf("after the run closed: watch %+v, phase %s", w.watch, w.phase)
	}
	select {
	case c := <-confirmed:
		t.Fatalf("a wake Home Assistant did not hear was told as confirmed: %+v", c)
	default:
	}

	// One not heard holds the next back for longer than the first wait.
	w.at = w.at.Add(confirmGapMin + time.Second)
	w.handle(event{kind: evCandidate, slot: 0, peak: 0.8})
	if w.watch != nil {
		t.Fatal("asked again before the longer wait after one Home Assistant did not hear")
	}
	w.at = w.at.Add(confirmGapMin)

	w.handle(event{kind: evCandidate, slot: 0, peak: 0.81})
	w.handle(event{kind: evWakeEnd})
	if w.phase != phaseListening || !w.woke {
		t.Fatalf("a confirmed wake: phase %s, woke %v", w.phase, w.woke)
	}
	select {
	case c := <-confirmed:
		if c.Peak != 0.81 || c.Slot != 0 {
			t.Errorf("confirmed %+v, want slot 0 at 0.81", c)
		}
	case <-time.After(time.Second):
		t.Fatal("the confirmed wake was not told")
	}

	// Busy: a candidate during a turn is not asked about.
	w.handle(event{kind: evCandidate, slot: 0, peak: 0.8})
	if w.watch != nil {
		t.Error("a candidate during a turn was asked about")
	}
}

// With confirming off, a candidate is nothing.
func TestACandidateIsIgnoredWithConfirmingOff(t *testing.T) {
	w := newWatcher(t, config.OnHA{})
	w.handle(event{kind: evCandidate, slot: 0, peak: 0.8})
	if w.watch != nil || w.opened() != 0 {
		t.Fatalf("confirming off: watch %+v, runs %d", w.watch, w.opened())
	}
}

// The microphones cut, or taken by a call, close the stream at once and keep it closed; and what Home
// Assistant heard while they were is not a turn.
func TestAMuteOrACallStopsTheStream(t *testing.T) {
	w := newWatcher(t, config.OnHA{Stream: true})
	w.handle(event{kind: evWatch})
	w.handle(event{kind: evWakeStart})

	w.busyMic = true
	w.handle(event{kind: evWatch})
	if w.watch == nil || !w.watch.closing || w.hears.Load() {
		t.Fatalf("muted: watch %+v, listening %v", w.watch, w.hears.Load())
	}
	w.handle(event{kind: evWakeEnd})
	if w.phase != phaseIdle {
		t.Fatalf("a wake heard as the stream was closing opened a turn: %s", w.phase)
	}
	w.handle(event{kind: evRunEnd})
	w.later(time.Minute)
	if w.watch != nil {
		t.Fatal("a run opened while muted")
	}

	w.busyMic = false
	w.later(time.Second)
	if w.watch == nil {
		t.Fatal("no run after the mute ended")
	}
}

// Home Assistant going away hands the wake word back to the device at once; and a turn asked for on the
// device while a run is open waits for that run to close rather than opening into its events.
func TestFallingBackAndATurnFromTheDevice(t *testing.T) {
	w := newWatcher(t, config.OnHA{Stream: true})
	w.handle(event{kind: evWatch})
	w.handle(event{kind: evWakeStart})

	w.ready = false
	w.handle(event{kind: evWatch})
	if w.watch != nil || w.hears.Load() {
		t.Fatalf("home assistant gone: watch %+v, listening %v", w.watch, w.hears.Load())
	}

	w.ready = true
	w.handle(event{kind: evWatch})
	w.handle(event{kind: evWakeStart})
	asked := w.at
	w.handle(event{kind: evStart, slot: 0, woke: true, at: asked})
	if w.phase != phaseIdle || w.pending == nil || w.watch == nil || !w.watch.closing {
		t.Fatalf("a wake on the device during a run: phase %s, pending %+v, watch %+v", w.phase, w.pending, w.watch)
	}
	if w.hears.Load() {
		t.Error("still standing down while the run closes")
	}
	w.handle(event{kind: evRunEnd})
	if w.phase != phaseListening || w.watch != nil || !w.conversation.opened.Equal(asked) {
		t.Fatalf("after the run closed: phase %s, watch %+v, opened %v", w.phase, w.watch, w.conversation.opened)
	}
}

// Only the first slot is asked about, in a run that carries no phrase: Home Assistant would refuse the
// phrase its engine just heard as a second device hearing it. A turn from the device that waited on a
// stream's run reports none either, for the same reason; one that did not keeps its phrase.
func TestRunsAtTheWakeWordStageCarryNoPhrase(t *testing.T) {
	w := newWatcher(t, config.OnHA{Confirm: true})
	w.vs.ActiveWakeWords = []string{"hey_jarvis", "alexa"}
	var started []string
	w.ha.start = func(phrase string) error { started = append(started, phrase); return nil }

	w.handle(event{kind: evCandidate, slot: 1, peak: 0.8})
	if w.watch != nil {
		t.Fatal("the second slot was asked about")
	}
	w.handle(event{kind: evCandidate, slot: 0, peak: 0.8})
	if w.watch == nil || w.opened() != 1 || w.watches[0] != "" {
		t.Fatalf("the first slot: watch %+v, runs %q", w.watch, w.watches)
	}
	w.handle(event{kind: evUnconfirmed})
	w.handle(event{kind: evRunEnd})

	// A wake on the device with nothing open keeps its phrase.
	w.handle(event{kind: evStart, slot: 0, woke: true, at: w.at})
	w.flush()
	w.handle(event{kind: evCancel})
	w.handle(event{kind: evRunEnd})

	// Streaming: the device hears the word too, and its turn waits for the run to close.
	if err := config.Set().OnHA().Stream(true); err != nil {
		t.Fatal(err)
	}
	w.later(time.Minute)
	w.handle(event{kind: evWakeStart})
	w.handle(event{kind: evStart, slot: 0, woke: true, at: w.at})
	w.handle(event{kind: evRunEnd})
	w.flush()
	if w.phase != phaseListening || len(started) != 2 || started[0] == "" || started[1] != "" {
		t.Fatalf("phase %s, turns started with %q; want a phrase, then none after the stream", w.phase, started)
	}
}

// A run let go of before it closed can still send its end after the turn that waited on it has opened:
// that must not end the turn, and an error from it must not sound like a failure.
func TestALateEndFromALetGoRunDoesNotEndTheTurn(t *testing.T) {
	w := newWatcher(t, config.OnHA{Stream: true})
	w.ha.start = func(string) error { return nil }
	w.handle(event{kind: evWatch})
	w.handle(event{kind: evWakeStart})
	w.handle(event{kind: evStart, slot: 0, woke: true, at: w.at})
	w.handle(event{kind: evPending}) // Home Assistant took longer than graceStart to close it
	if w.phase != phaseListening {
		t.Fatalf("the turn did not open after the wait: %s", w.phase)
	}
	w.handle(event{kind: evError, code: "wake_word_detection_aborted"})
	w.handle(event{kind: evError, code: "stt-stream-failed"})
	w.handle(event{kind: evRunEnd})
	if w.phase != phaseListening {
		t.Fatalf("the old run's last events ended the new turn: %s", w.phase)
	}
	// The new run starts: from now on its events are its own.
	w.handle(event{kind: evRunStart})
	w.handle(event{kind: evRunEnd})
	if w.phase == phaseListening {
		t.Fatal("the turn's own run ending was swallowed")
	}
}

// While a let-go run is still to close, the device's own refusal of the new turn's start is the new
// turn's, and ends it: it is not taken for the old run's.
func TestTheNewTurnsOwnRefusalIsNotTheOldRuns(t *testing.T) {
	w := newWatcher(t, config.OnHA{Stream: true})
	w.ha.start = func(string) error { return nil }
	w.handle(event{kind: evWatch})
	w.handle(event{kind: evWakeStart})
	w.handle(event{kind: evStart, slot: 0, woke: true, at: w.at})
	w.handle(event{kind: evPending})
	if w.phase != phaseListening || !w.staleRun {
		t.Fatalf("the turn did not open on a let-go run: %s, stale %v", w.phase, w.staleRun)
	}
	w.handle(event{kind: evError, code: "refused"})
	if w.phase == phaseListening {
		t.Fatal("the new turn's own refusal was swallowed as the old run's")
	}
}

// A run told to stop that ends with an error has not failed; one Home Assistant itself ends at once
// with an ordinary error is not reopened straight away.
func TestAStoppedRunIsNoFailureAndABriefOneWaits(t *testing.T) {
	w := newWatcher(t, config.OnHA{Stream: true})
	w.ha.start = func(string) error { return nil }
	w.handle(event{kind: evWatch})
	w.handle(event{kind: evWakeStart})
	w.handle(event{kind: evStart, slot: 0, woke: true, at: w.at})
	w.handle(event{kind: evError, code: "stt-no-text-recognized"})
	if w.fallback || w.failures != 0 {
		t.Fatalf("a run we stopped counted as failing: fallback %v, failures %d", w.fallback, w.failures)
	}
	w.handle(event{kind: evRunEnd})
	w.handle(event{kind: evCancel})
	w.handle(event{kind: evRunEnd})

	// The turn's run closed and streaming opened again at once; this time Home Assistant ends it.
	if w.watch == nil {
		t.Fatal("streaming did not open again after the turn")
	}
	runs := w.opened()
	w.handle(event{kind: evWakeStart})
	w.handle(event{kind: evError, code: "no_wake_word"})
	w.handle(event{kind: evRunEnd})
	if w.watch != nil || w.opened() != runs {
		t.Fatal("a run Home Assistant ended at once was opened again straight away")
	}
	w.later(briefRun + time.Millisecond)
	if w.watch == nil {
		t.Fatal("not opened again after the short wait")
	}
}

// A streaming run Home Assistant never says it is listening on is given up, and tried again later.
func TestAStreamThatNeverStartsIsGivenUp(t *testing.T) {
	w := newWatcher(t, config.OnHA{Stream: true})
	w.handle(event{kind: evWatch})
	w.later(startFor - time.Second)
	if w.watch == nil || w.watch.closing {
		t.Fatal("given up too soon")
	}
	w.later(2 * time.Second)
	if w.watch == nil || !w.watch.closing || !w.fallback {
		t.Fatalf("after the wait: watch %+v, fallback %v", w.watch, w.fallback)
	}
}

// What is asked about starts two seconds back: not while those were a call's.
func TestNoConfirmJustAfterACall(t *testing.T) {
	w := newWatcher(t, config.OnHA{Confirm: true})
	w.busyMic = true
	w.handle(event{kind: evWatch})
	w.busyMic = false
	w.at = w.at.Add(time.Second)
	w.handle(event{kind: evCandidate, slot: 0, peak: 0.8})
	if w.watch != nil {
		t.Fatal("asked about a wake whose history was the call's")
	}
	w.at = w.at.Add(3 * time.Second)
	w.handle(event{kind: evCandidate, slot: 0, peak: 0.8})
	if w.watch == nil {
		t.Fatal("not asked about once the call was well behind")
	}
}

// A wake word chosen for realtime voice is never listened for or confirmed by Home Assistant: hearing it
// there would be a turn on Home Assistant's pipeline.
func TestARealtimeWordIsNotHomeAssistants(t *testing.T) {
	w := newWatcher(t, config.OnHA{Stream: true, Confirm: true})
	if err := config.Set().Wake(0).Realtime(true); err != nil {
		t.Fatal(err)
	}
	w.handle(event{kind: evWatch})
	w.handle(event{kind: evCandidate, slot: 0, peak: 0.8})
	if w.watch != nil || w.opened() != 0 {
		t.Fatalf("home assistant asked about a realtime word: watch %+v, runs %d", w.watch, w.opened())
	}
}

// With a let-go Home Assistant run still to close, a realtime turn's own end ends it: it is not taken for
// the old run's.
func TestARealtimeTurnsEndIsNotTheOldRuns(t *testing.T) {
	w := newWatcher(t, config.OnHA{})
	_ = config.Set().Wake(0).Realtime(true)
	_ = config.Set().Realtime().Set("openai", "", "", "", 0)
	_ = config.Set().Realtime().SetKey("sk-test")
	w.realtime.dial = func(context.Context, realtime.Config) (rtSession, error) { return nil, errors.New("not here") }
	w.handle(event{kind: evStart, slot: 0, woke: true, at: w.at})
	if w.phase != phaseListening || !w.stopsAtOnce() {
		t.Fatalf("the realtime turn did not open: %s", w.phase)
	}
	w.staleRun = true
	w.handle(event{kind: evRunEnd})
	if w.phase == phaseListening {
		t.Fatal("the realtime turn's end was swallowed as the old run's")
	}
	if !w.staleRun {
		t.Error("the old run is still to close, and is not waited for any more")
	}
}

// Realtime voice's listening is ended by the device whatever End of speech on the device says, a turn
// that reaches its listening limit is answered rather than failed, and a Home Assistant question does
// not open a follow-up on it.
func TestARealtimeTurnAlwaysEndsListening(t *testing.T) {
	w := newWatcher(t, config.OnHA{})
	_ = config.Set().Microphone().DeviceEnds(false)
	_ = config.Set().Wake(0).Realtime(true)
	_ = config.Set().Realtime().Set("openai", "", "", "", 0)
	_ = config.Set().Realtime().SetKey("sk-test")
	w.realtime.dial = func(context.Context, realtime.Config) (rtSession, error) { return nil, errors.New("not here") }
	if !deviceEnds(w.realtime, true) || !deviceEnds(w.direct, true) || deviceEnds(w.ha, false) {
		t.Fatal("who ends listening")
	}
	w.handle(event{kind: evStart, slot: 0, woke: true, at: w.at})
	if w.phase != phaseListening {
		t.Fatalf("the realtime turn did not open: %s", w.phase)
	}
	w.handle(event{kind: evContinue})
	if w.pending != nil {
		t.Error("a follow-up was set for realtime voice")
	}
	w.handle(event{kind: evTimeout})
	if w.phase != phaseThinking {
		t.Fatalf("at the listening limit: %s, want thinking", w.phase)
	}
}

// Wakes Home Assistant hears none of are counted, for the log's hint, and one it hears starts the count
// again.
func TestUnheardWakesAreCounted(t *testing.T) {
	w := newWatcher(t, config.OnHA{Confirm: true})
	w.handle(event{kind: evWatch})
	for range unheardWarn {
		w.at = w.at.Add(confirmGapMax + time.Second)
		w.handle(event{kind: evCandidate, slot: 0, peak: 0.74})
		if w.watch == nil {
			t.Fatal("a candidate was not asked about")
		}
		w.handle(event{kind: evUnconfirmed})
		w.handle(event{kind: evRunEnd})
	}
	if w.unheard != unheardWarn {
		t.Fatalf("unheard %d, want %d", w.unheard, unheardWarn)
	}
	w.at = w.at.Add(confirmGapMax + time.Second)
	w.handle(event{kind: evCandidate, slot: 0, peak: 0.8})
	w.handle(event{kind: evWakeEnd})
	if w.unheard != 0 {
		t.Errorf("a wake Home Assistant heard left unheard at %d", w.unheard)
	}
}
