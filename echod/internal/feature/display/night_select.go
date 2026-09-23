//go:build !dot && !spot

package display

import (
	"log/slog"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The night's two settings in Home Assistant, beside Screen and Auto brightness: its hours, and what
// it does to the screen. Both are on the screen too, under Display.

// nightHoursText is a preset as Home Assistant lists it. In 24-hour time whatever the screen's clock
// says, so the list does not change under an automation when somebody changes the clock format.
func nightHoursText(v string) string {
	from, to, ok := nightWindow(v)
	if !ok {
		return "Never"
	}
	h := func(n int) string { return time.Date(2000, 1, 1, n, 0, 0, 0, time.UTC).Format("15:04") }
	return h(from) + " – " + h(to)
}

func nightHoursSelect() *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_night_hours",
			Name:     "Night hours",
			Icon:     "mdi:weather-night",
			Category: esphome.CategoryConfig,
		},
	}
	for _, p := range nightPresets {
		s.Options = append(s.Options, nightHoursText(p))
	}
	s.OnCommand = func(v string) {
		for _, p := range nightPresets {
			if nightHoursText(p) == v {
				if err := config.Set().Screen().Night(p); err != nil {
					slog.Error("saving the night hours failed", "err", err)
					return
				}
				s.Set(v)
				return
			}
		}
	}
	return s
}

func atNightSelect(d *Display) *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_at_night",
			Name:     "Screen at night",
			Icon:     "mdi:lightbulb-night-outline",
			Category: esphome.CategoryConfig,
		},
		Options: atNightOptions,
	}
	s.OnCommand = func(v string) {
		for i, o := range atNightOptions {
			if o == v {
				d.setAtNight(i)
				return
			}
		}
	}
	return s
}

// setAtNight saves the choice, shows it in Home Assistant, and lets the night take it up at once.
func (d *Display) setAtNight(i int) {
	if err := config.Set().Screen().NightLight(i == 1); err != nil {
		slog.Error("saving the night setting failed", "err", err)
		return
	}
	if d.atNight != nil {
		d.atNight.Set(atNightOptions[i])
	}
	if i == 0 {
		// Back to going dark: a glow still up would otherwise stay for the rest of the night.
		d.mu.Lock()
		glowing := d.nightGlow
		d.nightGlow = false
		d.mu.Unlock()
		if glowing {
			d.relight(true)
		}
	}
	d.wake()
}

// nightHoursChanged shows the hours chosen on the screen in Home Assistant.
func (d *Display) nightHoursChanged() {
	if d.nightHours != nil {
		d.nightHours.Set(nightHoursText(config.Get().Screen.Night))
	}
}
