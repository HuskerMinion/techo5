package config

import "testing"

func TestWeatherEntity(t *testing.T) {
	for _, c := range []struct{ saved, want string }{
		{"", DefaultWeather},
		{WeatherOff, ""},
		{"weather.backyard", "weather.backyard"},
	} {
		if got := (Home{Weather: c.saved}).WeatherEntity(); got != c.want {
			t.Errorf("WeatherEntity(%q) = %q, want %q", c.saved, got, c.want)
		}
	}
	// Home Assistant listed its weather entities: the default only while it has its own forecast.
	if got := (Home{WeatherSources: []string{"weather.a", DefaultWeather}}).WeatherEntity(); got != DefaultWeather {
		t.Errorf("with its own forecast listed: %q", got)
	}
	if got := (Home{WeatherSources: []string{"weather.a", "weather.b"}}).WeatherEntity(); got != "weather.a" {
		t.Errorf("without its own forecast: %q, want the first listed", got)
	}
	if got := (Home{Weather: WeatherOff, WeatherSources: []string{"weather.a"}}).WeatherEntity(); got != "" {
		t.Errorf("none chosen: %q", got)
	}
}
