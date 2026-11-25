package detect

import (
	"fmt"
	"log/slog"
	"math"
	"strings"
	"unicode/utf8"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/detect/assets"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// StopSlot is where the stop word listens. Well above Home Assistant's slots, which stop at two only
// because its interface does — #74 is about running more than that, and a reserved index next to them
// would be in the way of it.
const StopSlot = 100

// While the speaker's allowance is off its threshold, the stop word's near miss floor follows its
// cutoff, stopNearMissBelow under it and never under stopNearMissLeast (stopNearMissFloor).
const (
	stopNearMissBelow = 0.15
	stopNearMissLeast = 0.30
)

// stopNearMissFloor is the lowest peak of the stop word worth a "wake near miss" line, against the
// cutoff in force, with slack saying whether that cutoff has the allowance for the speaker taken off
// (slackFor); every other slot, and the stop word without the allowance, keeps NearMiss.
//
// While the speaker plays and the echo canceller runs, the stop word's cutoff is its threshold less the
// playing allowance, and no lower than 0.50 (thresholdFor): with a threshold anywhere from 0.50 to 0.60
// that is 0.50, which is NearMiss itself. A "stop" said over a reply that scored anything short of the
// cutoff was then under the floor too, and left no line at all, so a word the device all but heard and
// one it never heard read the same in the log, at every threshold an owner was likely to try for it.
// The stop word is the one word always said over the device's own sound, and how close it comes there
// is what says whether a lower threshold, the volume or the canceller is the thing to change.
//
// A floor that follows the cutoff gives those tries a line (slot=101, with the floor in it). It is
// for the log and the setup page's list only: the engine still tells the ducker, the ring's hush and
// the wake clips about a near miss only from NearMiss up, so nothing the device does changes with it.
//
// It is only for while the allowance is off. In a quiet room the cutoff is the threshold itself, and
// a floor under 0.5 there would let the television or a conversation the stop model half likes write
// a line every NearMissSettle for as long as it goes on, pushing the wake words' own detections and
// near misses out of the setup page's list and the diagnostics bundle; a reply, at least, ends.
//
// The floor is rounded to hundredths. A threshold set from Home Assistant comes as a float32, 0.55 as
// 0.550000011920929, and so does a cutoff made from it: unrounded, the floor would read
// floor=0.40000001192092896 in the log, and a cutoff a hair under 0.65 would put it a hair under
// NearMiss, with a floor in lines that ought to read as they always have.
func stopNearMissFloor(slot int, cutoff float64, slack bool) float64 {
	if slot != StopSlot || !slack {
		return NearMiss
	}
	return min(NearMiss, max(math.Round((cutoff-stopNearMissBelow)*100)/100, stopNearMissLeast))
}

// stopModel describes the embedded word to the engine. The numbers come from the manifest it ships
// with, and the cutoff is deliberately unreachable: the engine applies the user's threshold itself, and
// a detector that decided for itself would have to be rebuilt to change it, losing the streaming state
// a model needs to score well.
func stopModel() (wake.Model, error) {
	path, err := assets.Stop(layout.StateDir)
	if err != nil {
		return wake.Model{}, err
	}

	m := wake.Model{
		ID:     "stop",
		Phrase: assets.StopPhrase,
		Kind:   wake.KindMicroWakeWord,
		Path:   path,
	}
	m.Config.ModelPath = path
	m.Config.SlidingWindowSize = assets.StopWindowSize
	m.Config.FeaturesStepMs = assets.StopFeatureStep
	return m, nil
}

// loadStop puts the stop word in, or takes it out when it is switched off.
//
// Off means unloaded rather than ignored. A microWakeWord model carries its own feature front end, so
// one costs a front end per frame however loud the room is — unlike a second openWakeWord word, which
// is only another classifier over a front end already running. Leaving it loaded and never acting on it
// would be paying for the feature while it is turned off.
//
// The model is the one chosen in Stop word, the built-in one unless another was (stopword.go).
func (d *Detect) loadStop() { d.putStop(false) }

// followStop is loadStop after the library or the wake words changed, which happens far more often
// than the stop word does: a slot that already holds the word it should is left without a line.
func (d *Detect) followStop() { d.putStop(true) }

func (d *Detect) putStop(quiet bool) {
	// Called from the threshold, the select, the library changing and the engine's load, any of which can
	// come together: one at a time, so the last one's model is the one left in the slot.
	d.stopMu.Lock()
	defer d.stopMu.Unlock()

	if !config.Get().Wake.Stop.Listening() {
		if _, loaded := d.engine.Loaded(StopSlot); quiet && !loaded {
			return
		}
		d.engine.Clear(StopSlot)
		slog.Info("stop word off", "threshold", config.Get().Wake.Stop.Threshold)
		return
	}

	var m wake.Model
	var err error
	if d.stopWord != nil {
		m, err = d.stopWord.wanted()
	} else {
		m, err = builtInStop()
	}
	if err != nil {
		slog.Error("the stop word is unavailable", "err", err)
		d.engine.Clear(StopSlot)
		return
	}
	// Use keeps a slot that already holds a model of the same id as it is. A model of one's own could
	// have the built-in one's id, so a slot holding another file is emptied first.
	if have, loaded := d.engine.Loaded(StopSlot); loaded && have.Path != m.Path {
		d.engine.Clear(StopSlot)
	} else if loaded && quiet && have.ID == m.ID {
		return
	}
	if err := d.engine.Use(StopSlot, m); err != nil {
		slog.Error("loading the stop word failed", "id", m.ID, "err", err)
		d.engine.Clear(StopSlot)
		return
	}
	slog.Info("stop word listening", "id", m.ID, "phrase", m.Phrase, "threshold", config.Get().Wake.Stop.Threshold)
}

// newStopEntity is the one control the stop word has. There is no switch beside it: the top of the range
// is off, because a threshold no score can reach is the same thing said once instead of twice.
func newStopEntity(d *Detect) *esphome.Number {
	n := &esphome.Number{
		Base: esphome.Base{
			ObjectID: "stop_word_sensitivity",
			DeviceID: component.DeviceMicrophone,
			Name:     "Stop word threshold",
			Icon:     "mdi:hand-back-left",
			Category: esphome.CategoryConfig,
		},
		Min: 0.5, Max: config.StopOff, Step: 0.01,
		Mode: esphome.NumberBox,
	}

	n.OnCommand = func(v float32) {
		n.Set(v)

		if err := config.Set().Stop().Threshold(float64(v)); err != nil {
			slog.Error("saving the stop threshold failed", "err", err)
			return
		}
		// Loaded or dropped to match, so turning it off gives the frame time back rather than only
		// suppressing what it finds.
		d.loadStop()
	}
	return n
}

func (d *Detect) Entities() []esphome.Entity {
	out := []esphome.Entity{d.stop}
	if d.stopWord != nil {
		out = append(out, d.stopWord.word)
	}
	out = append(out, d.hangup.word, d.hangup.threshold)
	for _, c := range d.commands {
		out = append(out, c.word, c.threshold)
	}
	out = append(out, d.frames.busy, d.frames.rate, d.frames.worst)
	return append(out, d.score, d.wakeProblem, d.onHA.stream, d.onHA.confirm, d.onHA.threshold, d.onHA.faint, d.clipsTotal, d.clipsKinds,
		d.keepClips, d.send.to, d.send.unsent, d.send.status)
}

// newScoreEntity is the highest score the last wake reached. Set beside what was heard, it is how a
// threshold is set by evidence rather than by guessing: a wake by mistake that scored 0.91 is gone at
// 0.93, and a real one that scored 0.99 says there is room to go higher still.
func newScoreEntity() *esphome.Sensor {
	return &esphome.Sensor{
		Base: esphome.Base{
			ObjectID: "last_wake_score",
			Name:     "Last wake score",
			Icon:     "mdi:gauge",
			Category: esphome.CategoryDiagnostic,
		},
		Decimals: 2,
	}
}

// newWakeProblemEntity is Wake word problem: why a wake word chosen in Home Assistant is not
// listening. A model the device refuses (one too big for it, or with an operation its engine does not
// run) leaves the slot with no wake word, and Home Assistant shows only that; the reason is here.
func newWakeProblemEntity() *esphome.TextSensor {
	return &esphome.TextSensor{Base: esphome.Base{
		ObjectID: "wake_word_problem", Name: "Wake word problem", Icon: "mdi:alert-circle-outline",
		Category: esphome.CategoryDiagnostic,
	}}
}

// wakeProblemText is a load error as Wake word problem says it: the reason, without the path the
// engine names the model by, which means nothing in Home Assistant.
func wakeProblemText(err error) string {
	msg := err.Error()
	if _, after, ok := strings.Cut(msg, ".tflite: "); ok && strings.HasPrefix(msg, "wake: loading ") {
		return after
	}
	return msg
}

// setWakeProblem publishes what loadFrom found: each slot whose word is not listening and why, or None.
func (d *Detect) setWakeProblem(problems []string) {
	if d.wakeProblem == nil {
		return
	}
	if len(problems) == 0 {
		d.wakeProblem.Set("None")
		return
	}
	// A text sensor's state is at most 255 characters in Home Assistant.
	s := strings.Join(problems, "; ")
	if len(s) > 255 {
		// Cut on a character's boundary: half a character is not valid UTF-8, and the state would not
		// be sent at all.
		cut := 252
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "..."
	}
	d.wakeProblem.Set(s)
}

// Restore only publishes the value. Loading the model is Start's, through the engine's Load, so that a
// restart and a first boot take the same path.
func (d *Detect) Restore(c config.Config) {
	d.stop.Set(float32(c.Wake.Stop.Threshold))
	slog.Info("restored", "what", d.stop.ObjectID, "using", c.Wake.Stop.Threshold)
	if d.stopWord != nil {
		d.stopWord.restore(c.Wake.Stop)
	}
	d.hangup.restore(c.Wake.Hangup)
	d.restoreCommands(c.Wake)
	d.onHA.restore(c.Wake.OnHA)
	// An off that never reached the state wins over it (clipsoff.go).
	keep := d.keepClipsAtStart(c.Wake.KeepClips)
	d.keepClips.Set(keep)
	slog.Info("restored", "what", d.keepClips.ObjectID, "using", keep)
	// Off, anything an earlier run left behind goes too.
	d.clips.enable(keep)
	d.restoreSend(c)
}

// newClipsEntities are how many wake clips the device keeps (clips.go): all of them, and each kind.
func newClipsEntities() (*esphome.Sensor, *esphome.TextSensor) {
	total := &esphome.Sensor{
		Base: esphome.Base{
			ObjectID: "wake_clips",
			Name:     "Wake clips",
			Icon:     "mdi:waveform",
			Category: esphome.CategoryDiagnostic,
		},
	}
	kinds := &esphome.TextSensor{
		Base: esphome.Base{
			ObjectID: "wake_clips_by_kind",
			Name:     "Wake clips by kind",
			Icon:     "mdi:waveform",
			Category: esphome.CategoryDiagnostic,
		},
	}
	return total, kinds
}

// newKeepClipsEntity is Keep wake clips: short clips of the wake word kept on the device, for training
// a model on (clips.go). Off by default; turned off, every clip kept is deleted.
func newKeepClipsEntity(d *Detect) *esphome.Switch {
	sw := &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "keep_wake_clips",
			DeviceID: component.DeviceMicrophone,
			Name:     "Keep wake clips",
			Icon:     "mdi:waveform",
			Category: esphome.CategoryConfig,
		},
	}
	sw.OnCommand = func(on bool) {
		if err := d.SetKeepClips(on); err != nil {
			slog.Warn("keep wake clips: not changed", "err", err)
		}
	}
	return sw
}

// SetKeepClips turns Keep wake clips on or off, from Home Assistant's switch or the setup page. Off deletes
// every clip, and holds even when the state cannot be saved (clipsoff.go); on that cannot be saved is
// refused, and the switch is put back.
func (d *Detect) SetKeepClips(on bool) error {
	d.clipsSave.Lock()
	defer d.clipsSave.Unlock()
	// Turned off, the recorder stops first: nothing is kept while a save that is slow to fail runs.
	if !on && d.clips != nil {
		d.clips.enable(false)
	}
	if err := saveKeepClips(on); err != nil {
		slog.Error("saving keep wake clips failed", "err", err)
		if on {
			if d.clips != nil {
				d.keepClips.Set(d.clips.on.Load())
			}
			return fmt.Errorf("could not keep it: %w", err)
		}
		// The clips went all the same, and the off holds past a restart: it is recorded on its own
		// and the save tried again until it holds (clipsoff.go).
		if err := markClipsOff(); err != nil {
			slog.Error("recording keep wake clips off failed too", "err", err)
		}
		d.retryClipsOffLater()
	} else {
		clearClipsOff()
	}
	d.keepClips.Set(on)
	slog.Info("setting changed", "setting", d.keepClips.ObjectID, "using", on)
	if on && d.clips != nil {
		d.clips.enable(true)
	}
	return nil
}
