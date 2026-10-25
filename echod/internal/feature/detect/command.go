package detect

import (
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/detect/assets"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// Command words act on what is playing with no wake word before them (#191). There are three: the volume
// down word brings the music down, and with it down the wake word is heard again; the volume up word
// brings it up; and the next song word skips to the next song. Each is its own choice and listens in a
// slot of its own, only while music plays.

// CommandSlot is the first command word's, the volume down word's: past the stop word's slot and the one
// beside it, which the hang-up word has (#188), and as far from Home Assistant's. The other command words
// follow it.
const CommandSlot = StopSlot + 2

const (
	// VolumeUpSlot is the volume up word's.
	VolumeUpSlot = CommandSlot + 1

	// NextSongSlot is the next song word's.
	NextSongSlot = CommandSlot + 2

	// lastCommandSlot is the highest slot a command word listens in, the last the engine is sized for.
	lastCommandSlot = NextSongSlot
)

// commandNone is a command word's select switched off.
const commandNone = "None"

// CommandEvent is what Home Assistant's bus calls a command word acting, so an automation, or a client
// that is not Home Assistant, can follow what the device did on its own.
const CommandEvent = "esphome.techo5_command"

// volumeSteps is how far one "turn it down" or "turn it up" moves the level: five of the thirty steps,
// 6 to 8 dB near the top of a Show's dial, which is heard over loud music where one step of the buttons
// is not. Said again, it moves again.
const volumeSteps = 5

// commandHold is how long a command word stays loaded after the music stops. A client that plays one
// track at a time stops between every two of them, and a model loaded again comes back cold, so the word
// said as the next track starts would not be heard.
const commandHold = 10 * time.Second

// commandKind is one command word as the device knows it: the slot it listens in, its select and the
// name it goes by in the log, where its choice is saved, and what it does when it is heard. act returns
// what it did for the event, with "command" at least, and what to say in the log.
type commandKind struct {
	slot         int
	what         string // in the log: "volume down word"
	objectID     string
	name         string
	icon         string
	saved        func(config.Wake) config.CommandWord
	setID        func(string) error
	setThreshold func(float64) error
	act          func(*command) (data map[string]string, doing string)
}

var (
	volumeDown = &commandKind{
		slot: CommandSlot, what: "volume down word",
		objectID: "volume_down_word", name: "Volume down word", icon: "mdi:volume-minus",
		saved:        func(w config.Wake) config.CommandWord { return w.VolumeDown },
		setID:        func(v string) error { return config.Set().VolumeDown().ID(v) },
		setThreshold: func(v float64) error { return config.Set().VolumeDown().Threshold(v) },
		act: func(c *command) (map[string]string, string) {
			return c.turn("volume_down", -volumeSteps), "turning the music down"
		},
	}
	volumeUp = &commandKind{
		slot: VolumeUpSlot, what: "volume up word",
		objectID: "volume_up_word", name: "Volume up word", icon: "mdi:volume-plus",
		saved:        func(w config.Wake) config.CommandWord { return w.VolumeUp },
		setID:        func(v string) error { return config.Set().VolumeUp().ID(v) },
		setThreshold: func(v float64) error { return config.Set().VolumeUp().Threshold(v) },
		act: func(c *command) (map[string]string, string) {
			return c.turn("volume_up", volumeSteps), "turning the music up"
		},
	}
	nextSong = &commandKind{
		slot: NextSongSlot, what: "next song word",
		objectID: "next_song_word", name: "Next song word", icon: "mdi:skip-next",
		saved:        func(w config.Wake) config.CommandWord { return w.NextSong },
		setID:        func(v string) error { return config.Set().NextSong().ID(v) },
		setThreshold: func(v float64) error { return config.Set().NextSong().Threshold(v) },
		act:          func(c *command) (map[string]string, string) { return c.skip() },
	}
)

// commandKinds are the command words in slot order, which is also the order a model chosen for more than
// one of them is given to: it listens only as the first (wanted).
var commandKinds = []*commandKind{volumeDown, volumeUp, nextSong}

// isCommandSlot reports whether a slot is one of the command words'.
func isCommandSlot(slot int) bool { return slot >= CommandSlot && slot <= lastCommandSlot }

// commandKindAt is the command word that listens in a slot, or nil.
func commandKindAt(slot int) *commandKind {
	for _, k := range commandKinds {
		if k.slot == slot {
			return k
		}
	}
	return nil
}

// commandThreshold is the threshold the command word in slot is judged at, as w saves it, and whether the
// slot is a command word's at all.
func commandThreshold(w config.Wake, slot int) (float64, bool) {
	k := commandKindAt(slot)
	if k == nil {
		return 0, false
	}
	return k.saved(w).ThresholdAt(), true
}

// command is one command word: its select and threshold, its model loaded while music plays, and what
// it does when it is heard.
type command struct {
	kind      *commandKind
	d         *Detect
	word      *esphome.Select
	opts      *component.Options
	threshold *esphome.Number

	// models is what the device has, the same library the wake words come from, and wakeWords the ids
	// chosen or running as wake words. playing is whether the room's music is playing, muted whether the
	// speaker is muted, busy whether a turn is open and ringing whether an alarm or timer is; score is
	// what the word was heard at, adjust moves the level and reports where it went, reaches says whether
	// a skip would reach anyone and next asks for one, and fire tells the bus. now and after are the
	// clock. Tests stand in for all of them.
	models    func() []wake.Model
	wakeWords func() []string
	playing   func() bool
	muted     func() bool
	busy      func() bool
	ringing   func() bool
	score     func() float64
	adjust    func(delta int) int
	reaches   func() bool
	next      func()
	fire      func(data map[string]string)
	now       func() time.Time
	after     func(time.Duration, func())

	// mu keeps one look at a time. quietSince is when the music was last seen stopped, zero while it
	// plays, and waiting whether a look at the end of the hold is already due. loaded is the id this put
	// in the slot and has not taken out, and failed the id that would not load or that the engine took
	// out for failing on the audio, left out until something changes. All under mu.
	mu         sync.Mutex
	quietSince time.Time
	waiting    bool
	loaded     string
	failed     string

	// nudged folds a run of the player's changes into one look, and retry has the next look try a model
	// that failed again: it was chosen again, or the library changed under it.
	nudged atomic.Bool
	retry  atomic.Bool
}

func newCommand(d *Detect, k *commandKind) *command {
	c := &command{
		kind:      k,
		d:         d,
		models:    func() []wake.Model { return d.library().Ours() },
		wakeWords: d.wakeWordIDs,
		playing: func() bool {
			p := media.Get()
			playing, _ := p.ScreenState()
			return playing && !p.NoisePlaying()
		},
		muted:   func() bool { return media.Get().Muted() },
		busy:    func() bool { return voice.Get().Busy() },
		ringing: ring.IsSounding,
		score:   func() float64 { return d.engine.Crossing(k.slot) },
		adjust: func(delta int) int {
			p := media.Get()
			p.Adjust(delta)
			return p.Volume()
		},
		reaches: func() bool { return media.Get().SkipReaches() },
		next:    func() { media.Get().Transport(media.TransportNext) },
		fire: func(data map[string]string) {
			component.Fire.Emit(component.Event{Name: CommandEvent, Data: data})
		},
		now:   time.Now,
		after: func(d time.Duration, f func()) { time.AfterFunc(d, f) },
	}
	c.word = &esphome.Select{
		Base: esphome.Base{
			ObjectID: k.objectID,
			DeviceID: component.DeviceMicrophone,
			Name:     k.name,
			Icon:     k.icon,
			Category: esphome.CategoryConfig,
		},
		Options: []string{commandNone},
	}
	c.opts = component.NewOptions(c.word)
	c.word.OnCommand = func(v string) {
		id, ok := c.idFor(v)
		if !ok {
			// Home Assistant reads the options as it connects, so it can offer one the device has since
			// lost: the setup page has Home Assistant reconnect after its own changes, nothing else does.
			slog.Warn(k.what+": not a model the device has", "option", v)
			return
		}
		if err := k.setID(id); err != nil {
			slog.Error("saving the "+k.what+" failed", "err", err)
			return
		}
		c.word.Set(v)
		slog.Info("setting changed", "setting", c.word.ObjectID, "using", v)
		c.retry.Store(true)
		c.followAll()
	}
	c.threshold = &esphome.Number{
		Base: esphome.Base{
			ObjectID: k.objectID + "_threshold",
			DeviceID: component.DeviceMicrophone,
			Name:     k.name + " threshold",
			Icon:     "mdi:tune-vertical",
			Category: esphome.CategoryConfig,
		},
		Min: 0.5, Max: config.StopOff, Step: 0.01,
		Mode: esphome.NumberBox,
	}
	c.threshold.OnCommand = func(v float32) {
		if err := k.setThreshold(float64(v)); err != nil {
			slog.Error("saving the "+k.what+" threshold failed", "err", err)
			return
		}
		c.threshold.Set(v)
		slog.Info("setting changed", "setting", c.threshold.ObjectID, "using", v)
		// Loaded or dropped to match: at the top of the range it is off. The others look too, since one
		// that shares this one's model listens only while this one doesn't.
		c.followAll()
	}
	return c
}

// saved is this word's choice as the config holds it.
func (c *command) saved() config.CommandWord { return c.kind.saved(config.Get().Wake) }

// watch has the word follow the music, and its select the models the device has. Listeners must not
// block, and a load reads a model off disk, so both go elsewhere.
func (c *command) watch() {
	media.Get().OnNowPlaying.Listen(func(struct{}) { c.nudge() })
	wake.Lib().Changed.Listen(func(struct{}) {
		c.retry.Store(true)
		c.nudge()
	})
}

// nudge has follow look again, on a goroutine of its own. Changes that arrive while a look is waiting to
// start are that look: the player says something may have changed many times over for one change.
func (c *command) nudge() {
	if c == nil || !c.nudged.CompareAndSwap(false, true) {
		return
	}
	safe.Go(c.kind.what, func() {
		c.nudged.Store(false)
		c.follow()
	})
}

// restarted is the engine started again with every slot empty, this word's too, and its look after: the
// slot is filled again if music plays, and a model that failed before is tried again, as the wake words
// are.
func (c *command) restarted() {
	c.mu.Lock()
	c.loaded, c.failed = "", ""
	c.mu.Unlock()
	c.follow()
}

// dropped is the engine having taken the word's model out for failing on the audio (Engine.OnDropped).
// It stays out until it is chosen again or the library changes, as a wake word's slot does.
func (c *command) dropped(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id != c.loaded {
		return
	}
	// A look that ran between the engine emptying the slot and this put the model back: its next failure
	// says so again.
	if _, in := c.d.engine.Loaded(c.kind.slot); in {
		return
	}
	c.loaded, c.failed = "", id
	slog.Error("the "+c.kind.what+"'s model failed and is left out until it is chosen again", "id", id)
}

// wakeWordIDs are the wake words chosen and the ones running: a device never set up runs its default
// word with nothing saved, and a selection is loaded before it is saved.
func (d *Detect) wakeWordIDs() []string {
	ids := chosenWakeWords()
	for slot := range wakeword.Slots {
		if m, ok := d.engine.Loaded(slot); ok && !slices.Contains(ids, m.ID) {
			ids = append(ids, m.ID)
		}
	}
	return ids
}

// commandAt is the command word that listens in a slot, or nil.
func (d *Detect) commandAt(slot int) *command {
	for _, c := range d.commands {
		if c.kind.slot == slot {
			return c
		}
	}
	return nil
}

// followAll has this word look again, then the other command words: a choice or a threshold changing
// one can move another, which may share its model. The ones that now have to let go of a model do so
// first, so that a word taking one from another never has it in two slots at once, heard twice.
func (c *command) followAll() {
	all := c.d.commands
	if !slices.Contains(all, c) {
		all = append([]*command{c}, all...)
	}
	for _, o := range all {
		o.letGo()
	}
	c.follow()
	for _, o := range all {
		if o != c {
			o.follow()
		}
	}
}

// letGo takes the word's model out of its slot if it no longer wants it there (wanted). Putting one in is
// follow's: this only ever empties a slot.
func (c *command) letGo() {
	c.mu.Lock()
	defer c.mu.Unlock()
	have, loaded := c.d.engine.Loaded(c.kind.slot)
	if !loaded {
		return
	}
	if want, why := c.wanted(); why != "" || want.ID != have.ID {
		if why == "" {
			why = "another model chosen"
		}
		c.d.engine.Clear(c.kind.slot)
		c.loaded = ""
		slog.Info(c.kind.what+" off", "why", why)
	}
}

// commandDecide is how long the command words heard together wait, from the latest of them to cross,
// before the surest acts: the Hold that finds its peak (OnScored), the next fresh score after it (an
// openWakeWord model scores every 80 ms), and room for the goroutines between.
const commandDecide = Hold + 200*time.Millisecond

// commandVote is the command words heard together, while more than one listens. One thing said can
// cross two of them (a "turn it down" the volume up word scores over its own threshold too), so they wait
// until each has peaked, and only the one that peaked highest acts. Peaks rather than crossings: each
// word crosses at its own threshold, which can be set lower for one than another, and the wrong one
// tends to cross first, on the words they share. Each word that joins puts the count back to
// commandDecide after its own crossing, so its peak is in before it.
type commandVote struct {
	mu    sync.Mutex
	peaks map[int]float64 // slot: its peak, or what it crossed at until its hold ends; nil with none open
	gen   int             // which count is the latest: an earlier one finds it isn't and counts nothing
}

// commandHeard is a command word heard in slot (routeCommand). With no other command word listening
// there is nothing to mistake it for, and it acts at once.
func (d *Detect) commandHeard(slot int) {
	c := d.commandAt(slot)
	if c == nil {
		return
	}
	if d.commandsListening() < 2 {
		c.heard()
		return
	}
	v := &d.vote
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.peaks == nil {
		v.peaks = map[int]float64{}
	}
	if _, in := v.peaks[slot]; in {
		return
	}
	v.peaks[slot] = c.score()
	v.gen++
	gen := v.gen
	c.after(commandDecide, func() { d.commandDecided(gen) })
}

// commandScored is a command word's peak, the Hold after it crossed (Engine.OnScored), for the vote.
func (d *Detect) commandScored(slot int, peak float64) {
	v := &d.vote
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, in := v.peaks[slot]; in {
		v.peaks[slot] = peak
	}
}

// commandDecided counts the vote, unless a word joined it since: the word that peaked highest acts (on a
// tie, the earlier slot), and the others are only logged.
func (d *Detect) commandDecided(gen int) {
	v := &d.vote
	v.mu.Lock()
	if gen != v.gen || v.peaks == nil {
		v.mu.Unlock()
		return
	}
	peaks := v.peaks
	v.peaks = nil
	v.mu.Unlock()
	best, top := -1, 0.0
	for slot, p := range peaks {
		if best < 0 || p > top || p == top && slot < best {
			best, top = slot, p
		}
	}
	c := d.commandAt(best)
	if c == nil {
		return
	}
	for slot, p := range peaks {
		if o := d.commandAt(slot); o != nil && slot != best {
			slog.Info(o.kind.what+" heard with the "+c.kind.what+", left to it", "peak", p, "acting_peak", top)
		}
	}
	c.heard()
}

// commandsListening is how many command words have their model in the engine.
func (d *Detect) commandsListening() int {
	n := 0
	for _, c := range d.commands {
		if _, in := d.engine.Loaded(c.kind.slot); in {
			n++
		}
	}
	return n
}

// commandDropped is the engine having taken a command word's model out of slot for failing on the audio
// (Engine.OnDropped): only that word's.
func (d *Detect) commandDropped(slot int, id string) {
	if c := d.commandAt(slot); c != nil {
		c.dropped(id)
	}
}

// restoreCommands publishes each command word's saved choice and threshold (Restore).
func (d *Detect) restoreCommands(w config.Wake) {
	for _, c := range d.commands {
		c.restore(c.kind.saved(w))
	}
}

// offered are the models the select offers, each phrase once, in the library's order. Left out: one
// called "Stop", which could not be told from the stop word in the list. The wake words stay in it, so
// the list changes only with the library: one chosen as both is off as the command word while it is a
// wake word (isWakeWord).
func (c *command) offered() []wake.Model {
	var out []wake.Model
	for _, m := range c.models() {
		if m.Phrase == assets.StopPhrase || slices.ContainsFunc(out, func(o wake.Model) bool { return o.Phrase == m.Phrase }) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// samePhrase reports whether the model saved as id is m, by id or by phrase: two models of one phrase are
// one word to whoever says it.
func (c *command) samePhrase(id string, m wake.Model) bool {
	if id == m.ID {
		return true
	}
	o, ok := wake.Find(c.models(), id)
	return ok && o.Phrase == m.Phrase
}

// isWakeWord reports whether m runs or is chosen as a wake word, by id or by phrase. Heard as both, it
// would act on the music and open a turn at once.
func (c *command) isWakeWord(m wake.Model) bool {
	return slices.ContainsFunc(c.wakeWords(), func(id string) bool { return c.samePhrase(id, m) })
}

// takenBy is the command word before this one that m is chosen as too, or nil: heard as both, one word
// would turn the music down and up at once, so it listens only as the first.
func (c *command) takenBy(m wake.Model) *commandKind {
	w := config.Get().Wake
	for _, k := range commandKinds {
		if k.slot >= c.kind.slot {
			break
		}
		if o := k.saved(w); o.On() && c.samePhrase(o.ID, m) {
			return k
		}
	}
	return nil
}

// options are None and the offered models by phrase.
func (c *command) options() []string {
	out := []string{commandNone}
	for _, m := range c.offered() {
		out = append(out, m.Phrase)
	}
	return out
}

// idFor is what a select option is saved as, and whether it names anything: empty for None, an offered
// model's id otherwise.
func (c *command) idFor(phrase string) (string, bool) {
	if phrase == commandNone {
		return "", true
	}
	for _, m := range c.offered() {
		if m.Phrase == phrase {
			return m.ID, true
		}
	}
	return "", false
}

// phraseFor is the select option for what was saved. A model the device no longer has shows as None,
// which is what the device then does.
func (c *command) phraseFor(id string) string {
	if id == "" {
		return commandNone
	}
	if m, ok := wake.Find(c.models(), id); ok {
		return m.Phrase
	}
	return commandNone
}

func (c *command) restore(w config.CommandWord) {
	c.opts.Restore(c.options())
	c.word.Set(c.phraseFor(w.ID))
	c.threshold.Set(float32(w.ThresholdAt()))
	slog.Info("restored", "what", c.word.ObjectID, "using", c.phraseFor(w.ID), "threshold", w.ThresholdAt())
}

// wanted is the model the slot should hold while music plays, or why there is none. A word that is a
// wake word too keeps its choice, and listens again once it is not; so does one chosen for an earlier
// command word as well. So does one the device no longer has, as the hang-up word does: the select shows
// None, and the saved choice is not written over, so a library that could not be read for a moment does
// not lose it, and the model sent again brings it back.
func (c *command) wanted() (m wake.Model, why string) {
	w := c.saved()
	switch {
	case w.ID == "":
		return wake.Model{}, "none chosen"
	case !w.On():
		return wake.Model{}, "threshold at the top of its range"
	case w.ID == c.failed:
		return wake.Model{}, "its model failed"
	}
	m, ok := wake.Find(c.models(), w.ID)
	switch {
	case !ok:
		return wake.Model{}, "it is not on the device"
	case c.isWakeWord(m):
		return wake.Model{}, "it is a wake word too"
	}
	if k := c.takenBy(m); k != nil {
		return wake.Model{}, "it is the " + k.what + " too"
	}
	return m, ""
}

// follow puts the chosen model in the slot while music plays, and takes it out once none has played for
// commandHold. What the engine holds is asked each time rather than remembered: a restart empties every
// slot, and a model that fails on live audio is taken out by the engine.
func (c *command) follow() {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot := c.kind.slot
	if c.retry.Swap(false) {
		c.failed = ""
	}
	have, loaded := c.d.engine.Loaded(slot)

	now, playing := c.now(), c.playing()
	switch {
	case playing:
		c.quietSince = time.Time{}
	case c.quietSince.IsZero():
		c.quietSince = now
	}

	want, why := c.wanted()
	c.opts.Set(c.options())
	if v := c.phraseFor(c.saved().ID); c.word.Get() != v {
		c.word.Set(v)
	}

	in := loaded && why == "" && have.ID == want.ID

	// Kept through the hold only if it is in already: a word that was not listening when the music
	// stopped is not loaded for the gap.
	left := commandHold - now.Sub(c.quietSince)
	holding := !playing && in && left > 0
	if holding && !c.waiting {
		c.waiting = true
		c.after(left, func() {
			c.mu.Lock()
			c.waiting = false
			c.mu.Unlock()
			c.follow()
		})
	}

	switch {
	case why != "":
		if loaded {
			c.d.engine.Clear(slot)
			c.loaded = ""
			slog.Info(c.kind.what+" off", "why", why)
		}
	case !playing && !holding:
		if loaded {
			c.d.engine.Clear(slot)
			c.loaded = ""
			slog.Info(c.kind.what + " resting: no music is playing")
		}
	case in:
	default:
		if err := c.d.engine.Use(slot, want); err != nil {
			slog.Error("loading the "+c.kind.what+" failed; it is left out until it is chosen again",
				"id", want.ID, "err", err)
			c.d.engine.Clear(slot)
			c.loaded, c.failed = "", want.ID
			return
		}
		c.loaded = want.ID
		slog.Info(c.kind.what+" listening", "id", want.ID, "threshold", c.saved().ThresholdAt())
	}
}

// heard is the word heard, with nobody else holding the microphones (OnDetect has seen to a call and a
// talk through a camera). A turn has them too: the music is already down for it, and what is said is the
// turn's. Over a ring the stop word is what is wanted, and with no music to be heard, in the gap after a
// track or with the speaker muted, there is nothing to act on: Adjust would unmute it, and a skip nobody
// can hear is not what was asked for.
func (c *command) heard() {
	k := c.kind
	switch {
	case c.busy():
		slog.Info(k.what + " ignored: a turn is open")
		return
	case c.ringing():
		slog.Info(k.what + " ignored: an alarm or timer is ringing")
		return
	case !c.playing() || c.muted():
		slog.Info(k.what + " ignored: no music to be heard")
		return
	}
	score := c.score()
	word := c.saved().ID
	data, doing := k.act(c)
	attrs := []any{"word", word, "score", score}
	for _, key := range []string{"volume", "asked"} {
		if v, ok := data[key]; ok {
			attrs = append(attrs, key, v)
		}
	}
	slog.Info(k.what+", "+doing, attrs...)
	data["word"] = word
	data["score"] = strconv.FormatFloat(score, 'f', 2, 64)
	data["device"] = config.Get().Device.Name
	c.fire(data)
}

// turn moves the level by steps, for the volume down and volume up words, and says where it went.
func (c *command) turn(name string, steps int) map[string]string {
	level := c.adjust(steps)
	return map[string]string{"command": name, "volume": strconv.Itoa(level)}
}

// skip asks for the next song, for the next song word, when there is somebody to ask (Player.SkipReaches:
// Music Assistant, or a phone over AirPlay or Bluetooth), and says in the event whether it did. A client
// that sends the device one track at a time (Home Assistant and others), the device's own stations and a
// sender that can't be reached have nothing here to ask: the event, with asked false, is the skip, for
// whoever plays the music to make.
func (c *command) skip() (map[string]string, string) {
	if !c.reaches() {
		return map[string]string{"command": "next", "asked": "false"}, "nothing here to ask for the next song"
	}
	c.next()
	return map[string]string{"command": "next", "asked": "true"}, "asking for the next song"
}
