package detect

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/diag"
	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/feature/nearest"
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

	// loading is held for a whole load, so a refresh (wakeword.Refresh) and a selection take turns, and
	// asked is what the latest one was asked for, which is what a refresh loads again. refreshing keeps
	// a run of connects from queueing refreshes behind it, and lib is the library, wake.Lib() unless a
	// test says otherwise.
	loading    sync.Mutex
	asked      []string
	refreshing atomic.Bool
	lib        *wake.Library

	// hangup is the hang-up word's select and threshold, and its loading with the call: see hangup.go.
	hangup *hangup

	// score is the last wake's peak, for setting the threshold by: see newScoreEntity.
	score *esphome.Sensor

	// clipsTotal and clipsKinds are how many wake clips the device keeps, and keepClips is Keep wake
	// clips, which turns the recorder on: see clips.go.
	clipsTotal *esphome.Sensor
	clipsKinds *esphome.TextSensor
	keepClips  *esphome.Switch
	clips      *clips
	// clipsSave is held while Keep wake clips is saved, or an off that was not saved is tried again, so
	// a retry's look and its save cannot straddle the switch being turned on (clipsoff.go).
	// clipsOffRetrying is whether that retry runs, and clipsFails and clipsLogged how often it has
	// failed in a row and when that was last logged. All under clipsSave.
	clipsSave        sync.Mutex
	clipsOffRetrying bool
	clipsFails       int
	clipsLogged      time.Time

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

// slackFor reports whether a slot is judged with the allowance for the speaker (thresholdFor): while the
// canceller runs, except for the words that end a call, during one. A call keeps the canceller running
// even with the far end silent, so they would always be judged low, and a word heard by mistake ends the
// call. The stop word keeps the allowance over a ring, which it has to be heard over to stop.
func slackFor(slot int, canceling, onCall, ringing bool) bool {
	switch {
	case !canceling:
		return false
	case onCall && slot == HangupSlot:
		return false
	case onCall && slot == StopSlot:
		return ringing
	}
	return true
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
	// Sized to reach the reserved indexes, the stop word's and the hang-up word's. The slots between them
	// and Home Assistant's are never loaded, and an unloaded slot is one comparison a frame.
	e := New(HangupSlot+1, mic.Get())

	// The hang-up word is judged as a wake word is, its own threshold standing in for the slot's.
	base := func(slot int) float64 {
		if slot == HangupSlot {
			return config.Get().Wake.Hangup.ThresholdAt()
		}
		return wakeword.Threshold(slot)
	}
	e.Threshold = func(slot int) float64 {
		slack := slackFor(slot, mic.Get().Canceling(), phone.Get().Busy(), ring.IsSounding())
		return thresholdFor(slot, base, config.Get().Wake.Stop.Threshold, slack)
	}

	e.OnDetect = func(slot int) {
		c := config.Get()
		h := heard{
			phase:    phone.Get().State().Phase,
			talkBack: talkback.Get().Busy(),
			ringing:  ring.IsSounding(),
			stopHang: c.Wake.Hangup.StopWord(),
			callWake: c.Home.CallWake,
			farEnd:   phone.Get().FarEndTalking(),
			turnOpen: voice.Get().Busy(),
		}
		switch routeFor(slot, h) {
		case routeHangUp:
			if slot == StopSlot {
				hangUp("stop word")
			} else {
				hangUp("hang-up word")
			}
		case routeCancelTurn:
			slog.Info("stop word during a turn on a call, canceling the turn")
			voice.Get().Cancel()
		case routeCancelAndHangUp:
			if hangUp("hang-up word during a turn") {
				voice.Get().Cancel()
			}
		case routeEndRing:
			slog.Info("stop word during a call, ending the ring")
			ring.End()
		case routeInterrupt:
			voice.Get().Interrupt()
		case routeFarEnd:
			slog.Info("wake word during a call ignored, the far end was talking", "slot", slot+1)
		case routeTurn:
			if h.phase != phone.Idle {
				slog.Info("wake word during a call", "slot", slot+1)
			}
			// Heard here while Home Assistant's engine is listening too (config.OnHA), it still wakes:
			// the run there is closed and the turn opens here. The same word heard by both opens one
			// turn, since a wake word while listening is ignored.
			//
			// The other devices that heard it may be asked first which of them heard it best
			// (feature/nearest); with that off, or nobody to ask, the turn opens at once as it always
			// has.
			nearest.Get().Woke(slot)
		default:
			slog.Debug("wake word ignored during a call or a talk through a camera", "slot", slot+1)
		}
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
	d.hangup = newHangup(d)
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
		// The stop word is spared while an alarm or timer rings: it has to stop when told, and the sound
		// could otherwise hold it for as long as ten seconds. With nothing ringing it settles too, or
		// the unmute sound could be heard as "stop" and cut itself short, and music with it.
		if !muted {
			settle := unmuteSettle()
			if ring.IsSounding() {
				e.Settle(settle, StopSlot)
			} else {
				e.Settle(settle)
			}
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
		// A call already up when the engine starts has its hang-up word, as one that starts later does.
		d.hangup.follow(phone.Answerable(phone.Get().State().Phase))
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
	wakeword.Refresh = d.refresh

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
	d.loading.Lock()
	defer d.loading.Unlock()
	return d.loadLocked(ids)
}

// refresh swaps in the models Home Assistant replaced under the ids that are running. The new file is
// fetched first, and only a slot whose model arrived is cleared and loaded again: the engine keeps a
// slot that already runs the id as it is, and a download that failed leaves the old model listening.
func (d *Detect) refresh() {
	if !d.refreshing.CompareAndSwap(false, true) {
		return
	}
	defer d.refreshing.Store(false)

	d.loading.Lock()
	defer d.loading.Unlock()

	accepted, ok := d.refreshLocked()
	// Home Assistant has not changed its selection, so it is not told; the turn is, if what runs moved.
	if turn := voice.Get(); ok && !slices.Equal(accepted, turn.ActiveWakeWords()) {
		turn.SetActiveWakeWords(accepted)
	}
}

// refreshLocked is refresh under the lock, reporting what runs after it and whether anything changed.
func (d *Detect) refreshLocked() ([]string, bool) {
	// Asked again here: a selection that came first has already fetched what it needed.
	fresh := d.library().Refetch(d.library().Stale())
	if len(fresh) == 0 {
		return nil, false
	}
	for slot, id := range d.asked {
		if slices.Contains(fresh, id) {
			d.engine.Clear(slot)
		}
	}
	accepted := d.loadLocked(d.asked)
	for _, id := range fresh {
		if !slices.Contains(accepted, id) {
			slog.Error("a replaced wake word would not load; its slot is off until a word is chosen", "id", id)
		}
	}
	return accepted, true
}

func (d *Detect) library() *wake.Library {
	if d.lib != nil {
		return d.lib
	}
	return wake.Lib()
}

func (d *Detect) loadLocked(ids []string) []string {
	d.asked = slices.Clone(ids)
	d.busy.begin()

	// A selection may name a model Home Assistant is offering but this device has never had, so the
	// library is asked rather than a list captured at boot: this is where a new word arrives.
	models := d.library().Ensure(ids)

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
	d.library().Loaded(accepted)

	// The hang-up word's list leaves the wake words out, and a word just chosen as one is no longer
	// listened for as the hang-up word.
	if d.hangup != nil {
		d.hangup.refreshOptions()
		d.hangup.follow(phone.Answerable(phone.Get().State().Phase))
	}

	// Only the slots that took a wake word are waited on. A selection that loaded nothing has nothing to
	// warm up, so the ring goes back to what it was showing rather than animating for a device that is
	// not going to hear anything.
	d.busy.waitFor(loaded)
	return accepted
}
