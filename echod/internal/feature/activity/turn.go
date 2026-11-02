package activity

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// TurnEvent is what Home Assistant's bus calls a finished turn: one per voice request, for an
// automation or the logbook to see what was asked and how it went. Under the esphome domain because
// that is the only one Home Assistant accepts from a device; docs/actions.md has the fields.
const TurnEvent = "esphome.techo5_voice"

// LegacyTurnEvent is the same turn as EchoLocal reported it, which its Home Assistant integration
// reads for its logbook lines and its activity view. It goes out unchanged beside TurnEvent, with
// EchoLocal's fields and values. The docs name it only so it can be left out of the recorder:
// TurnEvent is the one to build on.
const LegacyTurnEvent = "esphome.echolocal_turn"

// Version is bumped when a field changes meaning, so a reader that does not know a version can ignore
// it rather than guess.
const Version = "1"

// Ending is how a turn ended, which is not always visible from what it left behind: a turn that timed
// out and one that was answered can both have a transcript and no reply.
type Ending string

const (
	Answered Ending = "answered"
	NoSpeech Ending = "no_speech" // nothing said, or only what speech-to-text writes for noise
	Canceled Ending = "canceled"  // stopped, interrupted, cut off, or answered by another device (Reason)
	Failed   Ending = "error"     // the pipeline failed or gave up; the event's error says which
)

// Reason is why a turn was canceled, which the event's ended does not say: a device that stood aside
// for a nearer one has done its job, and one that was stopped has not.
type Reason string

const (
	Stopped           Reason = "stopped"            // the button, the stop word or Home Assistant ended it
	Interrupted       Reason = "interrupted"        // a new turn took its place
	CutOff            Reason = "cut_off"            // another sound took the speaker from the reply
	AnsweredElsewhere Reason = "answered_elsewhere" // another device heard the wake word first
)

// Trigger is what opened a turn.
type Trigger string

const (
	WakeWord      Trigger = "wake_word"
	Button        Trigger = "button"
	FollowUp      Trigger = "follow_up"
	HomeAssistant Trigger = "home_assistant" // an announcement that asked for an answer
)

// textLimit is the longest transcript or reply an event carries. Longer than a state, since a reply
// is often a paragraph, and far short of what the wire allows: every event goes into the recorder.
const textLimit = 1000

// scoreWait is how long a finished turn holds its events for the wake word's peak, which is known only
// once the detection's hold ends (detect.Hold): a turn another device answered can be over before then.
const scoreWait = time.Second

// queued is how many events, two to a turn, may wait for a Home Assistant that has stopped reading.
// Turns are seconds apart, so a full queue is a connection that is gone, and the newest is dropped.
const queued = 16

// Turn is one turn as it happens. Each phase stamps when it began and nothing is worked out until the
// turn closes, so the arithmetic lives in one place instead of at every boundary.
type Turn struct {
	mu sync.Mutex

	id   string
	slot int
	word string
	by   Trigger

	heard string
	reply string

	// failure is what went wrong, for a turn that ends in an error, and timedOut whether that was the
	// device giving up rather than Home Assistant reporting an error.
	failure  string
	timedOut bool

	// reason is why a canceled turn was.
	reason Reason

	// score is the peak the wake word reached, for a turn the device's own engine opened. scoreDue says
	// one is on its way, and a turn that ends first holds its events for it (scoreWait).
	score    float64
	scoreDue bool

	// audio is seconds of recording kept for this turn, zero when none was.
	audio float64

	// When each phase began, zero for one the turn never reached, and when it ended.
	listening time.Time
	thinking  time.Time
	replying  time.Time
	ended     time.Time

	// how is how it ended; wait holds its events for the score, and sent says they have gone.
	how  Ending
	wait *time.Timer
	sent bool
}

// Begin starts a turn. The id lives as long as the device holds the turn's audio, which is what asks
// for it later. word is the slot's wake word, which chose the pipeline whatever opened the turn.
func (l *Log) Begin(slot int, word string, by Trigger) *Turn {
	return &Turn{id: newID(), slot: slot, word: word, by: by}
}

// Listening says the device is taking audio.
func (t *Turn) Listening() {
	if t == nil {
		return
	}
	t.at(&t.listening)
}

// Heard says Home Assistant has enough audio and is working on an answer.
func (t *Turn) Heard(text string) {
	if t == nil {
		return
	}

	// Not after the turn ended: its events may still be held for the score, and what comes late is
	// another run's.
	t.mu.Lock()
	if t.ended.IsZero() {
		t.heard = text
	}
	t.mu.Unlock()

	t.at(&t.thinking)
}

// Replying says an answer has arrived and is being spoken.
func (t *Turn) Replying(text string) {
	if t == nil {
		return
	}

	t.mu.Lock()
	if t.ended.IsZero() {
		t.reply = text
	}
	t.mu.Unlock()

	t.at(&t.replying)
}

// Fails says what went wrong, for the turn about to end in an error: Home Assistant's error code.
func (t *Turn) Fails(code string) {
	if t == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.failure = code
}

// Cancels says why the turn about to end as canceled is.
func (t *Turn) Cancels(why Reason) {
	if t == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.reason = why
}

// AwaitsScore says the device's own engine heard the wake word that opened this turn, and the peak it
// reached is on its way (Scored).
func (t *Turn) AwaitsScore() {
	if t == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.scoreDue = true
}

// Scored gives the turn its wake word's peak. A turn already over and holding its events for it sends
// them now; one whose events have gone is past adding to.
func (t *Turn) Scored(peak float64) {
	if t == nil {
		return
	}

	t.mu.Lock()
	if t.sent {
		t.mu.Unlock()
		return
	}
	t.score = peak
	held := t.wait != nil
	if held {
		t.wait.Stop()
	}
	t.mu.Unlock()

	if held {
		t.send()
	}
}

// TimesOut says the turn is about to end because the device gave up waiting.
func (t *Turn) TimesOut() {
	if t == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.failure, t.timedOut = "timeout", true
}

// Ends closes the turn and reports it. Only the first call counts: several things can notice a turn is
// over, and the one that noticed first is the one that timed it.
//
// The events are sent from a goroutine of their own. Ending is on the voice loop, and a write to a
// connection that has died waits out its timeout; the next wake must not. A turn still owed its wake
// word's score holds them for it, up to scoreWait.
func (t *Turn) Ends(how Ending) {
	if t == nil {
		return
	}

	t.mu.Lock()
	if !t.ended.IsZero() {
		t.mu.Unlock()
		return
	}
	t.ended, t.how = time.Now(), how
	if t.scoreDue && t.score == 0 {
		t.wait = time.AfterFunc(scoreWait, t.send)
		t.mu.Unlock()
		return
	}
	t.mu.Unlock()

	t.send()
}

// send reports the finished turn, once: the score arriving and the wait for it running out can both
// call it.
func (t *Turn) send() {
	t.mu.Lock()
	if t.sent {
		t.mu.Unlock()
		return
	}
	t.sent = true
	if wordsKept() {
		t.heard, t.reply = "", ""
	}
	fields, legacy := t.fields(t.how), t.legacy(t.how)
	remember(t.ended, fields, wordsKept())
	t.mu.Unlock()

	// A device that was never named has none to give, and an empty field says nothing a missing one
	// does not.
	if name := config.Get().Device.Name; name != "" {
		fields["device"] = name
	}
	events.post(component.Event{Name: TurnEvent, Data: fields})
	events.post(component.Event{Name: LegacyTurnEvent, Data: legacy})
}

// sender sends finished turns from a goroutine of its own, started with the first.
type sender struct {
	outbox chan component.Event
	once   sync.Once
	send   func(component.Event)
}

// events is the device's sender; a test has one of its own.
var events = &sender{
	outbox: make(chan component.Event, queued),
	send:   func(e component.Event) { component.Fire.Emit(e) },
}

// post hands a finished turn to the sender and returns at once. A full queue drops the newest.
func (s *sender) post(e component.Event) {
	s.once.Do(func() { safe.Go("turn events", s.run) })
	select {
	case s.outbox <- e:
	default:
		slog.Warn("turn: Home Assistant isn't taking events; dropped one", "id", e.Data["id"])
	}
}

// run sends what is posted, for as long as the daemon runs: one event that panics on the way out is
// that one event lost, not every turn after it.
func (s *sender) run() {
	for e := range s.outbox {
		s.sendOne(e)
	}
}

func (s *sender) sendOne(e component.Event) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("turn: sending an event failed", "event", e.Name, "id", e.Data["id"], "err", r)
		}
	}()
	s.send(e)
}

// Records says how long a recording was kept for this turn, which is what decides whether anything
// offers to play it. Zero for a turn nothing was kept for.
func (t *Turn) Records(seconds float64) {
	if t == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.audio = seconds
}

// ID is what asks the device for this turn's audio.
func (t *Turn) ID() string {
	if t == nil {
		return ""
	}
	return t.id
}

// at stamps a phase, unless the turn has closed or that phase has already begun — Home Assistant can
// say the same thing twice, and the first time is when it happened.
func (t *Turn) at(when *time.Time) {
	if t == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ended.IsZero() && when.IsZero() {
		*when = time.Now()
	}
}

// fields is the event's data. Everything is a string: the wire carries no other kind, so numbers go
// as decimal and a reader turns them back.
func (t *Turn) fields(how Ending) map[string]string {
	out := map[string]string{
		"version": Version,
		"id":      t.id,
		"slot":    strconv.Itoa(t.slot),
		"trigger": string(t.by),
		"ended":   string(how),
	}

	if t.word != "" {
		out["wake_word"] = component.Fit(t.word)
	}
	if t.heard != "" {
		out["heard"] = fit(t.heard)
	}
	if t.reply != "" {
		out["reply"] = fit(t.reply)
	}
	if how == Failed && t.failure != "" {
		out["error"] = component.Fit(t.failure)
	}
	if how == Canceled && t.reason != "" {
		out["reason"] = string(t.reason)
	}
	if t.score > 0 {
		out["wake_score"] = strconv.FormatFloat(t.score, 'f', 2, 64)
	}
	t.durations(out, "reply_ms")
	return out
}

// legacy is the event's data as EchoLocal sent it: an outcome rather than an ending and a trigger,
// the reply's time as speak_ms, and text cut only at what an event can carry.
func (t *Turn) legacy(how Ending) map[string]string {
	outcome := "completed"
	switch {
	case how == NoSpeech || how == Canceled:
		outcome = "cancelled" // spelled so on purpose: EchoLocal's readers match on it
	case how == Failed && t.timedOut:
		outcome = "timeout"
	case how == Failed:
		outcome = "failed"
	}

	out := map[string]string{
		"version": Version,
		"id":      t.id,
		"slot":    strconv.Itoa(t.slot),
		"outcome": outcome,
	}
	if t.word != "" {
		out["wake_word"] = component.FitEvent(t.word)
	}
	if t.heard != "" {
		out["heard"] = component.FitEvent(t.heard)
	}
	if t.reply != "" {
		out["reply"] = component.FitEvent(t.reply)
	}
	t.durations(out, "speak_ms")
	return out
}

// durations adds what both events share: the recording kept, and how long each phase took, the reply's
// under the key given.
func (t *Turn) durations(out map[string]string, replyKey string) {
	if t.audio > 0 {
		out["audio_seconds"] = strconv.FormatFloat(t.audio, 'f', 1, 64)
	}

	// A phase runs until the next one the turn reached, or until it ended. One it never reached has no
	// duration rather than a zero, so a turn that failed while thinking reports no reply time.
	marks := []struct {
		key string
		at  time.Time
	}{
		{"listen_ms", t.listening},
		{"think_ms", t.thinking},
		{replyKey, t.replying},
	}

	for i, mark := range marks {
		if mark.at.IsZero() {
			continue
		}

		until := t.ended
		for _, next := range marks[i+1:] {
			if !next.at.IsZero() {
				until = next.at
				break
			}
		}
		if ms := until.Sub(mark.at).Milliseconds(); ms > 0 {
			out[mark.key] = strconv.FormatInt(ms, 10)
		}
	}
}

// fit shortens a transcript or a reply to textLimit characters.
func fit(text string) string { return component.FitTo(text, textLimit) }

// The id outlives its recording, in every logbook entry the recorder keeps, so it has to stay unique
// across all of them.
func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}
