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
	"log/slog"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"
	"google.golang.org/protobuf/proto"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
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
	vs   *esphome.VoiceSatellite
	turn *conversation
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
	}

	wakeword.Requested.Listen(v.Start)

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

// Handle is the satellite's own protocol messages, which have no entity to arrive through.
func (v *Voice) Handle(ctx context.Context, c *esphome.Conn, msg proto.Message) error {
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
func (v *Voice) Ready() bool { return v.vs.Subscribed() }

// Start asks for a turn as if that slot's wake word had fired, which is how detection and the
// buttons both reach a pipeline. What that means from the phase the conversation is already in is
// the conversation's decision, not the caller's.
//
// A wake word said over a ringing alarm or timer silences it first, the way a button press does:
// whoever says it at a ringing alarm wants it quiet, and should not have to know the one word that
// stops it. The turn goes on, over a quiet room, and "stop" in it ends the ring (see stopsRing).
func (v *Voice) Start(slot int) {
	if ring.IsSounding() && ring.Silence() {
		slog.Info("wake word over a ring, silencing it")
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

	// Anything audible is what the press meant. Asking a question is what the button is for when the
	// device is doing nothing; while it is talking or playing, reaching for it means make it stop.
	if v.Stop() {
		return
	}

	// No wake word, so no slot to pair with: the first pipeline is the one Home Assistant falls back
	// to for anything that reports no phrase.
	v.turn.Start(0)
}

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
		if playing, _ := media.Get().Playing(); playing {
			slog.Info("stop word after the wake word: pausing the music")
			v.turn.Cancel()
			media.Get().Pause()
			return
		}
	}
	if !speaker.Sound().Busy() && !ring.IsSounding() {
		if playing, _ := media.Get().Playing(); !playing {
			slog.Debug("stop word ignored, nothing to stop")
			return
		}
	}
	v.Stop()
}

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

	if playing, _ := media.Get().Playing(); playing {
		media.Get().Pause()
		return true
	}
	// Last: a reminder left on the screen, with nothing else going on, is what the press was for.
	return ring.End()
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
	v.turn.Start(1)
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
