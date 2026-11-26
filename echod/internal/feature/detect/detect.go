package detect

import (
	"context"
	"fmt"
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
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/feature/takes"
	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/bootreason"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
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

	// base is each slot's own threshold, the stop word's apart (thresholdFor), as the scores view
	// shows it (scores.go).
	base func(slot int) float64

	// commands are the command words' selects and thresholds, and their loading with the music, in slot
	// order, and vote is the ones heard together: see command.go.
	commands []*command
	vote     commandVote

	// contest settles a wake word and a command word that crossed on the same thing said.
	contest contest

	// loading is held for a whole load, so a refresh (wakeword.Refresh) and a selection take turns, and
	// asked is what the latest one was asked for, which is what a refresh loads again. refreshing keeps
	// a run of connects from queueing refreshes behind it, and lib is the library, wake.Lib() unless a
	// test says otherwise.
	loading    sync.Mutex
	asked      []string
	refreshing atomic.Bool
	lib        *wake.Library
	// refused is the saved selection restore last loaded and the engine would not take all of, with the
	// library's version then, under loading. The same selection over the same models is not loaded
	// again on every configuration request: each load lights the ring and moves the command and hang-up
	// words, and a model the engine refuses refuses again.
	refused *refusal

	// stopWord is the stop word's select, and stopMu keeps one load of the stop slot at a time: see
	// stopword.go and loadStop.
	stopWord *stopWord
	stopMu   sync.Mutex

	// hangup is the hang-up word's select and threshold, and its loading with the call: see hangup.go.
	hangup *hangup

	// score is the last wake's peak, for setting the threshold by: see newScoreEntity.
	score *esphome.Sensor

	// wakeProblem says why a wake word chosen in Home Assistant is not listening, or "None": a model
	// the device refuses drops the slot to no wake word, and its reason was only in the log.
	wakeProblem *esphome.TextSensor

	// dropMu guards lastDrop: when each wake word's model last failed on live audio (dropped.go).
	dropMu   sync.Mutex
	lastDrop map[string]time.Time

	// frames is how the wake word loop keeps up: see newFrameEntities.
	frames frameEntities

	// clipsTotal and clipsKinds are how many wake clips the device keeps, and keepClips is Keep wake
	// clips, which turns the recorder on: see clips.go.
	clipsTotal *esphome.Sensor
	clipsKinds *esphome.TextSensor
	keepClips  *esphome.Switch
	clips      *clips

	// send is where kept clips are uploaded to, and sender does it (clipsend.go). sendMu is held while the
	// settings are saved.
	send   sendEntities
	sender *sender
	sendMu sync.Mutex
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

// playingSlack is how much lower a threshold sits while the echo canceller is running, for the stop
// word and the other words, and for a wake word with no threshold of its own for playing.
const playingSlack = config.DefaultPlayingSlack

// slackFloor is as low as the slack is allowed to drag a threshold, whatever it started at.
const slackFloor = config.MinWakeThreshold

// thresholdFor is the cutoff a slot's score is judged against. base supplies the per-slot wake word
// threshold; stop is the stop word's own, which is configured separately. The wake words' own
// threshold for playing goes through thresholdWithPlayback.
//
// While the speaker plays and the canceller runs, what reaches the detector is the residual of the
// music plus the voice, and the word scores lower than it does in a quiet room. A little slack here
// is worth more than the false wakes it risks: the music is the reference the canceller has, so it
// is the one sound least able to fake the word.
//
// The stop word takes the same slack as every other slot. It needs it more than they do, not less:
// it is the one word said while the speaker is certainly playing, because playing is what it is
// asked to stop. Exempting it made the one word whose job is to interrupt a sound the only word
// judged with no allowance for the sound. Hang-up and command words are still governed by slackFor.
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

// thresholdWithPlayback changes only the two wake word slots. All other words keep their existing
// allowance and call/ring exceptions; canceling is still the existing AEC gate.
func thresholdWithPlayback(slot int, base func(int) float64, stop float64, canceling bool, playing func(int) float64) float64 {
	if canceling && slot >= 0 && slot < wakeword.Slots {
		return playing(slot)
	}
	return thresholdFor(slot, base, stop, canceling)
}

// slackFor reports whether a slot is judged with the allowance for the speaker (thresholdFor): while the
// canceller runs, except for the words that end a call, during one. A call keeps the canceller running
// even with the far end silent, so they would always be judged low, and a word heard by mistake ends the
// call. The stop word keeps the allowance over a ring, which it has to be heard over to stop.
//
// The command words (command.go) never get it. They are listened for only while the speaker plays, so
// the allowance would be off their thresholds all the time, and they act at once with no turn after them
// to show a mistake: the music they are said over is the one thing they must not take for the word.
func slackFor(slot int, canceling, onCall, ringing bool) bool {
	switch {
	case !canceling, isCommandSlot(slot):
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
	// Sized to reach the reserved indexes, the stop word's, the hang-up word's and the command words'.
	// The slots between them and Home Assistant's are never loaded, and an unloaded slot is one comparison
	// a frame.
	e := New(lastCommandSlot+1, mic.Get())

	// The hang-up and command words are judged as a wake word is, their own thresholds standing in for
	// the slot's.
	base := func(slot int) float64 {
		if slot == HangupSlot {
			return config.Get().Wake.Hangup.ThresholdAt()
		}
		if t, ok := commandThreshold(config.Get().Wake, slot); ok {
			return t
		}
		return wakeword.Threshold(slot)
	}
	slack := func(slot int) bool {
		// A call still waiting to be dialed has no far end in the speaker, only "Calling Mom.", so the
		// stop word keeps the slack it has over any announcement.
		onCall := phone.Get().Busy() && !phone.Get().State().Waiting
		return slackFor(slot, mic.Get().Canceling(), onCall, ring.IsSounding())
	}
	e.Threshold = func(slot int) float64 {
		return thresholdWithPlayback(slot, base, config.Get().Wake.Stop.Threshold, slack(slot), wakeword.PlayingThreshold)
	}
	// While the speaker's allowance is off its threshold, the stop word's near misses are logged from
	// under its cutoff, rather than from NearMiss, which is where its cutoff then sits (stop.go). The
	// engine asks in the same breath as it asks for the cutoff, when the word peaks.
	e.NearMissFloor = func(slot int, cutoff float64) float64 {
		return stopNearMissFloor(slot, cutoff, slack(slot))
	}

	// d is made below, once the hooks it chains onto are in place; a detection only arrives after Start.
	var d *Detect
	e.OnDetect = func(slot int) {
		c := config.Get()
		st := phone.Get().State()
		h := heard{
			phase:    st.Phase,
			talkBack: talkback.Get().Busy(),
			ringing:  ring.IsSounding(),
			stopHang: c.Wake.Hangup.StopWord(),
			callWake: c.Home.CallWake,
			farEnd:   phone.Get().FarEndTalking(),
			turnOpen: voice.Get().Busy(),
			waiting:  st.Waiting,
			takes:    takes.Get().Holding(),
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
		case routeCancelCall:
			slog.Info("stop or hang-up word before a call was dialed, canceling it", "slot", slot+1)
			phone.Get().Hangup()
		case routeEndRing:
			slog.Info("stop word during a call, ending the ring")
			ring.End()
		case routeCommand:
			d.commandHeard(slot)
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
			d.wakeHeard(slot)
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
	e.OnCandidateStart = func(slot int) {
		if phone.Get().Busy() || talkback.Get().Busy() || voice.Get().HomeAssistantHears(slot) {
			return
		}
		voice.Get().CandidateStarting(slot)
	}

	d = &Detect{engine: e, busy: newWakeBusy(led.Get().Busy(), e.Ready), base: base}
	d.ducker = newDucker()
	d.ducker.watch(e)
	d.stop = newStopEntity(d)
	d.stopWord = newStopWord(d)
	d.hangup = newHangup(d)
	// What the words scored during each call, said as it ends (#484). Read as it runs, as the hang-up
	// word's loading is: two changes close together may run out of order.
	calls := newCallScores(e)
	phone.Get().Changed.Listen(func(phone.State) {
		go calls.follow(phone.Get().State(), time.Now())
	})
	// And what the stop word scored over each reply, said as the turn ends (replyscores.go).
	followReplies(e, slog.Info)
	for _, k := range commandKinds {
		c := newCommand(d, k)
		c.watch()
		d.commands = append(d.commands, c)
	}
	d.score = newScoreEntity()
	d.wakeProblem = newWakeProblemEntity()
	d.onHA = newOnHAEntities()
	e.OnScored = func(slot int, peak, _ float64) { d.scored(slot, peak) }
	d.frames = newFrameEntities()
	e.OnFrames = d.frames.set
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
	d.send = newSendEntities(d)
	d.sender = newSender(d.clips)
	d.sender.publish = d.publishSend
	d.clips.changed = d.sender.wake
	d.clips.abortSend = d.sender.abortSend
	// A reboot the daemon makes itself (settings, an update into a slot) ends the process without its
	// stop, so the clips waiting in RAM, which the sender moves to the storage as it stops, would be lost:
	// they are moved first. Not for a reset, which deletes them with the rest.
	clips := d.clips
	bootreason.BeforeReboot(func() {
		if !bootreason.IsReset() {
			clips.spill(func(string, time.Time) bool { return true })
		}
	})

	// A cut microphone hands on silence, and running the models over it is work that cannot find
	// anything. The engines go down with the microphones and come back with them. Not for a panel's
	// microphone found silent (its own switch, or another app holding it): that shows as muted, but the
	// engines run on over the silence, so the word that ends it is heard rather than lost to a reload.
	// What decides is whether the device cut the microphones (CutHere), and the engines' own state says
	// whether they are coming back.
	mute.Get().Changed.Listen(func(muted bool) {
		cut := muted && mute.Get().CutHere()
		was := e.Quieted()
		e.Quiet(cut)
		// The stop word is spared while an alarm or timer rings: it has to stop when told, and the sound
		// could otherwise hold it for as long as ten seconds. With nothing ringing it settles too, or
		// the unmute sound could be heard as "stop" and cut itself short, and music with it.
		if was && !cut {
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
	e.OnDropped = d.dropped

	// The engine loads on every start, including a restart. Home Assistant only pushes a selection when
	// the user changes one, so an engine that came back empty would leave the device deaf while it went
	// on advertising wake words it was not listening for.
	e.Load = func() error {
		// The saved selection, not what was said last: a start that loaded nothing would otherwise be
		// what every later start loads too (#657).
		turn := voice.Get()
		turn.SetActiveWakeWords(d.load(voice.SavedWakeWords(d.library().Ours())))
		// The load above has usually put the stop word in already (loadFrom); quiet, so a start says
		// "stop word listening" once.
		d.followStop()
		// A call already up when the engine starts has its hang-up word, as one that starts later does.
		d.hangup.follow(hangupListens(phone.Get().State()))
		// Back in if music is playing: the start emptied their slots.
		for _, c := range d.commands {
			c.restarted()
		}
		// A device that was muted when it was switched off comes back muted, and the hook below only
		// fires on a change, so the state has to be read once here as well.
		if muted, err := mute.Get().Muted(); err == nil {
			e.Quiet(muted && mute.Get().CutHere())
		}
		return nil
	}

	voice.Get().OnWakeWord(d.load, d.selected)
	wakeword.Refresh = d.refresh
	wakeword.Restore = d.restore
	wakeword.ReadScores = d.Scores

	ours := wake.Lib().Ours()
	slog.Info("wake words installed", "count", len(ours),
		"openwakeword", len(wake.OfKind(ours, wake.KindOpenWakeWord)),
		"microwakeword", len(wake.OfKind(ours, wake.KindMicroWakeWord)))

	return d
}

// selected is a wake word selection saved. A selection both downloads models and lets go of the ones it
// replaced, so it is the one thing that moves either number the disk reports. Saved, it is also what
// the command words are kept apart from, looked at here rather than from load, which runs before the
// save.
func (d *Detect) selected() {
	diag.Get().Measure()
	for _, c := range d.commands {
		c.follow()
	}
	// The stop and hang-up words keep away from the wake words saved, as the command words do, and
	// loadFrom looked at them before this selection was saved: a word of one's own just chosen as a wake
	// word would otherwise go on listening as the stop word too until a restart (#741).
	if d.hangup != nil {
		d.hangup.refreshOptions()
		d.hangup.follow(hangupListens(phone.Get().State()))
	}
	if d.stopWord != nil {
		d.stopWord.refreshOptions()
		d.followStop()
	}
}

func (d *Detect) Name() string { return "wake" }

func (d *Detect) Start(ctx context.Context) error { return d.engine.Start(ctx) }

func (d *Detect) Run(ctx context.Context) error {
	// The clip sender runs for as long as the engine does, and is let go of with it.
	sctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	safe.Go("wake clip sender", func() {
		defer close(done)
		d.sender.run(sctx)
	})
	defer func() {
		cancel()
		<-done
	}()
	return d.engine.Run(ctx)
}

func (d *Detect) Close() error { return d.engine.Close() }

// Actions are the wake clips' (clips.go).
func (d *Detect) Actions() []*esphome.Action { return d.clips.actions() }

// load puts one wake word in each slot and reports the ids that came up. Whatever the engine refuses
// is left out, so Home Assistant reverts that slot rather than showing a wake word the device is not
// listening for.
func (d *Detect) load(ids []string) []string {
	d.loading.Lock()
	defer d.loading.Unlock()
	// A selection or a start is a fresh look, so a selection restore gave up on is worth trying again.
	d.refused = nil
	return d.loadLocked(ids)
}

// refusal is a saved selection the engine would not take all of, and the library's version then.
type refusal struct {
	want    []string
	version uint64
}

// restore loads the saved selection when what runs is not it (#657). Home Assistant asks what runs
// once per connection and believes the answer, so a slot that came up empty after an update read as "No
// wake word" there, and choosing it again was the only way back.
func (d *Detect) restore() {
	turn := voice.Get()
	saved := func() []string { return voice.SavedWakeWords(d.library().Ours()) }
	if accepted, ok := d.restoreFrom(saved, turn.ActiveWakeWords); ok {
		turn.SetActiveWakeWords(accepted)
	}
}

// restoreFrom is restore with the saved selection and what runs passed in, reporting what runs after it
// and whether it loaded anything.
//
// Home Assistant is waiting for the answer this comes before, and gives up after five seconds, so it
// never waits itself. Nothing is downloaded: the selection is resolved against what is on disk, and a
// word offered with a new model loads as it is, for the refresh after the answer to fetch. And a load
// or a refresh already running is left to finish rather than queued behind: either one ends by saying
// what runs, and the next request looks again.
func (d *Detect) restoreFrom(saved, running func() []string) ([]string, bool) {
	if d.refreshing.Load() || !d.loading.TryLock() {
		slog.Debug("wake words are loading, not restoring the saved selection over them")
		return nil, false
	}
	defer d.loading.Unlock()

	// Read under the lock, so a load that finished a moment ago is what is compared.
	want, now := saved(), running()
	if slices.Equal(want, now) {
		return nil, false
	}
	version := d.library().Version()
	if r := d.refused; r != nil && r.version == version && slices.Equal(r.want, want) {
		return nil, false
	}

	slog.Warn("wake words running are not the ones chosen, loading them again", "running", now, "chosen", want)
	accepted := d.loadFrom(want, d.library().OnDisk)
	d.refused = nil
	if !slices.Equal(accepted, want) {
		slog.Warn("the chosen wake words would not all load; not trying again until they or the models change",
			"chosen", want, "running", accepted)
		d.refused = &refusal{want: slices.Clone(want), version: version}
	}
	return accepted, true
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

// loadLocked loads a selection, downloading what it names that is offered and not here: a selection may
// name a model Home Assistant is offering but this device has never had, so the library is asked
// rather than a list captured at boot. This is where a new word arrives.
func (d *Detect) loadLocked(ids []string) []string { return d.loadFrom(ids, d.library().Ensure) }

// loadFrom is loadLocked with the models resolved by resolve: Ensure, which downloads, or OnDisk, which
// does not.
func (d *Detect) loadFrom(ids []string, resolve func([]string) []wake.Model) []string {
	d.asked = slices.Clone(ids)
	d.busy.begin()

	models := resolve(ids)

	var accepted []string
	var loaded []int
	var problems []string
	for slot := range wakeword.Slots {
		if slot >= len(ids) || ids[slot] == "" {
			d.engine.Clear(slot)
			continue
		}

		m, ok := wake.Find(models, ids[slot])
		if !ok {
			slog.Warn("unknown wake word", "slot", slot+1, "id", ids[slot])
			problems = append(problems, fmt.Sprintf("Wake word %d (%s): not on this device or offered by Home Assistant", slot+1, ids[slot]))
			d.engine.Clear(slot)
			continue
		}
		if err := d.engine.Use(slot, m); err != nil {
			slog.Error("loading the selected wake word failed", "slot", slot+1, "id", m.ID, "err", err)
			problems = append(problems, fmt.Sprintf("Wake word %d (%s): %s", slot+1, m.ID, wakeProblemText(err)))
			d.engine.Clear(slot)
			continue
		}
		accepted = append(accepted, m.ID)
		loaded = append(loaded, slot)
	}
	d.library().Loaded(accepted)
	d.setWakeProblem(problems)
	// A word running as a wake word now is not listened for as a command word too. A selection looks
	// again once it is saved (OnWakeWord); this covers a start and a refresh.
	for _, c := range d.commands {
		c.nudge()
	}

	// The hang-up word's list leaves the wake words out, and a word just chosen as one is no longer
	// listened for as the hang-up word.
	if d.hangup != nil {
		d.hangup.refreshOptions()
		d.hangup.follow(hangupListens(phone.Get().State()))
	}
	// And so does the stop word's: one chosen as a wake word now has the built-in word listen in its place.
	if d.stopWord != nil {
		d.stopWord.refreshOptions()
		d.followStop()
	}

	// Only the slots that took a wake word are waited on. A selection that loaded nothing has nothing to
	// warm up, so the ring goes back to what it was showing rather than animating for a device that is
	// not going to hear anything.
	d.busy.waitFor(loaded)
	return accepted
}

// scored is a word's peak, the Hold after it fired (Engine.OnScored). Last wake score, and the score in
// the turn the word opened, are only Home Assistant's wake words: the stop word, and any other word listened for past them, has its own threshold
// and says nothing about theirs. A command word's peak is for the vote between them (command.go).
func (d *Detect) scored(slot int, peak float64) {
	if slot < wakeword.Slots {
		d.score.Set(float32(peak))
		voice.Get().WakeScored(slot, peak)
	}
	if isCommandSlot(slot) {
		d.commandScored(slot, peak)
	}
}
