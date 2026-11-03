//go:build spot

package display

import (
	"log/slog"
	"sync/atomic"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Two quieter rims for the Spot (#431). The camera dot can light only while a picture leaves the
// device - a stream, a camera page, Home Assistant's camera entity - rather than
// whenever the camera runs, which presence and person detection do on the device alone. And the idle
// rim's gray track can go, for a dashboard or a clock with nothing round about it; muted, listening,
// timers and the rest still draw. Both as they were until somebody chooses.

const (
	cameraDotRunsLabel    = "Whenever the camera runs"
	cameraDotWatchedLabel = "Only when watched"
)

// cameraDotWatchedOnly and idleRingHidden are the settings, read by each frame without the config's
// lock (see clock24).
var cameraDotWatchedOnly, idleRingHidden atomic.Bool

// cameraDotSelect is the Home Assistant setting for the dot; wake redraws the screen once it changes.
func cameraDotSelect(wake func()) *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_camera_dot",
			Name:     "Camera dot",
			Icon:     "mdi:record-circle-outline",
			Category: esphome.CategoryConfig,
		},
		Options: []string{cameraDotRunsLabel, cameraDotWatchedLabel},
	}
	s.OnCommand = func(v string) {
		on := v == cameraDotWatchedLabel
		if err := config.Set().Screen().CameraDotWatchedOnly(on); err != nil {
			slog.Error("saving the camera dot setting failed", "err", err)
			return
		}
		setCameraDotWatchedOnly(s, on)
		slog.Info("screen: camera dot only when watched", "on", on)
		wake()
	}
	return s
}

func setCameraDotWatchedOnly(s *esphome.Select, on bool) {
	cameraDotWatchedOnly.Store(on)
	if on {
		s.Set(cameraDotWatchedLabel)
	} else {
		s.Set(cameraDotRunsLabel)
	}
}

// idleRingSwitch is the Home Assistant switch for the idle rim, on while it is drawn.
func idleRingSwitch(wake func()) *esphome.Switch {
	s := &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "screen_idle_ring",
			Name:     "Idle ring",
			Icon:     "mdi:circle-outline",
			Category: esphome.CategoryConfig,
		},
	}
	s.OnCommand = func(on bool) {
		if err := config.Set().Screen().NoIdleRing(!on); err != nil {
			slog.Error("saving the idle ring setting failed", "err", err)
			return
		}
		setIdleRing(s, on)
		slog.Info("screen: idle ring", "on", on)
		wake()
	}
	return s
}

func setIdleRing(s *esphome.Switch, on bool) {
	idleRingHidden.Store(!on)
	s.Set(on)
}
