// Package voice is a turn: hearing a wake word, listening, waiting for an answer and playing it.
//
// Two halves. The conversation is a state machine on its own goroutine, so a button press, a pipeline
// event and a timeout cannot race each other. Around it sits the voice satellite, which is what Home
// Assistant talks to.
//
// Nothing here has an entity: a turn is not a setting and not a reading.
package voice

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"
	"google.golang.org/protobuf/proto"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

func init() {
	component.Register(component.Device, Get(), component.Order(30))
}

// Features is what the device claims it can do with a voice pipeline. Announce and
// StartConversation go together and both need a media player, which this device has.
const Features = esphome.DefaultVoiceFeatures |
	esphome.FeatureSpeaker |
	esphome.FeatureAnnounce |
	esphome.FeatureStartConversation |
	esphome.FeatureTimers

type Voice struct {
	vs     *esphome.VoiceSatellite
	turn   *conversation
	effect *esphome.Action
}

var (
	once   sync.Once
	shared *Voice
)

func Get() *Voice {
	once.Do(func() { shared = build() })
	return shared
}

// owner is asked whether the press of the action button happening now belongs to something other
// than the assistant. It is registered rather than called for, because the only thing that owns a
// press — the setup page, which a press on the device lets a browser into — sits above this package
// and cannot be imported from here.
var owner struct {
	mu sync.Mutex
	fn func() bool
}

// OwnsPress registers something that may own a press of the action button. While it says it does,
// the press starts no turn: on a device with no screen the press that answers the setup page arrives
// here as an ordinary tap, and there is nothing else to tell the button apart.
func OwnsPress(fn func() bool) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	owner.fn = fn
}

// pressOwned is whether the press now being handled is somebody else's.
func pressOwned() bool {
	owner.mu.Lock()
	fn := owner.fn
	owner.mu.Unlock()
	return fn != nil && fn()
}

// build makes the satellite and the conversation together: the satellite's callbacks are the
// conversation's inputs, and the conversation answers back through it.
//
// What the device can hear is not stored — it is worked out on every configuration request, because
// models arrive and are deleted while the device runs.
func build() *Voice {
	ours := wake.Lib().Ours()
	active := activeWakeWords(ours, wakeword.Slots)

	v := &Voice{
		vs: &esphome.VoiceSatellite{
			ActiveWakeWords:     active,
			MaxActiveWakeWords:  wakeword.Slots,
			OnExternalWakeWords: wakeword.Answer,
		},
	}
	v.turn = newConversation(v.vs)
	slog.Info("wake words", "ours", len(ours), "active", active)

	v.vs.OnTimer = timer.Get().Event
	v.vs.OnSubscribed = func(subscribed bool) {
		if !subscribed {
			timer.Get().Forget()
		}
		v.turn.post(event{kind: evWatch})
	}
	// A stream to Home Assistant stops the moment the microphones are cut, not at the next look.
	mute.Get().Changed.Listen(func(bool) { v.turn.post(event{kind: evWatch}) })

	wakeword.Requested.Listen(v.Start)
	wakeword.CancelRequested.Listen(func(struct{}) { v.CancelRequest() })

	// The action button is the only one where a hold means something different from a press, and what
	// it means is the conversation's to decide. The other buttons are somebody else's listeners.
	buttons.Get().Events.Listen(func(e buttons.Event) {
		if e.Name != buttons.Action {
			return
		}
		switch e.Kind {
		case buttons.Tap:
			// The press may not be the assistant's at all: the setup page lets a browser in on a press on
			// the device, and on a device with no screen there is nothing else to say so. A press meant
			// for a settings page must not put the microphone on the network.
			if pressOwned() {
				return
			}
			v.Action()
		case buttons.Hold:
			v.ActionHold()
		case buttons.LongHold:
			// Held on past the second assistant, to something else (Bluetooth pairing on the Dot): the
			// turn the hold started on the way is not what was wanted.
			if v.turn.Busy() {
				v.turn.Cancel()
			}
		}
	})
	return v
}

func (v *Voice) Name() string { return "conversation" }

// Actions is voice_effect (ShowEffect).
func (v *Voice) Actions() []*esphome.Action {
	if v.effect == nil {
		v.effect = &esphome.Action{
			Name: "voice_effect",
			Args: []esphome.Arg{{Name: "effect", Type: esphome.ArgString}},
			Run: func(c esphome.Call) (any, error) {
				return nil, v.ShowEffect(strings.TrimSpace(c.String("effect")))
			},
		}
	}
	return []*esphome.Action{v.effect}
}

// ShowEffect puts the effect named name on the ring for the step the running request is in, for an
// automation to say what the request is waiting on: another speech-to-text taking over, a slow
// answer. None turns the ring off for it, and Default (or nothing) goes back to the step's own
// setting. It is the request's, not a setting: nothing is saved, and the next step, or the next
// request, shows its own effect as ever. With no request running it does nothing; a name the ring
// does not have is refused.
func (v *Voice) ShowEffect(name string) error {
	switch {
	case name == "", name == component.EffectDefault, name == component.EffectNone:
	case !slices.Contains(led.EffectNames(), name):
		return fmt.Errorf("no ring effect called %q", name)
	}
	if !v.turn.Busy() {
		return nil
	}
	v.turn.askEffect(name)
	return nil
}

// Handle is the satellite's own protocol messages, which have no entity to arrive through.
//
// The connection a subscription arrives on is kept: a run at the wake word stage is a request the
// satellite has no call for, and goes out on it directly (hawake.go).
func (v *Voice) Handle(ctx context.Context, c *esphome.Conn, msg proto.Message) error {
	if m, ok := msg.(*api.SubscribeVoiceAssistantRequest); ok && m.GetSubscribe() {
		v.turn.conn.Store(c)
	}
	return v.vs.Handle(ctx, c, msg)
}

// Run owns the conversation until ctx is canceled. Nothing happens on a wake word until it is
// running.
func (v *Voice) Run(ctx context.Context) error {
	v.turn.Run(ctx)
	return nil
}

// Ready reports whether Home Assistant has a voice pipeline listening. Wake detection runs before
// that happens, but nothing can be done with a detection until it does, so this is what the device
// shows on the ring while it comes up.
func (v *Voice) Ready() bool { return v.vs.Subscribed() || config.Get().Brain.Direct() }

// HomeAssistant reports whether Home Assistant itself has a voice pipeline listening, whatever the
// brain. Ready is always true with a direct brain, so it says nothing about whether Home Assistant
// can still reach the device.
func (v *Voice) HomeAssistant() bool { return v.vs.Subscribed() }

// Start asks for a turn as if that slot's wake word had fired, which is how detection and the
// buttons both reach a pipeline. What that means from the phase the conversation is already in is
// the conversation's decision, not the caller's.
//
// A wake word said over a ringing alarm or timer silences it first, the way a button press does:
// whoever says it at a ringing alarm wants it quiet, and should not have to know the one word that
// stops it. The turn goes on, over a quiet room, and "stop" in it ends the ring (see stopsRing).
func (v *Voice) Start(slot int) { v.open(slot, false, time.Time{}) }

// CancelRequest is the Cancel request button: it ends a request that is running, whichever
// assistant it is for and whatever step it is at, as a tap on the action button ends one, and like
// that tap it silences what holds the speaker then: an announcement or a call ring that came in
// during the request stops with it. A ringing alarm is not on the speaker claim and goes on. With no
// request running it does nothing.
func (v *Voice) CancelRequest() {
	if !v.turn.Busy() {
		return
	}
	slog.Info("cancel request button, canceling the request", "phase", v.turn.Phase())
	v.turn.Cancel()
}

// Woke is Start for the wake word itself, which is the one way a turn can be opened by mistake: by a
// sound that was not the word (see Mistaken).
func (v *Voice) Woke(slot int) { v.open(slot, true, time.Time{}) }

// WokeAt is Woke for a wake word heard at at and acted on later, once the devices that heard it agreed
// which answers (feature/nearest). The turn sends what was said in between, from the history.
func (v *Voice) WokeAt(slot int, at time.Time) { v.open(slot, true, at) }

// CanAnswer is whether a wake word in slot would open a turn here now: an assistant is there to take it
// (Home Assistant, or the device's own direct pipeline), the microphones are not taken by something
// else (a talk through a camera, takes being recorded), and the slot has a wake word Home Assistant
// knows it by. A slot chosen for realtime voice is
// answered by it alone (backendFor): set up and with minutes left today, whatever Home Assistant is
// doing. A device that could not answer must not claim a wake word over the others (feature/nearest).
func (v *Voice) CanAnswer(slot int) bool {
	if micTaken() {
		return false
	}
	if w := config.Get().Wake.Words; slot >= 0 && slot < len(w) && w[slot].Realtime {
		return v.turn != nil && v.turn.realtime.Ready()
	}
	if !v.Ready() {
		return false
	}
	if config.Get().Brain.Direct() {
		return true
	}
	return slot >= 0 && slot < len(v.ActiveWakeWords())
}

func (v *Voice) open(slot int, woke bool, at time.Time) {
	if micTaken() {
		slog.Info("wake word ignored: the microphones are taken", "by", heldBy())
		return
	}
	if ring.IsSounding() && ring.Silence() {
		slog.Info("wake word over a ring, silencing it")
	}
	if woke {
		if at.IsZero() {
			v.turn.Woke(slot)
		} else {
			v.turn.WokeAt(slot, at)
		}
		return
	}
	v.turn.Start(slot)
}

// Busy reports whether a turn is running, for anything that has to leave the speaker alone while one
// is.
func (v *Voice) Busy() bool { return v.turn.Busy() }

// Cancel stops a turn that is running, for a gesture that turned out to mean something else: the
// second tap of a double, the way a long hold already undoes the turn its hold began.
func (v *Voice) Cancel() {
	if v.turn.Busy() {
		v.turn.Cancel()
	}
}

// Action is the action button: it gives up on whatever is happening, or starts something if nothing
// is. Canceling is the more useful half — it is the way out of a turn that is waiting on a pipeline
// that is not going to answer.
func (v *Voice) Action() {
	// A call ringing or up is what the button is for until it is over: it answers or hangs up.
	if phone.Get().Button() {
		return
	}

	// Talking through a camera, or takes being recorded: the press ends it, as it hangs up a call,
	// rather than asking a question that would go out of the camera's speaker or into the takes.
	if micTaken() && !ring.IsSounding() {
		if y, ok := holder(); ok {
			go y.release()
		}
		return
	}

	// Anything audible is what the press meant. Asking a question is what the button is for when the
	// device is doing nothing; while it is talking or playing, reaching for it means make it stop.
	if v.Stop() {
		return
	}

	// No wake word, so no slot to pair with: the first pipeline is the one Home Assistant falls back
	// to for anything that reports no phrase.
	v.turn.Start(0)
}

// What Stop and Interrupt ask of the music, and what lets go of the wake light a ring left on; here
// so tests can stand in for them.
var (
	musicPlaying = func() bool { playing, _ := media.Get().Playing(); return playing }
	pauseMusic   = func() { media.Get().Pause() }
	releaseRing  = ring.Release
)

// Interrupt is the stop word.
//
// It only acts while the device is making a sound. "Stop" is an ordinary word: somebody halfway through
// "stop the timer" is talking to Home Assistant, not to the device, and cutting their turn off there
// would be worse than not listening for it at all. Nothing is playing then, so there is nothing the word
// could sensibly mean.
func (v *Voice) Interrupt() {
	// "<wake word>, stop" over music: the stop word hears "stop" while the turn the wake word opened is
	// still listening, and it is the music that was meant, not the question that has not been asked.
	if v.turn.Phase() == phaseListening && !ring.IsSounding() {
		if musicPlaying() {
			slog.Info("stop word after the wake word: pausing the music")
			v.turn.Cancel()
			pauseMusic()
			return
		}
	}
	if !speaker.Sound().Busy() && !ring.IsSounding() {
		if !musicPlaying() {
			// Nothing audible, but the wake light held at full after an alarm (#161) is the device
			// plainly doing something, and stop is what somebody waking under it says to turn it off.
			if releaseRing() {
				slog.Info("stop word with nothing sounding: the held wake light let go")
				return
			}
			slog.Debug("stop word ignored, nothing to stop")
			return
		}
	}
	v.Stop()
}

// LookHere says the answer being given put something on the screen, so it is not followed by
// listening again, whose screen would cover it.
func (v *Voice) LookHere() { v.turn.LookHere() }

// Stop ends whatever the device is doing audibly, and reports whether there was anything to end.
//
// One ladder, because there is one meaning: a turn is canceled, a sound is silenced, a track is
// stopped. The action button falls through to starting a turn when it returns false; a stop word has
// nothing to fall through to and simply does nothing.
func (v *Voice) Stop() bool {
	// Before the turn, because a timer or an alarm ringing over one is what the person is reaching for.
	// Only while one sounds: ring.End also takes down a reminder left on the screen, and that must
	// not stand in for stopping the music or a turn.
	if ring.IsSounding() && ring.End() {
		return true
	}

	if v.turn.Busy() {
		v.turn.Cancel()
		return true
	}

	// An announcement outside a turn: nothing is listening, but something is playing.
	if sound := speaker.Sound(); sound.Busy() {
		sound.Silence()
		return true
	}

	if musicPlaying() {
		pauseMusic()
		return true
	}
	// Last: a reminder left on the screen, with nothing else going on, is what the press was for. And
	// the wake light held at full after an alarm (#161), which nothing above stops: reaching for stop
	// with nothing sounding lets it go, as Stop in Home Assistant does.
	ended := ring.End()
	return releaseRing() || ended
}

// ActionHold is holding the action button, which reaches the second assistant. Holding does not
// cancel: a press is the way out of a turn, so holding while one is running interrupts it with the
// other assistant instead, which is the same thing saying the other wake word would do.
func (v *Voice) ActionHold() {
	// Where holding on reaches something else (buttons.LongHold), a hold with no second assistant set
	// up is on its way there, not a request to report as failed.
	if buttons.LongHolds() {
		if _, ok := v.turn.phraseFor(1); !ok {
			slog.Debug("action held, no second assistant set up")
			return
		}
	}
	if micTaken() {
		return
	}
	v.turn.Start(1)
}

// A feature that takes the microphones for something of its own (feature/talkback sending them to a
// camera, feature/takes recording takes of a word; neither can be imported here) says so through
// YieldTo: while its taken says so, no turn starts, and the action button calls its release instead.
type yielding struct {
	name    string // what has the microphones, for the log: "a talk through a camera"
	taken   func() bool
	release func()
}

// yields is every feature that has said so, in the order they did. It is read on every frame of a
// stream to Home Assistant (hawake.go), so it is a pointer to a list that is replaced, never changed.
var yields atomic.Pointer[[]yielding]

// YieldTo is how such a feature is told about. Each call adds one: a talk and a recording are
// different features, and either may have the microphones.
func YieldTo(name string, taken func() bool, release func()) {
	for {
		old := yields.Load()
		var next []yielding
		if old != nil {
			next = append(next, *old...)
		}
		next = append(next, yielding{name: name, taken: taken, release: release})
		if yields.CompareAndSwap(old, &next) {
			return
		}
	}
}

// holder is the feature that has the microphones now, if one has.
func holder() (yielding, bool) {
	if ys := yields.Load(); ys != nil {
		for _, y := range *ys {
			if y.taken() {
				return y, true
			}
		}
	}
	return yielding{}, false
}

func micTaken() bool {
	_, taken := holder()
	return taken
}

// MicTaken is whether a feature that said so through YieldTo has the microphones now, for a listener
// on the action button that does not go through Action: the Dot's double tap, whose first tap may
// have been the one that started or stopped a recording.
func MicTaken() bool { return micTaken() }

// heldBy names what has the microphones, for the log.
func heldBy() string {
	y, _ := holder()
	return y.name
}

// OnWakeWord is called when Home Assistant changes the selection, so the engine can follow. It is
// given every slot: load reports which of them it accepted, and only those are echoed back as
// active, because Home Assistant takes the echo as authoritative and reverts a slot whose word is
// missing from it. A slot the device will not run therefore reverts in the interface rather than
// sitting there looking armed.
//
// selected is called after the slots have been written, for whatever else a new selection changes.
func (v *Voice) OnWakeWord(load func(ids []string) []string, selected func()) {
	v.vs.OnSetActiveWakeWords = func(ids []string) {
		accepted := load(ids)
		v.vs.ActiveWakeWords = accepted

		for slot := range wakeword.Slots {
			id := ""
			if slot < len(accepted) {
				id = accepted[slot]
			}
			if err := config.Set().Wake(slot).ID(id); err != nil {
				slog.Error("saving the wake word failed", "slot", slot+1, "err", err)
			}
		}
		// Nothing asked for is "No wake word", which must survive a restart; something asked for and
		// refused is not, and leaves the start-up default to do its job.
		if err := config.Set().NoneChosen(len(ids) == 0); err != nil {
			slog.Error("saving the wake word choice failed", "err", err)
		}
		if len(accepted) != len(ids) {
			slog.Warn("some wake words were refused", "asked", ids, "running", accepted)
		}
		selected()
	}
}

// ChooseWakeWord puts id in the first slot from the device itself — the settings sheet — by the same
// path Home Assistant's selection takes, keeping a second slot if one is set. The API then reconnects,
// because Home Assistant reads the selection once per connection and would otherwise keep showing
// the old word.
func (v *Voice) ChooseWakeWord(id string) {
	set := v.vs.OnSetActiveWakeWords
	if set == nil || id == "" {
		return
	}
	ids := []string{id}
	if cur := v.vs.ActiveWakeWords; len(cur) > 1 && cur[1] != id {
		ids = append(ids, cur[1])
	}
	slog.Info("wake word chosen on the device", "id", id)
	set(ids)
	component.Reconnect.Emit(struct{}{})
}

// ActiveWakeWords is what the device is advertising as listening, by slot.
func (v *Voice) ActiveWakeWords() []string { return v.vs.ActiveWakeWords }

// SetActiveWakeWords corrects what is advertised to what is actually running. The engine loads at
// start-up rather than waiting to be told, so this is how the advertisement is reconciled with what
// came up: anything that failed to load is not claimed.
func (v *Voice) SetActiveWakeWords(ids []string) {
	v.vs.ActiveWakeWords = ids
	slog.Info("wake words listening", "active", ids)
}
