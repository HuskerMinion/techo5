package home

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The weather alerts' switch: on unless turned off, on the settings screen or as Home Assistant's
// "Weather alerts" switch. Off, nothing is fetched and nothing shows.

// AlertsOn is whether the weather alerts are wanted.
func AlertsOn() bool { return !config.Get().Home.AlertsOff }

func (f *Feature) buildAlertsSwitch() {
	f.alertsSw = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "weather_alerts",
			Name:     "Weather alerts",
			Icon:     "mdi:alert",
			Category: esphome.CategoryConfig,
		},
		OnCommand: func(on bool) { f.SetAlertsOn(on) },
	}
}

// SetAlertsOn saves the choice and shows it in Home Assistant; turned off, what was fetched goes, and
// turned on, a fetch starts the next time the screen draws.
func (f *Feature) SetAlertsOn(on bool) {
	if err := config.Set().Home().AlertsOff(!on); err != nil {
		slog.Error("saving the weather alerts switch failed", "err", err)
		return
	}
	f.alertsSw.Set(on)
	a := &f.alerts
	a.mu.Lock()
	a.view, a.fetched = AlertView{}, a.fetched.AddDate(-1, 0, 0)
	a.mu.Unlock()
	f.Changed.Emit(struct{}{})
}
