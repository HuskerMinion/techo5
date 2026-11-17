//go:build !dot

package display

import (
	"fmt"
	"log/slog"
	"slices"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Screen off after idle: the screen goes out once nobody has touched it, spoken to it or, with presence
// detection on, been seen near it for a while, and a tap or the wake word lights it again. It is the
// room's screen-off (presence_screen.go) without needing the camera, with the same rules for the night
// and for anything going on. Home Assistant cannot see a finger on the panel, so a timer there would
// put the screen out under it; this one counts the touches.

// idleOffIndex is the saved wait's place in awayChoices, the same waits the room's screen-off offers; a
// value no choice has reads as never.
func idleOffIndex() int {
	return max(slices.Index(awayChoices, config.Get().Screen.OffAfterMinutes), 0)
}

// idleOffAfter is how long the screen waits unused before it goes out, 0 for never.
func idleOffAfter() time.Duration {
	return time.Duration(awayChoices[idleOffIndex()]) * time.Minute
}

// idleOffName is a wait as Home Assistant's select offers it: English, and kept as it is, since
// automations set the select by these names.
func idleOffName(m int) string {
	switch m {
	case 0:
		return "Never"
	case 1:
		return "1 minute"
	case 60:
		return "1 hour"
	}
	return fmt.Sprintf("%d minutes", m)
}

func idleOffNames() []string {
	out := make([]string, len(awayChoices))
	for i, m := range awayChoices {
		out[i] = idleOffName(m)
	}
	return out
}

// setIdleOff saves the wait at i and shows it in Home Assistant.
func (d *Display) setIdleOff(i int) {
	if i < 0 || i >= len(awayChoices) {
		return
	}
	if err := config.Set().Screen().OffAfterMinutes(awayChoices[i]); err != nil {
		slog.Error("saving screen off after idle failed", "err", err)
		return
	}
	if d.idleOffSel != nil {
		d.idleOffSel.Set(idleOffName(awayChoices[i]))
	}
	slog.Info("setting changed", "setting", "screen_off_after_idle", "using", idleOffName(awayChoices[i]))
	d.wake()
}

// idleOffSelect is the Home Assistant setting.
func idleOffSelect(d *Display) *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_off_after_idle",
			Name:     "Screen off after idle",
			Icon:     "mdi:monitor-off",
			Category: esphome.CategoryConfig,
		},
		Options: idleOffNames(),
	}
	s.OnCommand = func(v string) {
		if i := slices.Index(idleOffNames(), v); i >= 0 {
			d.setIdleOff(i)
		}
	}
	return s
}
