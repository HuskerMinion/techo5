//go:build !dot

package dashboard

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Two settings for a dashboard used as a panel: how long one opened by hand stays up before the clock
// comes back, and on the Show, how big a drawn one's tiles are.

// backChoices are how long a dashboard opened by hand stays up untouched, as config keeps them: 0 is
// the ten minutes it always was, and a negative value never.
var backChoices = []struct {
	label   string
	seconds int
}{
	{"30 seconds", 30}, {"1 minute", 60}, {"2 minutes", 120}, {"5 minutes", 300}, {"10 minutes", 0}, {"Never", -1},
}

// tilesChoices are how big a drawn dashboard's tiles are, as config keeps them.
var tilesChoices = []struct {
	label string
	value string
}{
	{"Normal", ""}, {"Large", "large"}, {"Fill the screen", "fill"},
}

// BackChoices and TilesChoices are the labels, in order, for the screen's own settings.
func BackChoices() []string {
	out := make([]string, len(backChoices))
	for i, c := range backChoices {
		out[i] = c.label
	}
	return out
}

func TilesChoices() []string {
	out := make([]string, len(tilesChoices))
	for i, c := range tilesChoices {
		out[i] = c.label
	}
	return out
}

func backLabel(seconds int) string {
	for _, c := range backChoices {
		if c.seconds == seconds {
			return c.label
		}
	}
	return backChoices[4].label
}

func tilesLabel(v string) string {
	for _, c := range tilesChoices {
		if c.value == v {
			return c.label
		}
	}
	return tilesChoices[0].label
}

func backSelect(f *Feature) *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_dashboard_return",
			Name:     "Dashboard returns to the clock after",
			Icon:     "mdi:timer-sand",
			Category: esphome.CategoryConfig,
		},
		Options: BackChoices(),
	}
	s.OnCommand = func(v string) { f.SetBack(v) }
	return s
}

func tilesSelect(f *Feature) *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_dashboard_tiles",
			Name:     "Dashboard tiles",
			Icon:     "mdi:view-grid-outline",
			Category: esphome.CategoryConfig,
		},
		Options: TilesChoices(),
	}
	s.OnCommand = func(v string) { f.SetTiles(v) }
	return s
}

// HasTiles is whether this device offers the Dashboard tiles setting: the Show does.
func HasTiles() bool { return hasTiles }

// Back and Tiles are the two settings' current choices, by label.
func Back() string  { return backLabel(config.Get().Dashboard.ReturnAfter) }
func Tiles() string { return tilesLabel(config.Get().Dashboard.Tiles) }

// ChooseMode and ChooseIdle are the Dashboard and Dashboard when idle settings set from somewhere other
// than Home Assistant - the setup page - and shown there as well.
func (f *Feature) ChooseMode(m config.DashboardMode) error {
	if err := config.Set().Dashboard().Mode(m); err != nil {
		return err
	}
	f.mode.Set(m.Label())
	f.setMode(m)
	slog.Info("dashboard: mode", "mode", m.Label())
	return nil
}

func (f *Feature) ChooseIdle(on bool) error {
	if err := config.Set().Dashboard().Idle(on); err != nil {
		return err
	}
	f.idle.Set(on)
	f.Changed.Emit(struct{}{})
	return nil
}

// SetBack saves how long a dashboard opened by hand stays up, by its label.
func (f *Feature) SetBack(label string) {
	for _, c := range backChoices {
		if c.label == label {
			if err := config.Set().Dashboard().ReturnAfter(c.seconds); err != nil {
				slog.Error("saving the dashboard return failed", "err", err)
				return
			}
			f.back.Set(label)
			slog.Info("dashboard: returns to the clock after", "choice", label)
			f.Changed.Emit(struct{}{})
			return
		}
	}
	slog.Warn("unknown dashboard return", "value", label)
}

// SetTiles saves how big a drawn dashboard's tiles are, by its label.
func (f *Feature) SetTiles(label string) {
	for _, c := range tilesChoices {
		if c.label == label {
			if err := config.Set().Dashboard().Tiles(c.value); err != nil {
				slog.Error("saving the dashboard tiles failed", "err", err)
				return
			}
			f.tiles.Set(label)
			slog.Info("dashboard: tiles", "choice", label)
			f.Changed.Emit(struct{}{})
			return
		}
	}
	slog.Warn("unknown dashboard tiles", "value", label)
}
