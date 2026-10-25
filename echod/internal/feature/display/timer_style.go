//go:build !dot

package display

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// How running timers show on the Show's clock (render_timers.go): Line, the soonest under the date
// with a thin bar, as it always was; Ring, a ring for each that empties as it runs; Corner, a small
// badge at the top right that leaves the clock the whole screen; Banner, a card for each across the
// foot; Big, the soonest's countdown in place of the clock. A timer that finishes rings the same
// whatever the style.

const (
	timerLine   = ""
	timerRing   = "ring"
	timerCorner = "corner"
	timerBanner = "banner"
	timerBig    = "big"
)

// timerStyles are the choices, as the screen and Home Assistant name them, with what the config keeps.
var timerStyles = []struct {
	label, value string
}{
	{"Line", timerLine},
	{"Ring", timerRing},
	{"Corner", timerCorner},
	{"Banner", timerBanner},
	{"Big", timerBig},
}

func timerStyleOptions() []string {
	out := make([]string, len(timerStyles))
	for i, t := range timerStyles {
		out[i] = t.label
	}
	return out
}

// timerStyleIndex is the saved choice's place in timerStyles; a value no choice has reads as Line.
func timerStyleIndex() int {
	v := config.Get().Screen.TimerStyle
	for i, t := range timerStyles {
		if t.value == v {
			return i
		}
	}
	return 0
}

// timerStyle is the style in force.
func timerStyle() string { return timerStyles[timerStyleIndex()].value }

// setTimerStyle saves the choice at i and shows it in Home Assistant.
func setTimerStyle(s *esphome.Select, i int) {
	if i < 0 || i >= len(timerStyles) {
		return
	}
	if err := config.Set().Screen().TimerStyle(timerStyles[i].value); err != nil {
		slog.Error("saving the timers style failed", "err", err)
		return
	}
	if s != nil {
		s.Set(timerStyles[i].label)
	}
}

// timerStyleSelect is the Home Assistant setting; wake redraws the clock in the new style.
func timerStyleSelect(wake func()) *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_timer_style",
			Name:     "Timers on the clock",
			Icon:     "mdi:timer-outline",
			Category: esphome.CategoryConfig,
		},
		Options: timerStyleOptions(),
	}
	s.OnCommand = func(v string) {
		for i, t := range timerStyles {
			if t.label == v {
				setTimerStyle(s, i)
				wake()
				return
			}
		}
	}
	return s
}
