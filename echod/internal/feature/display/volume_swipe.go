//go:build !dot

package display

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// Swipe for volume: on by default; off, a swipe up or down on the clock, on the pages its swipes reach
// (now playing, the weather) and on the Bluetooth page leaves the volume alone, for a screen in reach
// of hands that keep brushing it (#243). A Show's edges keep their swipes: down from the top is still
// the settings, up from the bottom still the deck. The volume buttons, Home Assistant and the Spot's ring
// menu are untouched.

// volumeSwipeOn is whether a swipe up or down changes the volume now.
func volumeSwipeOn() bool { return !config.Get().Screen.NoVolumeSwipe }

// swipeVolume is a swipe's step of the volume, while Swipe for volume is on.
func swipeVolume(step int) {
	if volumeSwipeOn() {
		adjustVolume(step)
	}
}

// adjustVolume moves the speaker's level; a test stands in for it.
var adjustVolume = func(step int) { media.Get().Adjust(step) }

// volumeSwipeSwitch is the Home Assistant switch for the setting.
func volumeSwipeSwitch() *esphome.Switch {
	s := &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "screen_volume_swipe",
			Name:     "Swipe for volume",
			Icon:     "mdi:gesture-swipe-vertical",
			Category: esphome.CategoryConfig,
		},
	}
	s.OnCommand = func(on bool) { setVolumeSwipe(s, on) }
	return s
}

// setVolumeSwipe saves the setting and tells Home Assistant.
func setVolumeSwipe(s *esphome.Switch, on bool) {
	if err := config.Set().Screen().NoVolumeSwipe(!on); err != nil {
		slog.Error("saving a setting failed", "setting", s.ObjectID, "err", err)
		s.Set(!config.Get().Screen.NoVolumeSwipe)
		return
	}
	s.Set(on)
	slog.Info("setting changed", "setting", s.ObjectID, "using", on)
}
