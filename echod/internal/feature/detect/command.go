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

// Command words act on what is playing with no wake word before them (#191). There is one so far, the
// volume down word: said over music too loud for the wake word to be heard, it brings the music down,
// and with it down the wake word is heard again.

// CommandSlot is where the volume down word listens: past the stop word's slot and the one beside it,
// which a hang-up word is to have (#188), and as far from Home Assistant's.
const CommandSlot = StopSlot + 2

// commandNone is the volume down word's select switched off.
const commandNone = "None"

// CommandEvent is what Home Assistant's bus calls a command word acting, so an automation, or a client
// that is not Home Assistant, can follow what the device did on its own.
const CommandEvent = "esphome.techo5_command"

// volumeDownSteps is how far one "turn it down" takes the level: five of the thirty steps, 6 to 8 dB
// near the top of a Show's dial, which is heard over loud music where one step of the buttons is not.
// Said again, it goes down again.
const volumeDownSteps = 5

// commandHold is how long the word stays loaded after the music stops. A client that plays one track
// at a time stops between every two of them, and a model loaded again comes back cold, so the word said
// as the next track starts would not be heard.
const commandHold = 10 * time.Second

// command is the volume down word: its select and threshold, its model loaded while music plays, and
// what it does when it is heard.
type command struct {
	d         *Detect
	word      *esphome.Select
	opts      *component.Options
	threshold *esphome.Number

	// models is what the device has, the same library the wake words come from, and wakeWords the ids
	// chosen or running as wake words. playing is whether the room's music is playing, muted whether the
	// speaker is muted, busy whether a turn is open and ringing whether an alarm or timer is; score is
	// what the word was heard at, adjust moves the level and reports where it went, and fire tells the
	// bus. now and after are the clock. Tests stand in for all of them.
	models    func() []wake.Model
	wakeWords func() []string
	playing   func() bool
	muted     func() bool
	busy      func() bool
	ringing   func() bool
	score     func() float64
	adjust    func(delta int) int
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

func newCommand(d *Detect) *command {
	c := &command{
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
		score:   func() float64 { return d.engine.Crossing(CommandSlot) },
		adjust: func(delta int) int {
			p := media.Get()
			p.Adjust(delta)
			return p.Volume()
		},
		fire: func(data map[string]string) {
			component.Fire.Emit(component.Event{Name: CommandEvent, Data: data})
		},
		now:   time.Now,
		after: func(d time.Duration, f func()) { time.AfterFunc(d, f) },
	}
	c.word = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "volume_down_word",
			DeviceID: component.DeviceMicrophone,
			Name:     "Volume down word",
			Icon:     "mdi:volume-minus",
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
			slog.Warn("volume down word: not a model the device has", "option", v)
			return
		}
		if err := config.Set().VolumeDown().ID(id); err != nil {
			slog.Error("saving the volume down word failed", "err", err)
			return
		}
		c.word.Set(v)
		slog.Info("setting changed", "setting", c.word.ObjectID, "using", v)
		c.retry.Store(true)
		c.follow()
	}
	c.threshold = &esphome.Number{
		Base: esphome.Base{
			ObjectID: "volume_down_word_threshold",
			DeviceID: component.DeviceMicrophone,
			Name:     "Volume down word threshold",
			Icon:     "mdi:tune-vertical",
			Category: esphome.CategoryConfig,
		},
		Min: 0.5, Max: config.StopOff, Step: 0.01,
		Mode: esphome.NumberBox,
	}
	c.threshold.OnCommand = func(v float32) {
		if err := config.Set().VolumeDown().Threshold(float64(v)); err != nil {
			slog.Error("saving the volume down word threshold failed", "err", err)
			return
		}
		c.threshold.Set(v)
		slog.Info("setting changed", "setting", c.threshold.ObjectID, "using", v)
		// Loaded or dropped to match: at the top of the range it is off.
		c.follow()
	}
	return c
}

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
	safe.Go("volume down word", func() {
		c.nudged.Store(false)
		c.follow()
	})
}

// restarted is the engine started again with every slot empty, the volume down word's too, and its
// look after: the slot is filled again if music plays, and a model that failed before is tried again,
// as the wake words are.
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
	if _, in := c.d.engine.Loaded(CommandSlot); in {
		return
	}
	c.loaded, c.failed = "", id
	slog.Error("the volume down word's model failed and is left out until it is chosen again", "id", id)
}

// chosenWakeWords are the ids of the wake words chosen in Home Assistant's slots.
func chosenWakeWords() []string {
	var ids []string
	for _, w := range config.Get().Wake.Slots(wakeword.Slots) {
		if w.ID != "" {
			ids = append(ids, w.ID)
		}
	}
	return ids
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

// offered are the models the select offers, each phrase once, in the library's order. Left out: one
// called "Stop", which could not be told from the stop word in the list. The wake words stay in it, so
// the list changes only with the library: one chosen as both is off as the volume down word while it is
// a wake word (isWakeWord).
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

// isWakeWord reports whether m runs or is chosen as a wake word, by id or by phrase: two models of one
// phrase are one word to whoever says it. Heard as both, it would turn the music down and open a turn at
// once.
func (c *command) isWakeWord(m wake.Model) bool {
	wakeWords := c.wakeWords()
	if slices.Contains(wakeWords, m.ID) {
		return true
	}
	return slices.ContainsFunc(c.models(), func(o wake.Model) bool {
		return o.Phrase == m.Phrase && slices.Contains(wakeWords, o.ID)
	})
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

// wanted is the model the slot should hold while music plays, or why there is none. drop says the choice
// is to go back to None: the device no longer has the model (Purge cache and the setup page leave it
// alone, so it was taken by hand). A word that is a wake word too keeps its choice, and listens again
// once it is not.
func (c *command) wanted() (m wake.Model, why string, drop bool) {
	w := config.Get().Wake.VolumeDown
	switch {
	case w.ID == "":
		return wake.Model{}, "none chosen", false
	case !w.On():
		return wake.Model{}, "threshold at the top of its range", false
	case w.ID == c.failed:
		return wake.Model{}, "its model failed", false
	}
	m, ok := wake.Find(c.models(), w.ID)
	switch {
	case !ok:
		return wake.Model{}, "it is not on the device", true
	case c.isWakeWord(m):
		return wake.Model{}, "it is a wake word too", false
	}
	return m, "", false
}

// follow puts the chosen model in the slot while music plays, and takes it out once none has played for
// commandHold. What the engine holds is asked each time rather than remembered: a restart empties every
// slot, and a model that fails on live audio is taken out by the engine.
func (c *command) follow() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.retry.Swap(false) {
		c.failed = ""
	}
	have, loaded := c.d.engine.Loaded(CommandSlot)

	now, playing := c.now(), c.playing()
	switch {
	case playing:
		c.quietSince = time.Time{}
	case c.quietSince.IsZero():
		c.quietSince = now
	}

	want, why, drop := c.wanted()
	if drop {
		slog.Warn("volume down word back to None", "why", why, "id", config.Get().Wake.VolumeDown.ID)
		if err := config.Set().VolumeDown().ID(""); err != nil {
			slog.Error("saving the volume down word failed", "err", err)
		}
	}
	c.opts.Set(c.options())
	if v := c.phraseFor(config.Get().Wake.VolumeDown.ID); c.word.Get() != v {
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
			c.d.engine.Clear(CommandSlot)
			c.loaded = ""
			slog.Info("volume down word off", "why", why)
		}
	case !playing && !holding:
		if loaded {
			c.d.engine.Clear(CommandSlot)
			c.loaded = ""
			slog.Info("volume down word resting: no music is playing")
		}
	case in:
	default:
		if err := c.d.engine.Use(CommandSlot, want); err != nil {
			slog.Error("loading the volume down word failed; it is left out until it is chosen again",
				"id", want.ID, "err", err)
			c.d.engine.Clear(CommandSlot)
			c.loaded, c.failed = "", want.ID
			return
		}
		c.loaded = want.ID
		slog.Info("volume down word listening", "id", want.ID,
			"threshold", config.Get().Wake.VolumeDown.ThresholdAt())
	}
}

// heard is the volume down word heard, with nobody else holding the microphones (OnDetect has seen to a
// call and a talk through a camera). A turn has them too: the music is already down for it, and what is
// said is the turn's. Over a ring the stop word is what is wanted, and with no music to be heard, in the
// gap after a track or with the speaker muted, there is nothing to turn down: Adjust would unmute it.
func (c *command) heard() {
	switch {
	case c.busy():
		slog.Info("volume down word ignored: a turn is open")
		return
	case c.ringing():
		slog.Info("volume down word ignored: an alarm or timer is ringing")
		return
	case !c.playing() || c.muted():
		slog.Info("volume down word ignored: no music to be heard")
		return
	}
	score := c.score()
	level := c.adjust(-volumeDownSteps)
	word := config.Get().Wake.VolumeDown.ID
	slog.Info("volume down word, turning the music down", "word", word, "score", score,
		"steps", volumeDownSteps, "volume", level)
	c.fire(map[string]string{
		"command": "volume_down",
		"word":    word,
		"score":   strconv.FormatFloat(score, 'f', 2, 64),
		"volume":  strconv.Itoa(level),
		"device":  config.Get().Device.Name,
	})
}
