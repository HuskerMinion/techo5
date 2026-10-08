//go:build !dot && !spot

package display

import esphome "github.com/ygelfand/go-esphome-device"

// hasClockTap is whether the Tap on the clock setting is offered (clock_tap.go).
const hasClockTap = true

// clockTapSelect is the Home Assistant setting.
func clockTapSelect() *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_clock_tap",
			Name:     "Tap on the clock",
			Icon:     "mdi:gesture-tap",
			Category: esphome.CategoryConfig,
		},
		Options: clockTapOptions(),
	}
	s.OnCommand = func(v string) {
		for i, c := range clockTaps {
			if c.label == v {
				setClockTap(s, i)
				return
			}
		}
	}
	return s
}
