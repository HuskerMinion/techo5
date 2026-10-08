//go:build !dot

package display

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// What a tap on the clock does: start a voice turn, as it always has, put the dashboard up, or nothing,
// for a panel that is only ever talked to. Only the Show offers it (hasClockTap): the Spot's clock is
// the middle of its ring menu.

// clockTaps are the choices, with what the config keeps for each; Assist is kept as nothing.
var clockTaps = []struct {
	label string
	value string
}{
	{"Assist", ""},
	{"Dashboard", "dashboard"},
	{"Nothing", "nothing"},
	{"Deck", "deck"},
}

func clockTapOptions() []string {
	out := make([]string, len(clockTaps))
	for i, c := range clockTaps {
		out[i] = c.label
	}
	return out
}

// clockTapIndex is the saved choice's place in clockTaps; a value no choice has reads as Assist.
func clockTapIndex() int {
	v := config.Get().Screen.ClockTap
	for i, c := range clockTaps {
		if c.value == v {
			return i
		}
	}
	return 0
}

// setClockTap saves the choice at i and shows it in Home Assistant.
func setClockTap(s *esphome.Select, i int) {
	if i < 0 || i >= len(clockTaps) {
		return
	}
	if err := config.Set().Screen().ClockTap(clockTaps[i].value); err != nil {
		slog.Error("saving the clock tap failed", "err", err)
		return
	}
	slog.Info("screen: a tap on the clock", "does", clockTaps[i].label)
	if s != nil {
		s.Set(clockTaps[i].label)
	}
}
