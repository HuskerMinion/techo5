//go:build !dot

package display

import (
	"log/slog"
	"sync/atomic"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// callButton is the home screen showing its Call button: the way to the list of devices in the house
// and phone contacts without a word said. Kept here, as clock24 is, so each frame reads it without the
// config's lock.
var callButton atomic.Bool

// callButtonSwitch is the Home Assistant switch for it; wake redraws the screen once it changes.
func callButtonSwitch(wake func()) *esphome.Switch {
	s := &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "screen_call_button",
			Name:     "Call button on the home screen",
			Icon:     "mdi:phone",
			Category: esphome.CategoryConfig,
		},
	}
	s.OnCommand = func(on bool) {
		setCallButtonSaved(s, on)
		wake()
	}
	return s
}

// setCallButtonSaved changes the setting and keeps it.
func setCallButtonSaved(s *esphome.Switch, on bool) {
	if err := config.Set().Screen().CallButton(on); err != nil {
		slog.Error("saving the call button setting failed", "err", err)
		return
	}
	setCallButton(s, on)
	slog.Info("screen: call button", "on", on)
}

func setCallButton(s *esphome.Switch, on bool) {
	callButton.Store(on)
	s.Set(on)
}
