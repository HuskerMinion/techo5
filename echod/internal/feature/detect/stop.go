package detect

import (
	"log/slog"

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
func (d *Detect) loadStop() {
	if !config.Get().Wake.Stop.Listening() {
		d.engine.Clear(StopSlot)
		slog.Info("stop word off", "threshold", config.Get().Wake.Stop.Threshold)
		return
	}

	m, err := stopModel()
	if err != nil {
		slog.Error("the stop word is unavailable", "err", err)
		d.engine.Clear(StopSlot)
		return
	}
	if err := d.engine.Use(StopSlot, m); err != nil {
		slog.Error("loading the stop word failed", "err", err)
		d.engine.Clear(StopSlot)
		return
	}
	slog.Info("stop word listening", "threshold", config.Get().Wake.Stop.Threshold)
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
	return []esphome.Entity{d.stop, d.hangup.word, d.hangup.threshold, d.score, d.onHA.stream, d.onHA.confirm, d.onHA.threshold,
		d.clipsTotal, d.clipsKinds, d.keepClips}
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

// Restore only publishes the value. Loading the model is Start's, through the engine's Load, so that a
// restart and a first boot take the same path.
func (d *Detect) Restore(c config.Config) {
	d.stop.Set(float32(c.Wake.Stop.Threshold))
	slog.Info("restored", "what", d.stop.ObjectID, "using", c.Wake.Stop.Threshold)
	d.hangup.restore(c.Wake.Hangup)
	d.onHA.restore(c.Wake.OnHA)
	// An off that never reached the state wins over it (clipsoff.go).
	keep := d.keepClipsAtStart(c.Wake.KeepClips)
	d.keepClips.Set(keep)
	slog.Info("restored", "what", d.keepClips.ObjectID, "using", keep)
	// Off, anything an earlier run left behind goes too.
	d.clips.enable(keep)
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
		d.clipsSave.Lock()
		defer d.clipsSave.Unlock()
		// Turned off, the recorder stops first: nothing is kept while a save that is slow to fail runs.
		if !on && d.clips != nil {
			d.clips.enable(false)
		}
		if err := saveKeepClips(on); err != nil {
			slog.Error("saving keep wake clips failed", "err", err)
			if on {
				return
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
		sw.Set(on)
		slog.Info("setting changed", "setting", sw.ObjectID, "using", on)
		if on && d.clips != nil {
			d.clips.enable(true)
		}
	}
	return sw
}
