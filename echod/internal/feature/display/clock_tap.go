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

type clockTap struct {
	label string
	value string
}

// clockTaps are the choices, with what the config keeps for each; Assist is kept as nothing.
var clockTaps = []clockTap{
	{"Assist", ""},
	{"Dashboard", "dashboard"},
	{"Nothing", "nothing"},
	{"Deck", "deck"},
}

// clockTapChoices are the choices this screen offers: no Assist without a microphone, where a tap
// kept as Assist opens the dashboard (Display.gesture), and so reads as Dashboard.
func clockTapChoices() []clockTap {
	if !hasMicrophone() {
		return clockTaps[1:]
	}
	return clockTaps
}

func clockTapOptions() []string {
	taps := clockTapChoices()
	out := make([]string, len(taps))
	for i, c := range taps {
		out[i] = c.label
	}
	return out
}

// clockTapIndex is the saved choice's place in clockTapChoices; a value no choice has reads as the
// first.
func clockTapIndex() int {
	v := config.Get().Screen.ClockTap
	for i, c := range clockTapChoices() {
		if c.value == v {
			return i
		}
	}
	return 0
}

// setClockTap saves the choice at i and shows it in Home Assistant.
func setClockTap(s *esphome.Select, i int) {
	taps := clockTapChoices()
	if i < 0 || i >= len(taps) {
		return
	}
	if err := config.Set().Screen().ClockTap(taps[i].value); err != nil {
		slog.Error("saving the clock tap failed", "err", err)
		return
	}
	slog.Info("screen: a tap on the clock", "does", taps[i].label)
	if s != nil {
		s.Set(taps[i].label)
	}
}
