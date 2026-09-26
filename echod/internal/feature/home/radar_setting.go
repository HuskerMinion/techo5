package home

import (
	"log/slog"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The radar source setting: automatic (the NWS in the lower 48, RainViewer anywhere else), or one of
// them always. Offered on the device's settings and as Home Assistant's "Radar source" select.

// radarChoices are the options, in the order both offer them; value is what config keeps.
var radarChoices = []struct{ label, value string }{
	{"Automatic", ""},
	{"NWS (U.S.)", config.RadarNWS},
	{"RainViewer", config.RadarRainViewer},
}

// RadarSourceOptions are the choices' labels.
func RadarSourceOptions() []string {
	out := make([]string, len(radarChoices))
	for i, c := range radarChoices {
		out[i] = c.label
	}
	return out
}

// RadarSourceIndex is the saved choice's place among them; a value none has reads as automatic.
func RadarSourceIndex() int {
	v := config.Get().Home.RadarSource
	for i, c := range radarChoices {
		if c.value == v {
			return i
		}
	}
	return 0
}

func (f *Feature) buildRadarSelect() {
	f.radarSel = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "radar_source",
			Name:     "Radar source",
			Icon:     "mdi:radar",
			Category: esphome.CategoryConfig,
		},
		Options: RadarSourceOptions(),
		OnCommand: func(v string) {
			for i, c := range radarChoices {
				if c.label == v {
					f.SetRadarSource(i)
					return
				}
			}
		},
	}
}

// SetRadarSource saves the choice at i, shows it in Home Assistant, and has the rain map fetched
// again from the new source the next time it is looked at (at once, if it is being looked at).
func (f *Feature) SetRadarSource(i int) {
	if i < 0 || i >= len(radarChoices) {
		return
	}
	if err := config.Set().Home().RadarSource(radarChoices[i].value); err != nil {
		slog.Error("saving the radar source failed", "err", err)
		return
	}
	f.radarSel.Set(radarChoices[i].label)
	r := &f.radar
	r.mu.Lock()
	r.fetched = time.Time{}
	r.gen++
	r.mu.Unlock()
	f.Changed.Emit(struct{}{})
}
