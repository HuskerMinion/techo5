//go:build !dot && !spot

package display

import (
	"log/slog"
	"sync/atomic"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Show Microphone off (techo5#621): whether the header's red "Microphone off" pill is up while the
// microphones are muted. On by default. Offered only where the red mute light already says it
// (muteLightSaysIt): on a Show, turning it off leaves the light to say it alone. Where there is no
// such light (the Skylight, a tablet) the pill always shows and the switch is not there. The night
// clock has no header, so it never shows the pill either way.

// micOffPillHidden is the switch turned off, read by each frame without the config's lock (see
// clock24).
var micOffPillHidden atomic.Bool

// micOffPillShown is whether a muted device says so in its header.
func micOffPillShown() bool { return !muteLightSaysIt() || !micOffPillHidden.Load() }

// micOffSaid is whether the header's pill says the microphone is off in this frame: muted, and the
// pill wanted.
func (s scene) micOffSaid() bool { return s.muted && micOffPillShown() }

// micOffPillSwitch is the Home Assistant switch for it; wake redraws the screen once it changes.
func micOffPillSwitch(wake func()) *esphome.Switch {
	s := &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "screen_microphone_off",
			Name:     "Show Microphone off",
			Icon:     "mdi:microphone-off",
			Category: esphome.CategoryConfig,
		},
	}
	s.OnCommand = func(on bool) {
		if err := config.Set().Screen().NoMicOffPill(!on); err != nil {
			slog.Error("saving a setting failed", "setting", "screen_microphone_off", "err", err)
			return
		}
		setMicOffPill(s, on)
		slog.Info("setting changed", "setting", "screen_microphone_off", "using", on)
		wake()
	}
	return s
}

// setMicOffPill takes the setting up and shows it in Home Assistant.
func setMicOffPill(s *esphome.Switch, on bool) {
	micOffPillHidden.Store(!on)
	s.Set(on)
}
