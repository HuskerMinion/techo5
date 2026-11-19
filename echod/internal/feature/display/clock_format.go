//go:build !dot

package display

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

const (
	clock12Label     = "12-hour"
	clock12BareLabel = "12-hour, no AM/PM"
	clock24Label     = "24-hour"
)

// clockLabels are Clock format's choices, in the order the select and the screen's picker list them.
var clockLabels = []string{clock12Label, clock12BareLabel, clock24Label}

// clockFormatOf is a choice of clockLabels as the two settings it stands for.
func clockFormatOf(label string) (h24, bare bool) {
	return label == clock24Label, label == clock12BareLabel
}

// clockSelect is the Home Assistant setting between them; wake redraws the screen once it changes.
func clockSelect(wake func()) *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_clock_format",
			Name:     "Clock format",
			Icon:     "mdi:clock-digital",
			Category: esphome.CategoryConfig,
		},
		Options: clockLabels,
	}
	s.OnCommand = func(v string) {
		h24, bare := clockFormatOf(v)
		if err := config.Set().Screen().ClockFormat(h24, bare); err != nil {
			slog.Error("saving the clock format failed", "err", err)
			return
		}
		setClockFormat(s, h24, bare)
		wake()
	}
	return s
}

func setClockFormat(s *esphome.Select, h24, bare bool) {
	clock24.Store(h24)
	noAMPM.Store(bare)
	if s != nil {
		s.Set(clockLabels[clockIndex()])
	}
}

// clockIndex is the choice in force, as an index into clockLabels.
func clockIndex() int {
	switch {
	case clock24.Load():
		return 2
	case noAMPM.Load():
		return 1
	}
	return 0
}
