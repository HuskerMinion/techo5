package home

import (
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func TestWeatherOptions(t *testing.T) {
	h := config.Home{Weather: "weather.station", WeatherSources: []string{"weather.forecast_home", "weather.other", "weather.station"}}
	want := []string{weatherNone, config.DefaultWeather, "weather.station", "weather.other"}
	if got := weatherOptions(h); !slices.Equal(got, want) {
		t.Errorf("options = %v, want %v", got, want)
	}
	if got := weatherOptions(config.Home{}); !slices.Equal(got, []string{weatherNone, config.DefaultWeather}) {
		t.Errorf("a new device offers %v", got)
	}
	if chosenOption(config.Home{Weather: config.WeatherOff}) != weatherNone || optionEntity(weatherNone) != "" {
		t.Error("none does not round-trip")
	}
	if chosenOption(config.Home{}) != config.DefaultWeather {
		t.Error("a new device does not show Home Assistant's forecast")
	}
	// A Home Assistant without its own forecast: not offered, and the first listed shows instead.
	none := config.Home{WeatherSources: []string{"weather.a", "weather.b"}}
	if got := weatherOptions(none); !slices.Equal(got, []string{weatherNone, "weather.a", "weather.b"}) {
		t.Errorf("without its own forecast: options = %v", got)
	}
	if chosenOption(none) != "weather.a" {
		t.Errorf("without its own forecast: shows %q", chosenOption(none))
	}
	// An entity chosen by the action is offered even before Home Assistant lists it.
	if got := weatherOptions(config.Home{Weather: "weather.a"}); !slices.Contains(got, "weather.a") {
		t.Errorf("the chosen entity is not offered: %v", got)
	}
}
