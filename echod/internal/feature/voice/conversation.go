package voice

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/activity"
	"github.com/HuskerMinion/techo5/echod/internal/feature/feedback"
	"github.com/HuskerMinion/techo5/echod/internal/feature/light"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/recording"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/endpoint"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/lib/triggers"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// errDuplicate is what Home Assistant reports to the devices that lost a race to answer: "Duplicate
// wake-up detected for Glados". Every satellite in earshot hears the wake word and starts a turn, and
// only the first is served.
const errDuplicate = "duplicate_wake_up_detected"

// yieldFlash is how long the ring says the turn went elsewhere. Long enough to be seen by somebody
// looking at the wrong device, short enough not to compete with the one that is answering.
const yieldFlash = 900 * time.Millisecond

// phase is what the conversation is doing. It is the whole of its state: everything that used to be
// inferred from a handful of booleans is a phase, and every transition happens in one goroutine, so
// there is no combination to get into that the transitions do not describe.
type phase int

const (
	// phaseIdle is nothing happening.
	phaseIdle phase = iota

	// phaseListening is microphone audio going up to Home Assistant.
	phaseListening

	// phaseThinking is having stopped listening and waiting on an answer.
	phaseThinking

	// phaseReplying is speaking. The reply outlives the turn as far as Home Assistant is concerned:
	// it ends the run as soon as it has handed over the text, which is before the audio exists.
	phaseReplying
)

func (p phase) String() string {
	switch p {
	case phaseListening:
		return "listening"
	case phaseThinking:
		return "thinking"
	case phaseReplying:
		return "replying"
	}
	return "idle"
}

type eventKind int

const (
	evStart       eventKind = iota // a wake word fired, or something asked for a turn
	evHeard                        // Home Assistant has heard enough
	evReplyText                    // a reply is coming
	evReplyURL                     // the reply can be fetched whole
	evStreamStart                  // reply audio is about to arrive over the API
	evStreamAudio                  // a chunk of it
	evStreamEnd                    // all of it has been sent
	evPlayed                       // the reply has finished playing
	evRunEnd                       // Home Assistant closed the run
	evError                        // the pipeline failed
	evCancel                       // the user gave up on it
	evTimeout                      // Home Assistant never closed the run
	evPending                      // a turn held back for the last one to close has waited long enough
	evPlaying                      // the reply (event.reply) has audio, so the pipeline owes nothing more
	evContinue                     // Home Assistant wants the answer to a question it just asked
	evSpeaking                     // VAD detected speech has started
	evSpokeEnd                     // the device heard the speaker finish; text is the turn's id
	evLookHere                     // the answer put something on the screen: no listening again after it
	evCutOff                       // another sound took the speaker from the reply (event.reply)
	evEffect                       // an effect asked for the step the turn is in (c.effect, askEffect)
	evWakeStart                    // Home Assistant's wake word engine is listening (hawake.go)
	evWakeEnd                      // and has heard the word
	evUnconfirmed                  // a wake asked about was not heard in time
	evCandidate                    // a wake the device was not sure of (event.slot, event.peak)
	evWatch                        // something a stream to Home Assistant depends on may have changed
	evRunStart                     // Home Assistant opened a run
	evAnnouncing                   // an announcement that asks for an answer is being said (event.text)
	evAnnounced                    // and was cut off, so no answer follows
)

type event struct {
	kind  eventKind
	slot  int
	text  string
	url   string
	audio []byte
	code  string
	msg   string
	at    time.Time

	// reply is which reply an evPlaying or evCutOff is about (reply.id). One from a reply that was
	// stopped, or from anything that is not a reply, must not take the turn's deadline away or end
	// the turn after it.
	reply uint64

	// woke is an evStart from the wake word (Woke), not a button or Home Assistant (Start).
	woke bool

	// asked is an evStart Home Assistant asked for, by an announcement that wants an answer.
	asked bool

	// waited is how long after the wake word an evStart comes, deciding which device answers
	// (WokeAt): what was said meanwhile is in the history, and the turn sends that much more of it.
	waited time.Duration

	// peak is an evCandidate's score.
	peak float64
}

// conversation runs one turn at a time: microphone audio up, pipeline events back, spoken reply out.
//
// One goroutine owns the phase and everything derived from it. Everything else — the API read loop,
// the audio streamer, the reply fetch, the buttons — posts events, so no state is read while it is
// being written and an event that does not fit the phase is dropped by the transition rather than by
// a guard flag.
type conversation struct {
	vs *esphome.VoiceSatellite

	// ha and direct are the two backends a turn can run against, and be the one the open turn is
	// running against (backend.go). be is chosen as a turn opens and is only the loop's to change.
	ha     ha
	direct *direct
	// realtime answers a slot chosen for realtime voice (realtime.go).
	realtime *rtBackend
	be       backend

	source  *mic.Source
	speaker *speaker.Player

	// sound is who may make one. Everything audible goes through it, so canceling is one call
	// wherever the sound came from.
	sound  *speaker.Driver
	player *media.Player
	ring   *light.Light
	leds   *led.Driver
	log    *activity.Log

	events chan event

	// out is everything bound for Home Assistant, so nothing local waits on the socket.
	out chan func() error

	// conn is Home Assistant's connection, for the one request the satellite has no call for: a run
	// that starts at the wake word stage (hawake.go).
	conn atomic.Pointer[esphome.Conn]

	// hears is Home Assistant listening for the first slot's wake word, so a candidate is not asked about.
	hears atomic.Bool

	// effect is the latest effect asked for (askEffect), and effectPosted whether an evEffect is on
	// its way to draw it: an automation asking over and over has one event in the queue at a time,
	// so it cannot crowd out the pipeline's own.
	effect       atomic.Pointer[string]
	effectPosted atomic.Bool

	// now is the clock, for tests.
	now func() time.Time

	// busy stands in for the microphones being cut or somebody else's, for tests (micBusy).
	busy func() bool

	// visible is the phase, published for anything outside the loop that needs to ask. Only the loop
	// writes it, and a reader tolerates being a moment out of date: the button uses it to choose
	// between starting and canceling, and posting either is safe whichever it picks.
	visible atomic.Int32

	// Below here belongs to the run goroutine alone.
	phase phase

	// slot is the wake word slot that owns the turn, which decides the tone, the animation and the
	// pipeline. A wake from another slot takes it over rather than being ignored, so the feedback
	// follows the word that actually just fired.
	slot int

	claim     *led.Claim
	stopAudio func()
	deadline  *time.Timer

	// holding is whether the turn has taken the speaker from a track, so that taking it twice or
	// giving it back twice cannot happen however the turn ends.
	holding bool

	// followUp says this turn was opened without a wake word, so hearing nothing is a normal ending
	// rather than Home Assistant having gone away. followUps is how many follow-ups in a row there have
	// been since the wake word (wakeword.FollowUps).
	followUp  bool
	followUps int

	// woke is the open turn's nextTurn.woke, asked its nextTurn.asked, and opened when it was asked
	// for (nextTurn.at).
	woke   bool
	asked  bool
	opened time.Time

	// lookHere says this turn's answer put a page on the screen (the rain map, the forecast), so it is
	// not followed by listening again: the listening screen would cover the page asked for.
	lookHere bool

	// turn measures the one that is open and reports it when it closes. Nil while idle, and every
	// method on it tolerates that, so the phases do not each have to check.
	turn *activity.Turn

	// pending is a turn owed once the current one has finished closing, nil when none.
	//
	// Home Assistant's events carry no run identifier, so a turn started while the last one is still
	// ending cannot tell that one's events from its own — and the first thing to arrive is the old
	// run's RUN_END, which would end the new turn instead. Waiting for the old run to close makes
	// that event the go-ahead rather than a stray.
	pending *nextTurn
	grace   *time.Timer

	// draining is a turn ended as a mistake whose run Home Assistant has yet to close: until it does,
	// or grace runs out, a new turn waits in pending.
	draining bool

	reply reply

	// replies counts the replies spoken, to give each its id.
	replies uint64

	// shown is what a screen is told: the phase and the words so far. Reset when a turn opens, so a
	// new turn never shows the last one's answer.
	shown State

	// watch is a run open at Home Assistant's wake word stage, nil when there is none (hawake.go).
	// While one is, the conversation is idle.
	watch *watching

	// runOpen is a run Home Assistant has yet to close, and stoppedAt when it was last told to stop.
	// A run at the wake word stage waits for the last one to close: its RUN_END would end it.
	runOpen   bool
	stoppedAt time.Time

	// fallback is the device listening for itself because Home Assistant's wake word engine failed,
	// failures how many times in a row, and retryAt when it is tried again.
	fallback bool
	failures int
	retryAt  time.Time

	// proven is Home Assistant's engine having said it is listening (WAKE_WORD_START) since the last
	// failure or connection: until it has, the device goes on listening for itself as well.
	proven bool

	// staleRun is a run at the wake word stage that was let go of before its RUN_END came. Its last
	// events can still arrive, after a turn has opened, and would end that turn: until the next run
	// starts, a RUN_END or an error is the old run's.
	staleRun bool

	// nextConfirm is the soonest another wake may be asked about, and confirmGap the wait after this
	// one: it grows with every wake Home Assistant did not hear, so a steady sound near the word is
	// not asked about every couple of seconds, and starts again from a wake that was real.
	nextConfirm time.Time
	confirmGap  time.Duration

	// busyAt is the last time the microphones were seen to be somebody else's, while confirming is on.
	// What is asked about includes the two seconds before the wake, which must not be a call's.
	busyAt time.Time
}

// graceStart is how long to wait for a stopped run to close before starting the next one anyway.
const graceStart = 750 * time.Millisecond

// drainFor is the same for a run stopped as a mistake, which Home Assistant goes on with: long enough
// for a quick answer to arrive and be dropped, short enough that a wake held behind it still feels
// like an answer to the word.
const drainFor = 3 * time.Second

// nextTurn is a turn to open: which slot, and whether it follows a reply rather than a wake word.
type nextTurn struct {
	slot     int
	followUp bool

	// woke is a turn the wake word opened, rather than a button, Home Assistant or a follow-up: the
	// only kind that can have been opened by mistake.
	woke bool

	// asked is a turn Home Assistant opened, after an announcement that wants an answer.
	asked bool

	// at is when it was asked for, which a turn held back in pending opens later than.
	at time.Time

	// waited is the event's waited: how much more history the turn sends.
	waited time.Duration

	// afterWatch is a turn that waited for a run at the wake word stage to close. Home Assistant's engine
	// may have heard the same word in it just before, and refuses that phrase again for a moment as a
	// second device hearing it: so the first slot's turn reports no phrase, which reaches the same
	// first pipeline (hawake.go).
	afterWatch bool

	// continued is a turn on a run that is already open: Home Assistant's wake word engine heard the
	// word, and the run went on to listening by itself (hawake.go).
	continued bool
}

// reply is which answer is being spoken, and how. Only one of them is set: a url means it is being
// fetched whole, a stream means it is arriving over the API. Both play under one claim on the speaker.
type reply struct {
	url    string
	stream *stream

	// id tells this reply's evPlaying from a stopped one's. Zero is no reply.
	id uint64
}

func newConversation(vs *esphome.VoiceSatellite) *conversation {
	c := &conversation{
		vs:      vs,
		source:  mic.Get(),
		speaker: speaker.Get(),
		sound:   speaker.Sound(),
		player:  media.Get(),
		ring:    light.Get(),
		leds:    led.Get(),
		log:     activity.Get(),
		events:  make(chan event, 128),
		out:     make(chan func() error, outDepth),
		now:     time.Now,
	}
	c.ha = ha{vs: vs, watch: c.startWatch}
	c.direct = newDirect(c.post)
	c.realtime = newRealtime(c.postSure)
	c.be = c.ha

	vs.OnPipelineEvent = c.pipeline
	vs.OnTTSAudio = c.tts
	vs.OnAnnounce = c.announce
	vs.OnStartRequestAccepted = func(port uint32, failed bool) {
		if failed {
			slog.Error("home assistant refused the turn")
			c.post(event{kind: evError, code: "refused"})
			return
		}
		if port != 0 {
			// The legacy UDP path is not implemented; audio goes over the API.
			slog.Warn("home assistant asked for udp audio", "port", port)
		}
	}
	return c
}

// outDepth is how far behind Home Assistant may fall before audio is dropped, in frames of 20ms.
const outDepth = 64

// send queues a message rather than writing it. Ordering is why there is one queue and not a
// goroutine each: Start, the audio, End and Stop only mean anything in that order.
func (c *conversation) send(what string, fn func() error) {
	select {
	case c.out <- fn:
	default:
		slog.Warn("dropped a message to home assistant", "what", what)
	}
}

// sendAudio queues a frame or drops it. Audio held back until a stalled link recovers is audio
// nobody wants by then, and the frame is copied because the caller reuses it.
func (c *conversation) sendAudio(be backend, b []byte) {
	frame := append([]byte(nil), b...)
	select {
	case c.out <- func() error { return be.Audio(frame) }:
	default:
	}
}

// sends drains the queue. A failure ends the turn through the same path a pipeline error takes, and
// whatever is still queued belonged to that turn, so it goes with it.
func (c *conversation) sends(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case fn := <-c.out:
			if err := fn(); err != nil {
				slog.Error("sending to home assistant failed", "err", err)
				for len(c.out) > 0 {
					<-c.out
				}
				c.post(event{kind: evError, code: "send", msg: err.Error()})
			}
		}
	}
}

// post hands an event to the run loop. It never blocks: an event dropped because the queue is full
// is better than stalling the connection's read loop.
func (c *conversation) post(e event) {
	select {
	case c.events <- e:
	default:
		slog.Warn("conversation event dropped", "kind", e.kind, "phase", c.Phase())
	}
}

// postSure is post for what must arrive (an answer's speech and its end, from realtime voice): it waits a
// while for room rather than dropping it. Never from the conversation's own goroutine, which empties the
// queue.
func (c *conversation) postSure(e event) {
	select {
	case c.events <- e:
	case <-time.After(postWait):
		slog.Warn("conversation event dropped after waiting", "kind", e.kind, "phase", c.Phase())
	}
}

// postWait is how long postSure waits for room.
const postWait = 2 * time.Second

// Phase is what it is doing, for anything outside that needs to know.
func (c *conversation) Phase() phase { return phase(c.visible.Load()) }

// Run owns the conversation until ctx is canceled.
func (c *conversation) Run(ctx context.Context) {
	c.claim = c.leds.Claim(led.PriorityTurn)
	defer c.claim.Release()

	safe.Go("voice sender", func() { c.sends(ctx) })

	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	for {
		var expired, waited <-chan time.Time
		if c.deadline != nil {
			expired = c.deadline.C
		}
		if c.grace != nil {
			waited = c.grace.C
		}

		select {
		case <-ctx.Done():
			c.handle(event{kind: evCancel})
			return
		case <-expired:
			c.handle(event{kind: evTimeout})
		case <-waited:
			c.handle(event{kind: evPending})
		case <-c.watchTimeout():
			c.handle(event{kind: evUnconfirmed})
		case <-tick.C:
			c.handle(event{kind: evWatch})
		case e := <-c.events:
			c.handle(e)
		}
	}
}

// handle is the transition table. Anything an event cannot do from the current phase is dropped
// here, which is what makes the whole thing idempotent. A run at Home Assistant's wake word stage
// takes its own events first, and whether one should be open is considered after every event.
func (c *conversation) handle(e event) {
	if !c.watchEvent(e) {
		c.turnEvent(e)
	}
	c.consider()
}

func (c *conversation) turnEvent(e event) {
	switch e.kind {
	case evStart:
		c.start(nextTurn{slot: e.slot, woke: e.woke, asked: e.asked, at: e.at, waited: e.waited})

	case evSpeaking:
		if c.followUp && c.phase == phaseListening {
			c.arm(wakeword.MaxListen(c.slot))
		}

	case evSpokeEnd:
		// The device heard the speaker finish before the pipeline did, which with a television or
		// other voices in the room it may never do. Ending the audio is what the pipeline's own end
		// does, and it goes on to transcribe and answer what it has. The id keeps a late one from an
		// earlier turn from ending this one.
		if c.phase == phaseListening && e.text == c.turn.ID() {
			slog.Info("end of speech heard here, ending the turn", "slot", c.slot+1)
			c.think()
		}

	case evLookHere:
		if c.phase != phaseIdle {
			c.lookHere = true
		}

	case evAnnouncing:
		// A question from Home Assistant (start_conversation) is on the screen as a reply would be,
		// while it is said; the turn that answers it opens after it. Not over a turn of its own.
		if c.phase == phaseIdle {
			c.shown = State{Phase: phaseReplying.String(), Reply: e.text}
			Changed.Emit(c.shown)
		}

	case evAnnounced:
		if c.phase == phaseIdle && c.shown.Phase == phaseReplying.String() {
			c.shown.Phase = phaseIdle.String()
			Changed.Emit(c.shown)
		}

	case evEffect:
		c.effectPosted.Store(false)
		if name := c.effect.Load(); name != nil {
			c.showEffect(*name)
		}

	case evContinue:
		// The pipeline asked a question. Its answer is owed after the reply has been spoken, so this
		// only records the intent; evPlayed opens it.
		// Never for realtime voice, whose next turn would not know the question (rtBackend.finish): a
		// Home Assistant run's late question is not its.
		if _, rt := c.be.(*rtBackend); c.phase != phaseIdle && !rt {
			slog.Info("pipeline asked for an answer", "slot", c.slot+1)
			c.pending = &nextTurn{slot: c.slot, followUp: true}
		}

	case evHeard:
		if pageAsked(e.text) {
			c.lookHere = true
		}
		if e.text != "" {
			slog.Info("heard", "slot", c.slot+1, "text", e.text)
			c.log.Heard(e.text)
			c.shown.Heard = e.text
			Changed.Emit(c.shown)
		}
		// Home Assistant has no alarm of this device's to stop or snooze, so "stop the alarm" and
		// "snooze for ten minutes" are acted on here, whatever it goes on to answer.
		if ring.IsSounding() {
			if stopsRing(e.text) {
				slog.Info("heard a stop over a ring, ending it", "text", e.text)
				ring.End()
			} else if minutes, ok := ring.SnoozeAsked(e.text); ok {
				slog.Info("heard a snooze over a ring", "text", e.text, "minutes", minutes)
				ring.SnoozeFor(minutes)
			}
		}
		c.turn.Heard(e.text)

		// A wake word followed by only what speech-to-text writes for noise was a wake by mistake. It
		// ends here, before the answer to "Thank you." is spoken to an empty room. A follow-up is left
		// alone: "yeah" and "okay" are answers there.
		//
		// Home Assistant goes on with the run it was told to stop, and its last events are still to
		// come: a wake in the meantime waits for them (draining), or they would land in its turn.
		// Realtime voice is answered by the model that heard the audio, whatever its transcript was
		// taken for, and the transcript can come after the answer has begun: only nothing heard at all
		// is a mistake there.
		_, rt := c.be.(*rtBackend)
		if c.woke && c.phase != phaseIdle && onlyNoise(e.text) && (!rt || e.text == "") {
			slog.Info("heard only what noise is written as; the wake was a mistake", "slot", c.slot+1)
			slot, opened := c.slot, c.opened
			c.clearPending()
			c.idle("woken by mistake", activity.NoSpeech)
			// The built-in brain and realtime voice stop at once and send nothing more.
			if !c.stopsAtOnce() {
				c.draining = true
				c.grace = time.NewTimer(drainFor)
			}
			Mistaken.Emit(Mistake{Slot: slot, Heard: e.text, Opened: opened})
			return
		}
		// "Call Mom", with Mom in the phone book: the device places the call itself (callvoice.go).
		if who, words, ready, ok := c.heardCall(e.text, rt); ok {
			c.callFromTurn(who, words, ready)
			return
		}
		// Realtime voice's listening is ended by this device hearing the speaker finish (evSpokeEnd),
		// never by a transcript, which can be of part of the question.
		if c.phase == phaseListening && !rt {
			c.think()
		}

	case evReplyText:
		// The reply to a turn that has ended, one taken as a mistake say: its run is being stopped, and
		// marking the player as sounding now would hold the music down with nothing to give it back.
		if c.phase == phaseIdle {
			return
		}
		slog.Info("replying", "slot", c.slot+1, "text", e.text)
		c.log.Replied(e.text)
		c.turn.Replying(e.text)
		c.shown.Reply = e.text
		Changed.Emit(c.shown)
		if c.phase == phaseListening {
			c.think()
		}
		c.player.Sounding(true)

	case evStreamStart:
		// The turn has to be claimed before the audio arrives: Home Assistant closes the run as soon
		// as it has handed over the text, and a turn still only thinking would take that as the end
		// and go idle, discarding everything that followed.
		if c.phase != phaseIdle && wakeword.Delivery(c.slot) == config.DeliveryStream {
			c.speak("")
		}

	case evReplyURL:
		// Home Assistant serves the reply whole as well as streaming it over the API, and this
		// arrives before the first streamed byte. Fetching cannot gap: the streamed copy arrives at
		// about the rate it plays out, so any hiccup empties the queue and silence lands in the
		// middle of a word. HTTP also says when the audio has ended, which the stream does not.
		//
		// Which of the two runs is the slot's setting, so ignoring the url here leaves the streamed
		// copy to arrive on its own, exactly as it would have if Home Assistant had sent no url.
		if c.phase == phaseIdle {
			return
		}
		if wakeword.Delivery(c.slot) == config.DeliveryStream {
			return
		}
		c.speak(e.url)

	case evStreamAudio:
		// Retired as soon as a url arrives.
		if c.phase == phaseIdle || c.reply.url != "" {
			return
		}
		if c.phase != phaseReplying {
			c.speak("")
		}
		c.reply.stream.send(e.audio)

	case evStreamEnd:
		// Everything Home Assistant is going to send has been sent. What the errand is still holding
		// back for the cushion plays, and it finishes once that has been heard.
		if c.phase == phaseReplying && c.reply.stream != nil {
			c.reply.stream.done()
		}

	case evPlayed:
		c.reported(e.at)
		if c.phase == phaseReplying {
			slot := c.slot
			// Realtime voice answers each turn in a session of its own, which would not know what was
			// said before: no listening again after it.
			_, rt := c.be.(*rtBackend)
			c.idle("spoken", activity.Answered)

			// Continual conversation: the slot keeps listening after every reply, not only the ones
			// Home Assistant asked to continue - as many times in a row as the slot allows.
			limit := wakeword.FollowUps(slot)
			if !rt && c.pending == nil && !c.lookHere && wakeword.FollowUp(slot) > 0 && (limit == 0 || c.followUps < limit) {
				slog.Info("listening again after the reply", "slot", slot+1, "for", wakeword.FollowUp(slot))
				c.pending = &nextTurn{slot: slot, followUp: true}
			}
			c.startPending()
		}

	case evRunEnd:
		// While idle this is the stopped run closing, which is what a held-back turn is waiting for.
		if c.phase == phaseIdle {
			c.startPending()
			return
		}
		// A reply is on its way or playing and owns the ring and the player until it has been heard.
		if c.phase != phaseReplying {
			c.idle("ended", activity.Answered)
		}

	case evPending:
		c.startPending()

	case evPlaying:
		// Only the reply being spoken. An announcement used to post this too, and one made while a
		// turn was thinking took its deadline away, so a pipeline that never answered left the turn
		// thinking for good. And a reply stopped just after its audio was queued can have left its own
		// waiting in the loop, to be handled in the next turn.
		if e.reply == 0 || e.reply != c.reply.id {
			return
		}
		c.disarm()

	case evCutOff:
		// An announcement, or an alarm, took the speaker from the reply. Nothing else would end the
		// turn: its deadline went when the reply's audio arrived, and only a reply that played out
		// posts evPlayed, so it stayed replying until somebody canceled it. It ends here, and nothing
		// follows it: listening again would open over whatever cut it off.
		if c.phase != phaseReplying || e.reply != c.reply.id {
			return
		}
		slog.Info("the reply was cut off by another sound", "slot", c.slot+1)
		c.clearPending()
		c.idle("cut off", activity.Canceled)

	case evError:
		// Two devices in earshot both hear the wake word and both start a turn. Home Assistant keeps the
		// first and refuses the rest, so this arrives on every device that lost — which is not a failure
		// of any of them, and a chime and a red ring say the opposite to the room. It ends the turn and
		// says nothing: the device that won is about to light up and answer.
		if e.code == errDuplicate {
			slog.Info("another device answered first", "slot", c.slot+1, "message", e.msg)
			c.clearPending()
			c.idle("answered elsewhere", activity.Canceled)
			c.leds.Busy().Flash(led.WorkElsewhere, yieldFlash)
			return
		}

		// Nothing to write down: nobody spoke after the wake word, or not to this device. The chime and
		// the red ring are for something failing, and nothing has: the turn ends quietly.
		if e.code == errNoText {
			slog.Info("nothing was said", "slot", c.slot+1, "follow-up", c.followUp)
			slot, opened, mistaken := c.slot, c.opened, c.woke && c.phase != phaseIdle
			c.clearPending()
			c.idle("nothing said", activity.NoSpeech)
			if mistaken {
				Mistaken.Emit(Mistake{Slot: slot, Opened: opened})
			}
			return
		}

		slog.Error("pipeline error", "slot", c.slot+1, "code", e.code, "message", e.msg)
		c.clearPending()
		c.turn.Fails(e.code)
		c.idle("failed", activity.Failed)
		c.trouble()

	case evCancel:
		c.clearPending()

		// Stopping the sound comes first and does not care whether a turn is open: an announcement
		// plays with nothing else happening, and used to be the one thing cancel could not reach.
		sounding := c.sound.Busy()
		c.sound.Silence()

		// The track is left alone. Canceling means the turn is over, and a turn ending is what puts
		// music back up to where it was — whoever wanted the music itself gone said so somewhere else.
		if c.phase == phaseIdle && !sounding {
			return
		}
		slog.Info("canceled", "phase", c.phase, "slot", c.slot+1, "sounding", sounding)
		c.idle("canceled", activity.Canceled)
		feedback.Canceled()

	case evTimeout:
		c.clearPending()

		// Nobody spoke into a turn nobody asked for, which is how a follow-up is meant to end. Every
		// other timeout is something failing: listening means Home Assistant stopped answering,
		// thinking means its pipeline is slower than the slot allows for.
		if c.followUp && c.phase == phaseListening {
			slog.Info("nothing followed", "slot", c.slot+1)
			c.idle("nothing said", activity.NoSpeech)
			return
		}

		// Realtime voice is answered by what it was sent, and nobody else ends its listening: at the
		// limit, what was heard is the question, as when the device hears the speaker stop.
		if _, rt := c.be.(*rtBackend); rt && c.phase == phaseListening {
			slog.Info("listened to the limit; asking realtime voice to answer what it heard", "slot", c.slot+1)
			c.think()
			return
		}

		switch c.phase {
		case phaseListening:
			slog.Warn("gave up listening", "slot", c.slot+1, "after", wakeword.MaxListen(c.slot))
		case phaseThinking:
			slog.Warn("gave up waiting for an answer", "slot", c.slot+1, "after", wakeword.MaxThink(c.slot))
		}
		c.turn.TimesOut()
		c.idle("timed out", activity.Failed)
		c.trouble()
	}
}

// muted reports whether the microphones are cut. A device that could not take the pin has none.
func (c *conversation) muted() (bool, error) { return mute.Get().Muted() }

// start opens a turn on the pipeline paired with a slot, and reports whether it did.
func (c *conversation) start(n nextTurn) bool {
	slot := n.slot

	// A wake the device's own engine was sure of: the word is being said the way it hears it, so what
	// is asked about next is not held back by the ones Home Assistant did not hear before.
	if n.woke && !n.continued {
		c.confirmGap, c.nextConfirm = 0, time.Time{}
	}

	// A cut microphone hears nothing, so a turn started from a button would stream silence until it
	// gave up — and worse, would look like the device is listening while it is muted.
	if muted, err := c.muted(); err == nil && muted {
		slog.Info("microphone muted, not starting a turn", "slot", slot+1)
		c.trouble()
		return false
	}

	// Saying the wake word while the device is still listening is part of the sentence, not a new
	// request: acting on it would cut off what the user is in the middle of saying. Once it is
	// thinking or speaking, a wake word is a deliberate interruption and takes the turn over.
	if c.phase == phaseListening {
		slog.Info("wake word ignored, still listening", "slot", slot+1)
		return false
	}

	// A run is open at Home Assistant's wake word stage: it is stopped, and the turn waits for it to
	// close, as one that interrupts a turn does.
	if c.phase == phaseIdle && c.watch != nil {
		if !c.watch.closing {
			c.closeWatch()
		}
		slog.Info("waiting for the wake word run to close", "slot", slot+1)
		n.afterWatch = true
		c.pending = &n
		if c.grace == nil {
			c.grace = time.NewTimer(graceStart)
		}
		return false
	}

	// A turn ended as a mistake is still being closed by Home Assistant (draining): this one waits for
	// that, as one that interrupts a turn does.
	if c.phase == phaseIdle && c.draining {
		slog.Info("waiting for the mistaken turn's run to close", "slot", slot+1)
		c.pending = &n
		return false
	}

	if c.phase != phaseIdle {
		slog.Info("conversation interrupted", "was", c.phase, "from", c.slot+1, "slot", slot+1)
		c.sound.Silence()

		// Held back before the turn ends, so that ending it does not hand the speaker back to a track
		// for the moment it takes the next turn to open.
		c.pending = &nextTurn{slot: slot, woke: n.woke, asked: n.asked, at: n.at}
		c.idle("interrupted", activity.Canceled)

		// The stopped run has yet to close, and its last events are still on their way.
		c.grace = time.NewTimer(graceStart)
		return false
	}
	c.clearPending()

	// The microphones are going out of a camera (feature/talkback): no turn opens on them, however
	// it was asked for - a wake word, the button, Home Assistant's announcement, or a follow-up.
	if micTaken() {
		slog.Info("turn not started: the microphones are talking through a camera", "follow_up", n.followUp)
		return false
	}

	// A run Home Assistant's engine heard the word in is its own, whatever the brain.
	c.be = c.backendFor(slot)
	if n.continued {
		c.be = c.ha
	}
	if !c.be.Ready() {
		slog.Warn("no voice pipeline ready, ignoring wake", "slot", slot+1, "backend", c.be.Name())
		wakeword.Chime(slot, n.followUp)
		c.trouble()
		return false
	}

	// Which pipeline runs is resolved from the phrase, so a slot with no wake word in it cannot be
	// reached: an empty phrase means the first pipeline. That is right for slot 0, where no wake word
	// is a legitimate way to open a turn, and wrong for any other, where it would quietly run the
	// wrong assistant instead of saying it could not run the one asked for.
	phrase, ok := c.phraseFor(slot)
	if slot > 0 && !ok {
		slog.Warn("no wake word in that slot, nothing to reach its pipeline with", "slot", slot+1)
		c.trouble()
		return false
	}

	c.slot = slot
	c.followUp = n.followUp
	c.woke = n.woke
	c.asked = n.asked
	c.opened = n.at
	if c.opened.IsZero() {
		c.opened = time.Now()
	}
	c.lookHere = false
	// Counted from the wake word: a turn it opens starts again, a follow-up is one more in a row.
	if n.followUp {
		c.followUps++
	} else {
		c.followUps = 0
	}

	// Before the chime, and before Home Assistant is told anything. Ducking is what the room hears
	// first, and it has a second of queued music to get through, so every step it waits behind is a
	// step of full-volume music over somebody who has already started talking.
	//
	// It also keeps the chime out of the duck: the chime is mixed into the queue after this, so it
	// sounds at its own level rather than being faded along with the track underneath it.
	c.hold(true)

	// A follow-up chimes like the wake word that started the conversation, unless the slot has been
	// told a follow-up should be silent: the microphone is open again with nothing said to say so. It
	// is not a wake, though, so it does not report a phrase nobody spoke.
	wakeword.Chime(slot, n.followUp)
	if !n.followUp {
		c.log.Woke(phrase)
	}
	c.turn = c.log.Begin(slot+1, phrase, n.trigger())
	recording.Get().Opens(c.turn.ID(), slot)
	be := c.be
	if !n.continued {
		asked := phrase
		if n.afterWatch && slot == 0 {
			asked = ""
		}
		if rt, ok := be.(*rtBackend); ok {
			// Stopped at once, not through the queue: a start still queued when the turn ends is refused.
			gen := rt.generation()
			c.send("start", func() error { return rt.startFrom(gen) })
		} else {
			c.send("start", func() error { return be.Start(asked) })
		}
	}
	if !c.stopsAtOnce() {
		c.runOpen = true
	}

	c.shown = State{}
	c.enter(phaseListening)
	c.turn.Listening()
	c.reply = reply{}

	if effect := wakeword.Effect(slot); effect != "" {
		c.claim.Play(effect, c.ring.Base())
	}

	c.arm(c.listenFor(n))
	c.startAudio(slot, n.followUp, n.continued, n.waited)
	// The phrase is logged for a follow-up too, because it is what chose the pipeline — not because
	// anyone said it.
	slog.Info("turn started", "slot", slot+1, "phrase", phrase, "follow_up", n.followUp, "on_ha", n.continued)
	return true
}

// trigger is what opened the turn, for the event it ends with. A run Home Assistant's engine heard the
// word in is a wake word as much as one the device heard.
func (n nextTurn) trigger() activity.Trigger {
	switch {
	case n.followUp:
		return activity.FollowUp
	case n.woke:
		return activity.WakeWord
	case n.asked:
		return activity.HomeAssistant
	}
	return activity.Button
}

// listenFor is how long a turn may listen. A follow-up gets its own, shorter, window when the slot
// sets one: it was not asked for, so silence is a likely answer and holding the microphone open
// through the full limit is what the wake word is there to avoid.
func (c *conversation) listenFor(n nextTurn) time.Duration {
	if n.followUp {
		if d := wakeword.FollowUp(n.slot); d > 0 {
			return d
		}
	}
	return wakeword.MaxListen(n.slot)
}

// deviceEnds is whether the device hearing the speaker stop ends a turn on be: always where nobody else
// would (the direct pipeline, and realtime voice, which is told when the speaker has finished), follow-ups
// too, or a follow-up would run to its limit and be dropped as nothing said; for Home Assistant, only
// after a wake word and with End of speech on the device on.
func deviceEnds(be backend, followUp bool) bool {
	switch be.(type) {
	case *direct, *rtBackend:
		return true
	}
	return !followUp && !config.Get().Microphone.PipelineEnds
}

// think stops sending audio. The same animation turned around says the device has stopped listening
// and is waiting on an answer, which is a different thing to be doing and looks like one.
func (c *conversation) think() {
	c.stopStreaming()
	c.enter(phaseThinking)
	c.arm(wakeword.MaxThink(c.slot))

	c.showPhase(wakeword.ThinkingEffect(c.slot))
}

// speak moves to playing the reply. url is empty when it is arriving over the API instead.
//
// Either way it becomes one claim on the speaker, so silencing it stops the whole errand: a fetch
// still downloading is abandoned rather than arriving to play into a canceled turn, and a stream
// still receiving stops taking chunks.
func (c *conversation) speak(url string) {
	c.stopStreaming()
	c.enter(phaseReplying)
	c.replies++
	id := c.replies
	c.reply = reply{url: url, id: id}
	playing := func() { c.post(event{kind: evPlaying, reply: id}) }

	c.showPhase(wakeword.ReplyingEffect(c.slot))
	c.player.Sounding(true)

	// The deadline is left as it was. Text arriving is not the pipeline delivering: it still owes the
	// audio, and the limit it was given when the device stopped listening goes on running until some
	// of that audio turns up.
	errand := func(ctx context.Context, p *speaker.Player) error {
		return c.play(ctx, url, playing, phone.RepliesIntoCall())
	}
	if url == "" {
		s := newStream(c.speaker.Splices(), c.speaker.Underruns(), wakeword.Buffer(c.slot))
		c.reply.stream = s
		errand = func(ctx context.Context, p *speaker.Player) error {
			return s.play(ctx, p, playing)
		}
	}

	// A call up meanwhile sends the reply, not the room, until it and its echo are over (phone's uplink).
	replied := phone.Get().ReplyStarted()
	held := c.sound.ClaimSpeech("reply", errand)
	// The slot is read here, on the loop: by the time the reply is over, the loop may be on another
	// turn, and the slot is not the reply goroutine's to read.
	slot := c.slot
	safe.Go("reply", func() {
		<-held.Done()
		time.AfterFunc(speaker.HardwareTail, replied)

		// A reply that was stopped - canceled, interrupted by a wake word, or cut off by another sound -
		// fails its fetch with the context canceled, which says nothing about the url. Taken as a
		// failure, it moved the wake word to streamed replies for good. Canceling and a wake word end
		// the turn themselves; another sound taking the speaker does not, so the loop is told.
		if held.Stopped() {
			if held.Preempted() {
				c.post(event{kind: evCutOff, reply: id})
			}
			return
		}
		if err := held.Err(); err != nil {
			slog.Error("playing the reply failed", "url", url, "err", err)
			// A url that cannot be fetched would be silence every turn; the streamed copy arrives
			// over the connection the device already has.
			if url != "" {
				wakeword.Get().FallBackToStream(slot)
			}
		}
		c.post(event{kind: evPlayed, at: held.Quiet()})
	})
}

// idle puts everything back. why is only for the log.
// idle closes whatever is open. The ending is passed rather than read back out of why, so a new way
// for a turn to end cannot quietly report itself as one of the old ones.
func (c *conversation) idle(why string, how activity.Ending) {
	was := c.phase
	c.stopStreaming()
	c.disarm()

	if was != phaseIdle {
		if rt, ok := c.be.(*rtBackend); ok {
			// Stopped here and now, never dropped with a full queue: a session left open goes on, and is
			// billed, until its limit.
			_ = rt.Stop()
		} else {
			c.send("stop", c.be.Stop)
		}
		c.stoppedAt = c.now()
	}

	kept := recording.Get()
	kept.Closes()
	c.turn.Records(kept.Seconds(c.turn.ID()))
	c.turn.Ends(how)
	c.turn = nil

	c.enter(phaseIdle)
	c.claim.Clear()
	c.player.Sounding(false)
	c.reply = reply{}
	c.hold(c.pending != nil)

	if was != phaseIdle {
		slog.Info("turn ended", "was", was, "slot", c.slot+1, "why", why)
	}
}

// askEffect has the loop show name for the step the turn is in (Voice.ShowEffect). Only the latest
// name asked for is kept, and only one event is posted until the loop has taken it.
func (c *conversation) askEffect(name string) {
	c.effect.Store(&name)
	if c.effectPosted.Swap(true) {
		return
	}
	select {
	case c.events <- event{kind: evEffect}:
	default:
		c.effectPosted.Store(false)
		slog.Warn("conversation event dropped", "kind", evEffect, "phase", c.Phase())
	}
}

// showEffect puts name on the ring for the step the turn is in (Voice.ShowEffect): Default, or no
// name, draws the step as entering it did, and None clears it. A turn that has ended shows nothing.
func (c *conversation) showEffect(name string) {
	switch {
	case c.phase == phaseIdle:
	case name == "", name == component.EffectDefault:
		c.restyle()
	case name == component.EffectNone:
		c.claim.Clear()
	default:
		c.claim.Play(name, c.ring.Base())
	}
}

// restyle draws the turn's phase again, as entering it did.
func (c *conversation) restyle() {
	switch c.phase {
	case phaseListening:
		if effect := wakeword.Effect(c.slot); effect != "" {
			c.claim.Play(effect, c.ring.Base())
		} else {
			c.claim.Clear()
		}
	case phaseThinking:
		c.showPhase(wakeword.ThinkingEffect(c.slot))
	case phaseReplying:
		c.showPhase(wakeword.ReplyingEffect(c.slot))
	}
}

// showPhase runs a phase's override, or the wake word's own animation reversed when there is none.
// None is an answer of its own: the ring stays out of that phase. A wake word with no animation
// leaves the ring clear, which also takes off an effect an automation put up for the step before
// (showEffect).
func (c *conversation) showPhase(override string) {
	switch override {
	case "":
		if effect := wakeword.Effect(c.slot); effect != "" {
			c.claim.PlayReversed(effect, c.ring.Base())
		} else {
			c.claim.Clear()
		}
	case component.EffectNone:
		c.claim.Clear()
	default:
		c.claim.Play(override, c.ring.Base())
	}
}

func (c *conversation) enter(p phase) {
	c.phase = p
	c.visible.Store(int32(p))
	c.shown.Phase = p.String()
	Changed.Emit(c.shown)
}

// startPending opens the turn that was held back, if there is one.
func (c *conversation) startPending() {
	next := c.pending
	c.clearPending()

	if next != nil {
		c.start(*next)
	}
}

func (c *conversation) clearPending() {
	c.pending = nil
	c.draining = false
	if c.grace != nil {
		c.grace.Stop()
		c.grace = nil
	}

	// A turn that is not going to open cannot be what a track is waiting for.
	if c.phase == phaseIdle {
		c.hold(false)
	}
}

// hold takes the speaker away from a track for as long as a turn needs it, and is what gives it
// back. A turn is several sounds with listening in between, so it holds it across all of them
// rather than one claim at a time.
func (c *conversation) hold(on bool) {
	if on == c.holding {
		return
	}
	c.holding = on

	// Ducked music keeps playing under the whole turn, so the echo canceller has a live reference while
	// somebody is talking — which is the one thing an adaptive filter must not learn from. Stop it
	// learning for the duration; it goes on canceling with what it already knows.
	mic.Get().SetAdapting(!on)

	// Every background, not just the track: a room playing along with the rest of the house is also
	// something a reply has to be heard over, and it is not the media player's to quieten.
	speaker.Sound().Backgrounds().Duck("turn", on)
}

// arm gives the current phase a limit; disarm removes it. Each phase sets its own, so a slow model
// does not eat the time the microphone was meant to have and a live microphone is not bounded by how
// long an answer may take.
func (c *conversation) arm(d time.Duration) {
	c.disarm()
	c.deadline = time.NewTimer(d)
}

func (c *conversation) disarm() {
	if c.deadline != nil {
		c.deadline.Stop()
		c.deadline = nil
	}
}

// trouble says a request could not be served. It takes its own claim at a priority above the turn,
// so ending the turn that failed cannot take the indication away with it.
func (c *conversation) trouble() {
	feedback.Failure()
}

// phraseFor is what Home Assistant expects a turn to report for one of its wake word slots: the
// spoken phrase, not the model's id. Which pipeline runs is resolved by comparing that phrase against
// each slot's select, so a turn from slot n has to report slot n's own phrase.
func (c *conversation) phraseFor(slot int) (string, bool) {
	if slot < 0 || slot >= len(c.vs.ActiveWakeWords) {
		return "", false
	}

	// From what is on disk, because a slot is only active once its model loaded, and the advertised list
	// is not kept anywhere to be read.
	id := c.vs.ActiveWakeWords[slot]
	if m, ok := wake.Find(wake.Lib().Ours(), id); ok {
		return m.Phrase, true
	}
	return id, true
}

// pipeline turns what Home Assistant reports into events. It runs on the connection's read loop, so
// it does nothing but translate.
func (c *conversation) pipeline(e esphome.PipelineEvent) {
	switch e.Type {
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_STT_END:
		c.post(event{kind: evHeard, text: e.Data["text"]})
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_STT_VAD_START:
		c.post(event{kind: evSpeaking})
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_STT_VAD_END:
		c.post(event{kind: evHeard})
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_INTENT_PROGRESS:
		if e.Data["tts_start_streaming"] == "1" {
			slog.Info("early tts streaming offered", "slot", c.slot+1)
		}

	case api.VoiceAssistantEvent_VOICE_ASSISTANT_INTENT_END:
		if e.Data["continue_conversation"] == "1" {
			c.post(event{kind: evContinue})
		}
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_TTS_START:
		c.post(event{kind: evReplyText, text: e.Data["text"]})
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_TTS_END:
		if url := e.Data["url"]; url != "" {
			c.post(event{kind: evReplyURL, url: url})
		}

	// Home Assistant brackets the streamed audio with these, and calls the reply finished after the
	// end. They are the reliable signals: the per-message end flag is not.
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_TTS_STREAM_START:
		c.post(event{kind: evStreamStart})
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_TTS_STREAM_END:
		c.post(event{kind: evStreamEnd})
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_RUN_START:
		c.post(event{kind: evRunStart})
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_RUN_END:
		c.post(event{kind: evRunEnd})
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_WAKE_WORD_START:
		c.post(event{kind: evWakeStart})
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_WAKE_WORD_END:
		c.post(event{kind: evWakeEnd})
	case api.VoiceAssistantEvent_VOICE_ASSISTANT_ERROR:
		c.post(event{kind: evError, code: e.Data["code"], msg: e.Data["message"]})
	}
}

// tts is reply audio streamed over the API, as 16 kHz mono.
func (c *conversation) tts(data []byte, end bool) {
	if len(data) > 0 {
		c.post(event{kind: evStreamAudio, audio: data})
	}
	// The per-message end flag is not depended on: TTS_STREAM_END is what Home Assistant sends when it
	// has finished, and it arrives whether or not this was ever set.
	_ = end
}

// LookHere says the answer being given put something on the screen, so the turn is not followed by
// listening again, which would cover it.
func (c *conversation) LookHere() { c.post(event{kind: evLookHere}) }

// Start asks for a turn on a slot's pipeline. Wake detection and the buttons both use it.
func (c *conversation) Start(slot int) { c.post(event{kind: evStart, slot: slot, at: time.Now()}) }

// Woke opens a turn for the wake word in slot.
func (c *conversation) Woke(slot int) {
	c.post(event{kind: evStart, slot: slot, woke: true, at: time.Now()})
}

// WokeAt is Woke for a wake word heard at at and acted on now.
func (c *conversation) WokeAt(slot int, at time.Time) {
	c.post(event{kind: evStart, slot: slot, woke: true, at: at, waited: max(time.Since(at), 0)})
}

// Cancel gives up on whatever is happening.
func (c *conversation) Cancel() { c.post(event{kind: evCancel}) }

// Busy reports whether there is anything to cancel.
func (c *conversation) Busy() bool { return c.Phase() != phaseIdle }

// audioSettings asks Home Assistant to condition the microphone audio before recognition. The
// array's analog gain already matches what the vendor's own recognizer ran with, and speech still
// reaches the pipeline around -45 dBFS, which is some 20 dB below what a recognizer wants: the vendor
// made that up in its DSP, and this is the equivalent handled by the far end.
//
// Auto gain is a ceiling on how much the far end may apply, not a fixed boost, so it costs nothing
// when the talker is close and loud.
func audioSettings() *api.VoiceAssistantAudioSettings {
	return &api.VoiceAssistantAudioSettings{
		NoiseSuppressionLevel: 2,
		AutoGain:              31,
		VolumeMultiplier:      1,
	}
}

// startAudio begins sending microphone frames. The streamer only reads, so it needs no coordination
// beyond being told to stop. continued is a run that has had the audio up to now already.
func (c *conversation) startAudio(slot int, followUp, continued bool, waited time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	c.stopAudio = cancel

	// slot is captured rather than read from the loop's state, which the streamer does not own. So is
	// whether this is a follow-up: between them they say what the turn sounds like and so what has to
	// be kept out of the microphone.
	id := c.turn.ID()
	be := c.be
	// A call up meanwhile holds the far end back from the room for as long as this listens (phone's
	// playFarEnd), so the caller does not speak into the turn.
	listened := phone.Get().TurnListening()
	streams.Add(1)
	safe.Go("turn audio", func() {
		defer streams.Done()
		defer listened()
		c.stream(ctx, be, slot, followUp, continued, id, waited)
	})
}

// streams counts the turns' audio goroutines still running, so a test can wait for the last to stop
// before it changes the settings they read.
var streams sync.WaitGroup

func (c *conversation) stopStreaming() {
	if c.stopAudio == nil {
		return
	}
	c.stopAudio()
	c.stopAudio = nil

	c.send("end of audio", c.be.End)
}

// stream sends microphone frames until it is told to stop.
func (c *conversation) stream(ctx context.Context, be backend, slot int, followUp, continued bool, id string, waited time.Duration) {
	frames, pre, unlisten := c.source.ListenHistory("turn", mic.History+waited)
	defer unlisten()

	buf := make([]byte, 0, mic.FrameSamples*2)

	// Send what was already said before this turn began. The wake word can only be recognized after
	// it has been spoken, and people run straight on into the request, so the opening words exist only
	// in the microphone's history. History and subscription share one capture boundary.
	//
	// A slot that chimes does not send it: whoever is talking waits for the tone, so the history holds
	// the wake word rather than the request, and the tone itself is louder at the array than they are.
	// That is as true of a wake Home Assistant's engine heard.
	if wakeword.Tones(slot, followUp) {
		pre = nil
	}

	// End of speech as the device hears it, which ends the turn when the pipeline has not already.
	// It hears what is sent - the history too - since that is what it was measured on. See package
	// endpoint for why the pipeline's detector alone is not enough.
	//
	// Only after a wake word, though. The detector measures the speaker against the loudest thing it
	// has heard, and a wake word means someone close has just spoken; a follow-up opens on nothing,
	// where the loudest thing may be the television, and ending on that would send it the
	// television's words to act on. There it is logged only, and the follow-up's own limit ends it.
	// The same when the device has been told to leave it to Home Assistant.
	//
	// The direct pipeline and realtime voice have nobody else to decide (deviceEnds).
	acts := deviceEnds(be, followUp)
	ep := endpoint.New(endpoint.Default)
	var sent int
	var endpointAt float64
	see := func(frame []int16) {
		sent += len(frame)
		if endpointAt == 0 && ep.Feed(frame) {
			endpointAt = float64(sent) / float64(mic.Rate)
			slog.Info("end of speech heard here", "slot", slot+1, "acted", acts,
				"spoke_s", math.Round(float64(ep.EndedAt())/float64(mic.Rate)*10)/10,
				"at_s", math.Round(endpointAt*10)/10)
			if acts {
				c.post(event{kind: evSpokeEnd, text: id})
			}
		}
	}
	see(pre)

	// A run Home Assistant's engine heard the word in has had all of it, and is already listening to
	// what follows: without a tone the history is only the end-of-speech detector's, which measures the
	// speaker by the word.
	if continued {
		pre = nil
	}

	if len(pre) > 0 {
		for _, s := range pre {
			buf = append(buf, byte(s), byte(s>>8))
		}
		recording.Get().Frame(buf)

		c.sendAudio(be, buf)
		slog.Debug("sent audio history", "ms", len(pre)*1000/mic.Rate)
	}

	// What the microphone actually sends is the input to speech recognition, and a quiet or clipped
	// stream explains a bad transcript better than anything downstream does.
	// The tone plays as the turn opens and is louder at the array than a talker across the room, so
	// nothing is sent while it is sounding. What the speaker still has queued says when that is, and
	// hardwareTail is what the driver holds after the queue runs out.
	var sounding time.Time
	if wakeword.Tones(slot, followUp) {
		sounding = time.Now().Add(wakeword.ChimeLength(slot, followUp) + speaker.HardwareTail)
	}
	var held int

	var peak, samples int
	var energy float64
	defer func() {
		if samples == 0 {
			return
		}
		rms := math.Sqrt(energy / float64(samples))
		slog.Info("sent audio",
			"slot", slot+1,
			"seconds", float64(samples)/float64(mic.Rate),
			"endpoint_s", math.Round(endpointAt*10)/10,
			"peak", peak,
			"peakdbfs", math.Round(20*math.Log10(max(float64(peak), 1)/32768)*10)/10,
			"rms", math.Round(rms),
			"rmsdbfs", math.Round(20*math.Log10(math.Max(rms, 1)/32768)*10)/10,
			"gaindb", math.Round(c.source.Gain()*10)/10,
			"clipped", c.source.Clipped())
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-frames:
			if !ok {
				return
			}

			if !sounding.IsZero() {
				if time.Now().Before(sounding) {
					held += len(frame)
					continue
				}
				sounding = time.Time{}
				slog.Debug("held the tone back", "ms", held*1000/mic.Rate)
			}

			buf = buf[:0]
			for _, s := range frame {
				buf = append(buf, byte(s), byte(s>>8))
				peak = max(peak, int(s), -int(s))
				energy += float64(s) * float64(s)
				samples++
			}
			recording.Get().Frame(buf)

			c.sendAudio(be, buf)
			see(frame)
		}
	}
}

// reported logs how a streamed reply went, once the errand that played it has finished and its
// counters are nobody's to write. gap is silence the device had nothing to fill with.
func (c *conversation) reported(dry time.Time) {
	s := c.reply.stream
	if s == nil || s.bytes == 0 || s.playingAt.IsZero() {
		return
	}

	seconds := float64(s.bytes) / float64(2*mic.Rate)
	took := dry.Sub(s.playingAt).Seconds()

	slog.Info("reply played",
		"via", wakeword.Delivery(c.slot),
		"bytes", s.bytes,
		"seconds", math.Round(seconds*100)/100,
		"took", math.Round(took*100)/100,
		"gap", math.Round(max(took-seconds, 0)*100)/100,
		"peak", s.peak,
		"seams", c.speaker.Splices()-s.splicesAt,
		"underruns", c.speaker.Underruns()-s.underrunsAt,
		"clipped", c.speaker.Clipped(),
		"dropped", c.source.Dropped())
}

// activeWakeWords is the per-slot selection the user last made, filtered to what the device can
// actually load. Home Assistant takes it as authoritative, so claiming a model that is not here would
// leave a slot looking armed and deaf.
func activeWakeWords(models []wake.Model, slots int) []string {
	saved := config.Get().Wake
	var active []string
	for i := range slots {
		if id := saved.Slot(i).ID; id != "" {
			if _, ok := wake.Find(models, id); ok {
				active = append(active, id)
			}
		}
	}

	// Nothing chosen yet: start listening for something rather than nothing, or a fresh device looks
	// broken until the user finds the select. The shipped default when it is installed, and otherwise
	// whatever this device does have — a device carrying one model somebody copied on should listen for
	// that one rather than for nothing. "No wake word" chosen on purpose stays chosen.
	if len(active) == 0 && !saved.NoneChosen {
		if m, ok := wake.Find(models, wake.DefaultModel); ok {
			active = []string{m.ID}
		} else if len(models) > 0 {
			active = []string{models[0].ID}
		}
	}
	return active
}

// pageAsked is whether what was heard puts something on the screen that the answer should be left on
// rather than covered by listening again: the clock asked back ("go home"), a camera, or - where the
// screen opens the forecast on the words, which it does for Home Assistant's answers - the weather.
func pageAsked(heard string) bool {
	lang := config.Get().Screen.Language
	if triggers.AboutGoingHome(heard, lang) || triggers.AboutCamera(heard, lang) {
		return true
	}
	return !config.Get().Brain.Direct() && (triggers.AboutWeather(heard, lang) || triggers.AboutRadar(heard, lang))
}
