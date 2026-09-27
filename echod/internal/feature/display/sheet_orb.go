//go:build !dot

package display

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// turnStyles are the turn screen's choices: the title and bar, or the orb (render_orb.go).
var turnStyles = []string{"Classic", "Orb"}

func turnStyleIndex() int {
	if config.Get().Screen.TurnOrb {
		return 1
	}
	return 0
}

func turnStyleText() string { return turnStyles[turnStyleIndex()] }

// saveTurnStyle saves a choice and shows it on s, the select in Home Assistant, when there is one.
func saveTurnStyle(s *esphome.Select, i int) {
	if i < 0 || i >= len(turnStyles) {
		return
	}
	if err := config.Set().Screen().TurnOrb(i == 1); err != nil {
		slog.Error("saving the turn screen setting failed", "err", err)
		return
	}
	if s != nil {
		s.Set(turnStyles[i])
	}
}

// turnStyleSelect is the setting in Home Assistant.
func turnStyleSelect(wake func()) *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_turn_style",
			Name:     "Turn screen",
			Icon:     "mdi:orbit",
			Category: esphome.CategoryConfig,
		},
		Options: turnStyles,
	}
	s.OnCommand = func(v string) {
		for i, o := range turnStyles {
			if o == v {
				saveTurnStyle(s, i)
				wake()
				return
			}
		}
	}
	return s
}
