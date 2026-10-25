//go:build !dot && !spot

package display

import (
	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

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
		for i, c := range clockTapChoices() {
			if c.label == v {
				setClockTap(s, i)
				return
			}
		}
	}
	return s
}

// nightTapOpensDashboard is whether a tap on the night screen opens the dashboard: when a tap on the
// clock does by day. A night light is otherwise left alone when tapped, a hand in the dark being more
// likely a knock than a request; somebody who chose the dashboard for the clock's tap asked for it.
func nightTapOpensDashboard() bool { return config.Get().Screen.ClockTap == "dashboard" }
