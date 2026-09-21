package detect

import (
	"context"
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
	"github.com/HuskerMinion/techo5/echod/internal/feature/detect/assets"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// AnnounceSlot is where the announce word listens. Beside the stop word, above Home Assistant's own
// slots, for the same reason: it is the device's word rather than one somebody picked, and it opens
// no pipeline.
//
// This is what makes announcing work with Home Assistant switched off. Every other way in goes
// through it — the action it offers, or the sentence trigger somebody writes — and all of those stop
// when the server does. Saying the phrase at the device does not.
const AnnounceSlot = 101

// announceModel describes the embedded word to the engine.
//
// It is an openWakeWord classifier, which matters for what it costs: a microWakeWord model carries
// its own feature front end and so costs one per frame, while this is another classifier over the
// front end the engine is already running for whatever else is loaded. Leaving it listening is
// cheap; it is off by default for what it might hear, not for what it costs.
func announceModel() (wake.Model, error) {
	path, err := assets.HouseAnnounce(layout.StateDir)
	if err != nil {
		return wake.Model{}, err
	}
	return wake.Model{
		ID:     "house_announce",
		Phrase: assets.AnnouncePhrase,
		Kind:   wake.KindOpenWakeWord,
		Path:   path,
	}, nil
}

// loadAnnounce puts the announce word in, or takes it out when it is switched off.
func (d *Detect) loadAnnounce() {
	if !announceWordAvailable {
		d.engine.Clear(AnnounceSlot)
		return
	}
	if !config.Get().Wake.Announce.Listening() {
		d.engine.Clear(AnnounceSlot)
		slog.Info("announce word off")
		return
	}

	m, err := announceModel()
	if err != nil {
		slog.Error("the announce word is unavailable", "err", err)
		d.engine.Clear(AnnounceSlot)
		return
	}
	if err := d.engine.Use(AnnounceSlot, m); err != nil {
		slog.Error("loading the announce word failed", "err", err)
		d.engine.Clear(AnnounceSlot)
		return
	}
	slog.Info("announce word listening",
		"phrase", assets.AnnouncePhrase, "threshold", config.Get().Wake.Announce.Threshold)
}

// heard is what the announce word does when it fires: open the microphone and send what is said to
// the rest of the house. The recording, the tone that says it is listening and the sending are all
// announce's own — this only decides that it should start.
func (d *Detect) announceHeard() {
	if announce.Get().Recording() {
		return // already listening; saying it twice does not start a second one
	}
	// Not while one is coming out of this device, or just did. The speaker is a foot from the
	// microphone and an announcement is speech: without this the device answers its own playback,
	// announces that, and the house talks to itself until somebody mutes it.
	if announce.Get().JustPlayed() {
		slog.Debug("announce word ignored: an announcement is playing here")
		return
	}
	slog.Info("announce word heard")
	safe.Go("announce word", func() { announce.Get().Speak(context.Background()) })
}

// newAnnounceEntity is the one control the announce word has, the same shape as the stop word's: the
// top of the range is off, so a switch beside it would be the same thing said twice.
//
// It starts off. A device that acts on something said near it, without anybody having asked for
// that, is worse than a feature nobody found.
func newAnnounceEntity(d *Detect) *esphome.Number {
	n := &esphome.Number{
		Base: esphome.Base{
			ObjectID: "announce_word_sensitivity",
			DeviceID: component.DeviceMicrophone,
			Name:     "Announce word sensitivity",
			Icon:     "mdi:bullhorn",
			Category: esphome.CategoryConfig,
		},
		Min: 0.3, Max: config.AnnounceOff, Step: 0.01,
		Mode: esphome.NumberBox,
	}

	n.OnCommand = func(v float32) {
		n.Set(v)

		if err := config.Set().Announce().Threshold(float64(v)); err != nil {
			slog.Error("saving the announce threshold failed", "err", err)
			return
		}
		// Loaded or dropped to match, so switching it off gives the work back rather than only
		// ignoring what it finds.
		d.loadAnnounce()
	}
	return n
}
