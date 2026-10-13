package detect

import (
	"context"
	"log/slog"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/diag"
	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
	"github.com/HuskerMinion/techo5/echod/internal/service"
)

func init() {
	// Before the API, so Home Assistant cannot read the wake words while they are still loading and be
	// told about one that then fails.
	component.Register(component.Device, Get(), component.Order(40),
		component.Supervise(service.Restart(time.Second, 30*time.Second)))
}

// Detect is the engine as the device runs it: the wake words the user chose, loaded into it, and the
// ring saying so until they can hear.
type Detect struct {
	engine *Engine
	busy   *wakeBusy
	stop   *esphome.Number

	// score is the last wake's peak, for setting the threshold by: see newScoreEntity.
	score *esphome.Sensor

	// clipsTotal and clipsKinds are how many wake clips the device keeps, and keepClips is Keep wake
	// clips, which turns the recorder on: see clips.go.
	clipsTotal *esphome.Sensor
	clipsKinds *esphome.TextSensor
	keepClips  *esphome.Switch
	clips      *clips

	// onHA is Home Assistant's wake word engine: see onha.go.
	onHA onHAEntities

	// ducker gets the music out of the way after an utterance that nearly fired; see nearmiss.go.
	ducker *ducker
}

var (
	once   sync.Once
	shared *Detect
)

func Get() *Detect {
	once.Do(func() { shared = newDetect() })
	return shared
}

// playingSlack is how much lower the wake threshold sits while the echo canceller is running.
const playingSlack = 0.10

// slackFloor is as low as the slack is allowed to drag a threshold, whatever it started at.
const slackFloor = 0.5

// thresholdFor is the cutoff a slot's score is judged against. base supplies the per-slot wake word
// threshold; stop is the stop word's own, which is configured separately.
//
// While the speaker plays and the canceller runs, what reaches the detector is the residual of the
// music plus the voice, and the word scores lower than it does in a quiet room. A little slack here
// is worth more than the false wakes it risks: the music is the reference the canceller has, so it
// is the one sound least able to fake the word.
//
// The stop word takes the same slack as every other slot. It needs it more than they do, not less:
// it is the one word said while the speaker is certainly playing, because playing is what it is
// asked to stop. Exempting it made the one word whose job is to interrupt a sound the only word
// judged with no allowance for the sound.
func thresholdFor(slot int, base func(int) float64, stop float64, canceling bool) float64 {
	t := stop
	if slot != StopSlot {
		t = base(slot)
	}
	if canceling {
		t = max(t-playingSlack, slackFloor)
	}
	return t
}

// settleTail is how long past the unmute sound detections stay held: the echo of its last notes,
// and the canceller letting go of them.
const settleTail = 300 * time.Millisecond

// unmuteSettle is how long detections are held after an unmute: as long as the unmute sound plays,
// and settleTail after. The sound plays straight into the wake words as they come back cold from
// the mute, while the canceller is starting and the threshold already has the playing slack taken
// off, and it was heard as the word (a wake at 0.716 against 0.70, nothing said). Its length is the
// sound's own: two seconds for Home Assistant's, less for the classic notes, up to ten for a
// recording of the owner's.
func unmuteSettle() time.Duration {
	d := settleTail
	for _, n := range speaker.MuteSound(false) {
		d += time.Duration(n.Ms) * time.Millisecond
	}
	return d
}

func newDetect() *Detect {
	// Sized to reach the stop word's reserved index. The slots between it and Home Assistant's are never
	// loaded, and an unloaded slot is one comparison a frame.
	e := New(StopSlot+1, mic.Get())

	e.Threshold = func(slot int) float64 {
		return thresholdFor(slot, wakeword.Threshold, config.Get().Wake.Stop.Threshold, mic.Get().Canceling())
	}

	e.OnDetect = func(slot int) {
		// A call has the microphones and the speaker. The far end talking through the speaker is not
		// someone in the room, and a turn would take the call's audio away mid-sentence. A talk through
		// a camera (feature/talkback) is the same: whoever is at the door is being spoken to.
		if phone.Get().Busy() || talkback.Get().Busy() {
			// A ring during a call is the one thing that still has to hear "stop". The call page
			// takes every tap and Home Assistant cannot stop a timer, so without this a timer that
			// finishes mid-call sounds for its full fifteen minutes with no way out at all.
			//
			// It ends the ring and nothing else: the call is not interrupted, and the word is not
			// passed to the turn, because the far end talking through the speaker is not somebody in
			// the room.
			if slot == StopSlot && ring.IsSounding() {
				slog.Info("stop word during a call, ending the ring")
				ring.End()
				return
			}
			slog.Debug("wake word ignored during a call or a talk through a camera", "slot", slot+1)
			return
		}
		if slot == StopSlot {
			voice.Get().Interrupt()
			return
		}
		// Heard here while Home Assistant's engine is listening too (config.OnHA), it still wakes: the
		// run there is closed and the turn opens here. The same word heard by both opens one turn, since
		// a wake word while listening is ignored.
		voice.Get().Woke(slot)
	}

	// A wake the device was not sure of, asked about when confirming is on: only the first wake word,
	// whose pipeline is the one a run with no phrase reaches (voice's openWatch), never the stop word,
	// and never while the microphones are somebody else's.
	e.Candidate = func(slot int) float64 {
		if slot != 0 {
			return 0
		}
		on := config.Get().Wake.OnHA
		if !on.Confirm {
			return 0
		}
		return on.ConfirmAt()
	}
	e.OnCandidate = func(slot int, peak float64) {
		if phone.Get().Busy() || talkback.Get().Busy() || voice.Get().HomeAssistantHears(slot) {
			return
		}
		voice.Get().Candidate(slot, peak)
	}

	d := &Detect{engine: e, busy: newWakeBusy(led.Get().Busy(), e.Ready)}
	d.ducker = newDucker()
	d.ducker.watch(e)
	d.stop = newStopEntity(d)
	d.score = newScoreEntity()
	d.onHA = newOnHAEntities()
	e.OnScored = func(slot int, peak, _ float64) {
		// Only Home Assistant's wake words: the stop word, and any other word listened for past them,
		// has its own threshold and says nothing about theirs.
		if slot < wakeword.Slots {
			d.score.Set(float32(peak))
		}
	}
	// A wake Home Assistant confirmed is a wake, and its score is what says how far under the threshold
	// the word is landing. One it did not confirm is not shown: it opened nothing.
	voice.Confirmed.Listen(func(c voice.Confirmation) { d.score.Set(float32(c.Peak)) })

	// After every other hook it chains onto is in place.
	d.clipsTotal, d.clipsKinds = newClipsEntities()
	d.keepClips = newKeepClipsEntity(d)
	d.clips = startClips(e, func(total int, kinds string) {
		d.clipsTotal.Set(float32(total))
		d.clipsKinds.Set(kinds)
	})

	// A cut microphone hands on silence, and running the models over it is work that cannot find
	// anything. The engines go down with the microphones and come back with them.
	mute.Get().Changed.Listen(func(muted bool) {
		e.Quiet(muted)
		if !muted {
			settle := unmuteSettle()
			e.Settle(settle)
			slog.Info("wake words settling after unmute", "for", settle)
		}
	})
	e.OnReady = d.busy.scored

	// The engine loads on every start, including a restart. Home Assistant only pushes a selection when
	// the user changes one, so an engine that came back empty would leave the device deaf while it went
	// on advertising wake words it was not listening for.
	e.Load = func() error {
		turn := voice.Get()
		turn.SetActiveWakeWords(d.load(turn.ActiveWakeWords()))
		d.loadStop()
		// A device that was muted when it was switched off comes back muted, and the hook below only
		// fires on a change, so the state has to be read once here as well.
		if muted, err := mute.Get().Muted(); err == nil {
			e.Quiet(muted)
		}
		return nil
	}

	// A selection both downloads models and lets go of the ones it replaced, so it is the one thing that
	// moves either number the disk reports.
	voice.Get().OnWakeWord(d.load, diag.Get().Measure)

	ours := wake.Lib().Ours()
	slog.Info("wake words installed", "count", len(ours),
		"openwakeword", len(wake.OfKind(ours, wake.KindOpenWakeWord)),
		"microwakeword", len(wake.OfKind(ours, wake.KindMicroWakeWord)))

	return d
}

func (d *Detect) Name() string { return "wake" }

func (d *Detect) Start(ctx context.Context) error { return d.engine.Start(ctx) }

func (d *Detect) Run(ctx context.Context) error { return d.engine.Run(ctx) }

func (d *Detect) Close() error { return d.engine.Close() }

// Actions are the wake clips' (clips.go).
func (d *Detect) Actions() []*esphome.Action { return d.clips.actions() }

// load puts one wake word in each slot and reports the ids that came up. Whatever the engine refuses
// is left out, so Home Assistant reverts that slot rather than showing a wake word the device is not
// listening for.
func (d *Detect) load(ids []string) []string {
	d.busy.begin()

	// A selection may name a model Home Assistant is offering but this device has never had, so the
	// library is asked rather than a list captured at boot: this is where a new word arrives.
	models := wake.Lib().Ensure(ids)

	var accepted []string
	var loaded []int
	for slot := range wakeword.Slots {
		if slot >= len(ids) || ids[slot] == "" {
			d.engine.Clear(slot)
			continue
		}

		m, ok := wake.Find(models, ids[slot])
		if !ok {
			slog.Warn("unknown wake word", "slot", slot+1, "id", ids[slot])
			d.engine.Clear(slot)
			continue
		}
		if err := d.engine.Use(slot, m); err != nil {
			slog.Error("loading the selected wake word failed", "slot", slot+1, "id", m.ID, "err", err)
			d.engine.Clear(slot)
			continue
		}
		accepted = append(accepted, m.ID)
		loaded = append(loaded, slot)
	}

	// Only the slots that took a wake word are waited on. A selection that loaded nothing has nothing to
	// warm up, so the ring goes back to what it was showing rather than animating for a device that is
	// not going to hear anything.
	d.busy.waitFor(loaded)
	return accepted
}
