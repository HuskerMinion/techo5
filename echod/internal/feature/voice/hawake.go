package voice

import (
	"context"
	"log/slog"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Home Assistant's own wake word engine (config.OnHA), two ways:
//
//   - Streaming: while the device is idle its microphones go up to Home Assistant in a run that starts
//     at the wake word stage, and the pipeline's engine listens for the word as well as the device.
//     Whatever engine and model the pipeline has is the one that listens: one the device cannot run,
//     or one trained on the household's own language.
//   - Confirming: a wake of the first slot's word the device's own engine was not sure of, one that
//     scored between the confirm threshold and the wake word threshold, is asked about. The last two
//     seconds go up in a run that starts at the wake word stage, then what follows until Home
//     Assistant answers or confirmFor runs out, and the turn opens only if its engine heard the word.
//     Nothing is sent until then, nothing at all for a wake the device was sure of, and the questions
//     are spaced out (confirmGapMin) so a steady sound near the word is not asked about over and over.
//
// Either way the run is ESPHome's own: the satellite's request with the wake word flag set, which Home
// Assistant answers with WAKE_WORD_START, then WAKE_WORD_END once its engine hears the word, and from
// there the same run goes on to speech-to-text, so the turn opens on a run that is already listening.
//
// The stop word, the timer's ring, announcements and the second slot's word stay on the device: Home
// Assistant listens for one word per pipeline, and not while a turn is running.

// watchMode is why a run is open at the wake word stage.
type watchMode int

const (
	watchStream watchMode = iota
	watchConfirm
)

func (m watchMode) String() string {
	if m == watchConfirm {
		return "confirming"
	}
	return "streaming"
}

// confirmFor is how long Home Assistant has to hear a wake the device was not sure of. Measured on a
// Raspberry Pi 5 running openWakeWord, two seconds of audio sent at once was answered in about half a
// second; a slower server gets three times that before the question is dropped.
const confirmFor = 1500 * time.Millisecond

// A run that fails is tried again after a while, longer each time it fails in a row: a short wait for
// one that went wrong, a long one when Home Assistant's pipeline has no wake word engine to run it with,
// which only changes when somebody sets one up.
const (
	retryShort    = 2 * time.Second
	retryShortMax = time.Minute
	retryLong     = time.Minute
	retryLongMax  = 15 * time.Minute
)

// briefRun is a run so short that starting the next one at once would be a loop.
const briefRun = time.Second

// startFor is how long a streaming run has for Home Assistant to say its engine is listening. One that
// never does (Home Assistant takes the satellite's audio for something else, such as setting up a wake
// word) would otherwise take the room's audio for as long as nothing else closed it.
const startFor = 5 * time.Second

// The wait between asking about one wake and the next: the first, and the most it grows to.
const (
	confirmGapMin = 5 * time.Second
	confirmGapMax = time.Minute
)

// later is the later of two times.
func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// localErrors are the device's own, posted for the run it is opening now: never an earlier run's.
var localErrors = map[string]bool{"refused": true, "send": true}

// staleApplies is whether an error may be the let-go run's: only while one is still to close, and only
// on Home Assistant's pipeline, whose runs say when they start. The direct brain's never do.
func (c *conversation) staleApplies() bool {
	return c.staleRun && c.onHA()
}

// Errors Home Assistant ends a wake word run with that say nothing is wrong: the run is over, and
// streaming starts another.
var ordinaryWakeErrors = map[string]bool{
	"wake-word-timeout":           true,
	"wake_word_detection_aborted": true,
	"no_wake_word":                true,
}

// Errors that say the pipeline has no wake word engine to run.
var missingWakeEngine = map[string]bool{
	"wake-provider-missing": true,
	"wake-engine-missing":   true,
}

// watching is a run open at the wake word stage. It belongs to the conversation's loop.
type watching struct {
	mode watchMode
	slot int

	// peak is what the device's own engine scored the wake that is being confirmed.
	peak float64

	at      time.Time
	stop    func()
	timeout *time.Timer

	// started is Home Assistant having said its engine is listening (WAKE_WORD_START).
	started bool

	// closing is a run told to stop, or ended with an error, whose RUN_END is still to come.
	closing bool

	// byHA is a run Home Assistant ended (with an error) rather than one told to stop.
	byHA bool
}

// Confirmation is a wake the device was not sure of that Home Assistant heard too.
type Confirmation struct {
	Slot int
	Peak float64
}

// Confirmed is told of every wake Home Assistant confirmed, as the turn opens.
var Confirmed hook.Hook[Confirmation]

// startWatch opens a run at the wake word stage. The satellite has no call for it, so it is sent on
// Home Assistant's connection directly; everything after it, the audio, the stop and the events, is
// the satellite's as for any other run.
func (c *conversation) startWatch(phrase string) error {
	conn := c.conn.Load()
	if conn == nil || !c.vs.Subscribed() {
		return esphome.ErrNoSubscriber
	}
	return conn.Send(&api.VoiceAssistantRequest{
		Start:          true,
		Flags:          uint32(api.VoiceAssistantRequestFlag_VOICE_ASSISTANT_REQUEST_USE_WAKE_WORD),
		WakeWordPhrase: phrase,
		AudioSettings:  audioSettings(),
	})
}

// watchEvent handles what belongs to a run at the wake word stage, and reports whether it did. The rest
// goes to the turn.
func (c *conversation) watchEvent(e event) bool {
	switch e.kind {
	case evWatch:
		if config.Get().Wake.OnHA.Confirm && c.micBusy() {
			c.busyAt = c.now()
		}
		return true

	case evRunStart:
		// Whatever comes now is this run's: a run let go of before it closed has nothing more to say.
		c.staleRun = false
		return true

	case evWakeStart:
		// Home Assistant's engine is listening, so the pipeline has one and the connection works.
		if w := c.watch; w != nil && !w.closing {
			w.started = true
			if c.fallback {
				slog.Info("home assistant is listening for the wake word again")
			}
			c.failures, c.fallback, c.proven = 0, false, true
		}
		return true

	case evWakeEnd:
		c.heardOnHA()
		return true

	case evUnconfirmed:
		if w := c.watch; w != nil && !w.closing && w.mode == watchConfirm {
			c.confirmGap = min(2*c.confirmGap, confirmGapMax)
			c.nextConfirm = c.now().Add(c.confirmGap)
			slog.Info("home assistant did not hear the wake word", "slot", w.slot+1, "peak", w.peak,
				"next_ask_after", c.confirmGap)
			c.closeWatch()
		}
		return true

	case evCandidate:
		c.confirm(e.slot, e.peak)
		return true

	case evRunEnd:
		c.runOpen = false
		w := c.watch
		if w == nil {
			// A turn of the device's own (the direct brain, realtime voice) ends with its own: never the
			// let-go run's, which is still to close.
			if c.staleRun && (c.phase == phaseIdle || c.onHA()) {
				// The run let go of has closed. A turn opened since is still waiting on its own.
				c.staleRun = false
				if c.phase != phaseIdle {
					slog.Debug("the wake word run let go of has closed")
					return true
				}
			}
			return false
		}
		c.dropWatch()
		switch {
		case !w.closing && !w.started:
			// Ended before its engine was ever listening, and without saying why.
			c.failed("the run ended before listening", false)
		case (!w.closing || w.byHA) && c.now().Sub(w.at) < briefRun:
			// Ended by Home Assistant almost as soon as it began: the next waits, or a pipeline that ends
			// every run at once would have them opened back to back.
			c.retryAt = later(c.retryAt, c.now().Add(briefRun))
		}
		// What was held back for the run to close: a turn asked for meanwhile.
		c.startPending()
		return true

	case evError:
		w := c.watch
		if w == nil {
			// The end of a run at the wake word stage that was let go of before it closed: nothing failed,
			// and a turn opened since must not end for it. A run a turn opens never reaches the wake word
			// stage, so these codes cannot be its own.
			if c.staleApplies() && !localErrors[e.code] || ordinaryWakeErrors[e.code] {
				slog.Debug("an earlier wake word run's error", "code", e.code, "phase", c.phase)
				return true
			}
			return false
		}
		if w.closing {
			// Told to stop: whatever it ends with is how it took that, not a failure of Home Assistant's.
			slog.Debug("a stopped wake word run ended", "code", e.code)
			return true
		}
		w.byHA = true
		switch {
		case ordinaryWakeErrors[e.code]:
			slog.Debug("wake word run ended", "code", e.code)
		case e.code == errDuplicate:
			// Another device heard the same word first.
			slog.Info("another device answered first", "slot", w.slot+1)
			c.leds.Busy().Flash(led.WorkElsewhere, yieldFlash)
		case missingWakeEngine[e.code]:
			c.failed("home assistant's pipeline has no wake word engine: "+e.msg, true)
		default:
			c.failed("wake word run failed: "+e.code+" "+e.msg, false)
		}
		// Home Assistant ends the run itself after an error; its RUN_END is what closes it here.
		c.endWatch()
		return true

	case evPending:
		// Waited long enough for a run told to stop: it is taken as closed, and what it still sends is
		// stale.
		if w := c.watch; w != nil && w.closing {
			c.dropWatch()
			c.runOpen = false
			c.staleRun = true
		}
		return false
	}
	return false
}

// failed notes a run that went wrong: the device listens for itself until another has worked, and the
// next is tried after a wait that grows with every failure in a row.
func (c *conversation) failed(why string, missing bool) {
	c.failures++
	wait, most := retryShort, retryShortMax
	if missing {
		wait, most = retryLong, retryLongMax
	}
	for range min(c.failures-1, 10) {
		wait *= 2
	}
	wait = min(wait, most)
	c.retryAt = c.now().Add(wait)
	c.proven = false
	if !c.fallback {
		slog.Warn("listening for the wake word on the device instead", "why", why, "retry_in", wait)
	} else {
		slog.Info("home assistant wake word still unavailable", "why", why, "retry_in", wait)
	}
	c.fallback = true
}

// heardOnHA is Home Assistant's engine hearing the word. The run goes on to speech-to-text by itself,
// so the turn opens on it rather than starting another.
func (c *conversation) heardOnHA() {
	w := c.watch
	if w == nil || w.closing {
		return
	}
	c.dropWatch()

	// A call or a talk through a camera began after the run did: whatever was heard was not somebody
	// in the room asking.
	if c.micBusy() {
		slog.Info("home assistant heard the wake word with the microphones cut or busy, ignored")
		c.closeRun()
		return
	}
	slog.Info("home assistant heard the wake word", "slot", w.slot+1, "mode", w.mode, "local_peak", w.peak)
	// As the device's own wake word does (Voice.open): a word said over a ringing alarm or timer
	// silences it first.
	if ring.IsSounding() && ring.Silence() {
		slog.Info("wake word over a ring, silencing it")
	}
	if w.mode == watchConfirm {
		c.confirmGap = 0
	}
	if !c.start(nextTurn{slot: w.slot, woke: true, at: c.now(), continued: true}) {
		c.closeRun()
		return
	}
	if w.mode == watchConfirm {
		Confirmed.Emit(Confirmation{Slot: w.slot, Peak: w.peak})
	}
}

// closeRun stops a run that opened no turn, and holds anything new back until it has closed.
func (c *conversation) closeRun() {
	c.send("stop", c.ha.Stop)
	c.stoppedAt = c.now()
	c.draining = true
	if c.grace == nil {
		c.grace = time.NewTimer(graceStart)
	}
}

// confirm asks Home Assistant about a wake the device was not sure of. Only the first slot's: a run at
// the wake word stage carries no phrase (openWatch), which reaches the first pipeline.
func (c *conversation) confirm(slot int, peak float64) {
	on := config.Get().Wake.OnHA
	switch {
	case !on.Confirm || slot != 0:
		return
	case c.phase != phaseIdle || c.watch != nil || c.pending != nil || c.draining || c.grace != nil:
		slog.Debug("wake candidate not asked about, busy", "slot", slot+1, "phase", c.phase)
		return
	case !c.ha.Ready() || !haAnswers(slot) || c.micBusy():
		return
	case c.now().Before(c.retryAt):
		slog.Debug("wake candidate not asked about, home assistant unavailable", "slot", slot+1)
		return
	case !c.busyAt.IsZero() && c.now().Sub(c.busyAt) < mic.Confirming+time.Second:
		// What would go up starts two seconds back, and the microphones were a call's or a camera's then.
		slog.Debug("wake candidate not asked about, the microphones were busy just now", "slot", slot+1)
		return
	case c.now().Before(c.nextConfirm):
		slog.Debug("wake candidate not asked about, asked too recently", "slot", slot+1)
		return
	}
	if c.confirmGap == 0 {
		c.confirmGap = confirmGapMin
	}
	c.nextConfirm = c.now().Add(c.confirmGap)
	c.openWatch(watchConfirm, slot, peak)
}

// consider opens a streaming run when one should be open and none is, closes one that should not be,
// and says whether Home Assistant is listening for the first slot's word. It runs after every event and once a
// second, so a mute, a call or a change of setting reaches the stream without needing an event of its
// own.
func (c *conversation) consider() {
	defer func() {
		w := c.watch
		c.hears.Store(w != nil && w.mode == watchStream && !w.closing && c.proven)
	}()

	if c.watch == nil {
		// The cheap questions first: this runs on every event, a reply's audio included.
		switch {
		case c.phase != phaseIdle, c.pending != nil, c.draining, c.grace != nil:
			return
		case !config.Get().Wake.OnHA.Stream:
			return
		case c.runOpen && c.now().Sub(c.stoppedAt) < drainFor:
			// The last run has yet to close, and its RUN_END would end this one.
			return
		case c.now().Before(c.retryAt):
			return
		case !c.ha.Ready():
			c.runOpen, c.proven = false, false
			return
		case !c.mayStream():
			return
		}
		c.openWatch(watchStream, 0, 0)
		return
	}

	w := c.watch
	switch {
	case !c.ha.Ready():
		slog.Info("home assistant went away, the wake word is the device's again")
		c.dropWatch()
		c.runOpen, c.proven = false, false
	case w.closing && c.now().Sub(c.stoppedAt) > drainFor:
		// Its RUN_END never came. If it still does, it is stale.
		c.dropWatch()
		c.runOpen = false
		c.staleRun = true
	case w.mode == watchStream && !w.closing && !w.started && c.now().Sub(w.at) > startFor:
		c.failed("home assistant never said it was listening", false)
		c.closeWatch()
	case !w.closing && (c.micBusy() || w.mode == watchStream && !c.mayStream()):
		c.closeWatch()
	}
}

// mayStream is whether the microphones may go to Home Assistant now, all the time the device is idle.
func (c *conversation) mayStream() bool {
	return config.Get().Wake.OnHA.Stream && haAnswers(0) && !c.micBusy()
}

// haAnswers is whether a slot's wake word is answered by Home Assistant's pipeline: not when the direct
// brain answers, nor for a word chosen for realtime voice (backendFor). Home Assistant hearing a word is
// a turn on its pipeline, so it is never asked to listen for one of those.
func haAnswers(slot int) bool {
	if config.Get().Brain.Direct() {
		return false
	}
	w := config.Get().Wake.Words
	return !(slot >= 0 && slot < len(w) && w[slot].Realtime)
}

// micBusy is the microphones being cut or somebody else's: a call, or a talk through a camera. A test
// sets busy in its place.
func (c *conversation) micBusy() bool {
	if c.busy != nil {
		return c.busy()
	}
	if phone.Get().Busy() || micTaken() {
		return true
	}
	muted, err := c.muted()
	return err == nil && muted
}

// openWatch starts a run at the wake word stage and the audio that goes with it: from now on when
// streaming, the last two seconds and then from now on when confirming.
//
// The run carries no wake word phrase, as ESPHome's own satellites send none for one. Home Assistant
// notes the phrase its engine heard and, given the same one again in the run, refuses it as a second
// device hearing the word: no phrase means the first pipeline, and nothing to refuse.
func (c *conversation) openWatch(mode watchMode, slot int, peak float64) {
	watch := c.ha.watch
	c.send("wake word run", func() error { return watch("") })
	c.runOpen = true

	w := &watching{mode: mode, slot: slot, peak: peak, at: c.now()}
	var history time.Duration
	if mode == watchConfirm {
		history = mic.Confirming
		w.timeout = time.NewTimer(confirmFor)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.stop = cancel
	c.watch = w
	frames, pre, unlisten := c.source.ListenHistory("wake word on home assistant", history)
	safe.Go("wake word audio", func() {
		defer unlisten()
		c.watchAudio(ctx, pre, frames)
	})

	if mode == watchConfirm {
		slog.Info("asking home assistant about a wake", "slot", slot+1, "peak", peak)
	} else {
		slog.Debug("listening for the wake word on home assistant")
	}
}

// closeWatch tells Home Assistant to stop listening, and waits for its RUN_END.
func (c *conversation) closeWatch() {
	c.endWatch()
	c.send("stop wake word run", c.ha.Stop)
	c.stoppedAt = c.now()
}

// endWatch stops the audio of a run that is ending, which stays the open run until it has closed.
func (c *conversation) endWatch() {
	w := c.watch
	if w == nil || w.closing {
		return
	}
	w.closing = true
	w.stop()
	if w.timeout != nil {
		w.timeout.Stop()
	}
	if c.stoppedAt.Before(w.at) {
		c.stoppedAt = c.now()
	}
}

// dropWatch forgets the run, whatever state it was in.
func (c *conversation) dropWatch() {
	w := c.watch
	if w == nil {
		return
	}
	w.stop()
	if w.timeout != nil {
		w.timeout.Stop()
	}
	c.watch = nil
}

// watchTimeout is the confirming run's deadline, nil when there is none to wait on.
func (c *conversation) watchTimeout() <-chan time.Time {
	if w := c.watch; w != nil && w.timeout != nil && !w.closing {
		return w.timeout.C
	}
	return nil
}

// confirmChunk is how much of what came before goes in one message: the queue to Home Assistant is
// sized for frames, and two seconds at once would overflow it.
const confirmChunk = mic.Rate / 2

// watchAudio sends the microphones to a run at the wake word stage until told to stop. Nothing is sent
// while a call or a talk through a camera has them.
func (c *conversation) watchAudio(ctx context.Context, pre []int16, frames <-chan []int16) {

	var buf []byte
	send := func(samples []int16) {
		buf = buf[:0]
		for _, s := range samples {
			buf = append(buf, byte(s), byte(s>>8))
		}
		c.sendAudio(c.ha, buf)
	}
	for len(pre) > 0 {
		n := min(len(pre), confirmChunk)
		send(pre[:n])
		pre = pre[n:]
	}
	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-frames:
			if !ok {
				return
			}
			if c.busy == nil && (phone.Get().Busy() || micTaken()) {
				continue
			}
			send(frame)
		}
	}
}

// Candidate is a wake the device's own engine was not sure of, to be asked about when confirming is on.
func (v *Voice) Candidate(slot int, peak float64) {
	if micTaken() {
		return
	}
	v.turn.post(event{kind: evCandidate, slot: slot, peak: peak})
}

// WakeOnHAChanged says a Home Assistant wake word setting changed, so a stream starts or stops now
// rather than at the next look a second later.
func (v *Voice) WakeOnHAChanged() { v.turn.post(event{kind: evWatch}) }

// HomeAssistantHears is whether Home Assistant is listening for a slot's wake word, so a wake the
// device was not sure of is not asked about as well.
func (v *Voice) HomeAssistantHears(slot int) bool { return slot == 0 && v.turn.hears.Load() }
