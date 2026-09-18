package display

import (
	"log/slog"
	"sync/atomic"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// clock24 is the screen showing 15:04 rather than 3:04 PM. It lives here so every page drawn reads it
// without taking the config's lock each frame.
var clock24 atomic.Bool

const (
	clock12Label = "12-hour"
	clock24Label = "24-hour"
)

// clockSelect is the Home Assistant setting between the two; wake redraws the screen once it changes.
func clockSelect(wake func()) *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_clock_format",
			Name:     "Clock format",
			Icon:     "mdi:clock-digital",
			Category: esphome.CategoryConfig,
		},
		Options: []string{clock12Label, clock24Label},
	}
	s.OnCommand = func(v string) {
		on := v == clock24Label
		if err := config.Set().Screen().Clock24(on); err != nil {
			slog.Error("saving the clock format failed", "err", err)
			return
		}
		setClock24(s, on)
		wake()
	}
	return s
}

func setClock24(s *esphome.Select, on bool) {
	clock24.Store(on)
	if on {
		s.Set(clock24Label)
	} else {
		s.Set(clock12Label)
	}
}

// clockHM is the time as a clock face shows it, with its AM/PM set apart: 3:04, or 15:04.
func clockHM(t time.Time) string {
	if clock24.Load() {
		return t.Format("15:04")
	}
	return t.Format("3:04")
}

// clockSuffix is what goes beside or under clockHM: PM, or nothing on a 24-hour clock.
func clockSuffix(t time.Time) string {
	if clock24.Load() {
		return ""
	}
	return t.Format("PM")
}

// clockText is a time within a line of text: 3:04 PM, or 15:04.
func clockText(t time.Time) string {
	if clock24.Load() {
		return t.Format("15:04")
	}
	return t.Format("3:04 PM")
}
