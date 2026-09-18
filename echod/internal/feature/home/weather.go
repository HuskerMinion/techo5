package home

import (
	"log/slog"
	"slices"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The weather source: which weather entity the clock and the forecast page show. A new device shows
// Home Assistant's own forecast (config.DefaultWeather); a station of your own, a Tempest or any
// other weather.* entity, is picked on the device, from the "Weather source" select, or with the
// home_weather action, and stays picked.

// weatherNone is the option for no weather, as the select and the screen say it.
const weatherNone = "None"

// sourcesEvery is how often the list of weather entities is asked for again, with a token.
const sourcesEvery = 6 * time.Hour

func (f *Feature) buildWeatherSelect() {
	f.weatherSel = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "weather_source",
			Name:     "Weather source",
			Icon:     "mdi:weather-partly-cloudy",
			Category: esphome.CategoryConfig,
		},
		Options:   weatherOptions(config.Get().Home),
		OnCommand: func(v string) { f.ChooseWeather(optionEntity(v)) },
	}
}

// Entities is the weather source select, plus the slideshow selects and idle number on a device
// with a screen to show them on.
func (f *Feature) Entities() []esphome.Entity {
	if hasScreen {
		return []esphome.Entity{f.weatherSel, f.slideshowSel, f.slideshowOverlaySel, f.slideshowIdleNum,
			f.slideshowShuffleSw, f.slideshowSubfoldersSw}
	}
	return []esphome.Entity{f.weatherSel}
}

// weatherOptions is what the select offers: none, Home Assistant's forecast, the one chosen, and
// every weather entity Home Assistant listed when it was last asked.
func weatherOptions(h config.Home) []string {
	opts := []string{weatherNone, config.DefaultWeather}
	add := func(id string) {
		if id != "" && !slices.Contains(opts, id) {
			opts = append(opts, id)
		}
	}
	add(h.WeatherEntity())
	for _, id := range h.WeatherSources {
		add(id)
	}
	return opts
}

// optionEntity is the entity an option names, "" for none.
func optionEntity(v string) string {
	if v == weatherNone {
		return ""
	}
	return strings.TrimSpace(v)
}

// chosenOption is the select's value for a configuration.
func chosenOption(h config.Home) string {
	if e := h.WeatherEntity(); e != "" {
		return e
	}
	return weatherNone
}

// ChooseWeather shows a weather entity, "" for none. The default is saved as the default, so a later
// change of the default follows.
func (f *Feature) ChooseWeather(entity string) {
	var saved string
	switch entity {
	case "":
		saved = config.WeatherOff
	case config.DefaultWeather:
		saved = ""
	default:
		saved = entity
	}
	if err := config.Set().Home().Weather(saved); err != nil {
		slog.Warn("home: saving the weather source failed", "err", err)
		return
	}
	slog.Info("home: weather source", "entity", entity)
	f.mu.Lock()
	f.forecast = nil
	f.mu.Unlock()
	f.weatherSel.Set(chosenOption(config.Get().Home))
	// A new entity's state is only sent once Home Assistant is asked for it again.
	f.rewire()
}

// NextWeather moves to the next option, for the settings sheet.
func (f *Feature) NextWeather() {
	opts := f.weatherSel.Options
	cur := chosenOption(config.Get().Home)
	i := slices.Index(opts, cur)
	f.ChooseWeather(optionEntity(opts[(i+1)%len(opts)]))
}

// WeatherSource is the chosen source as the sheet says it: its name when Home Assistant gave one.
func (f *Feature) WeatherSource() string { return f.weatherName(config.Get().Home.WeatherEntity()) }

// WeatherChoices is what the screen's weather list offers, as the select does: each source's entity
// ("" for none) and its name, with the index of the one in force, or -1.
func (f *Feature) WeatherChoices() (entities, names []string, cur int) {
	cur = -1
	chosen := chosenOption(config.Get().Home)
	for i, o := range f.weatherSel.Options {
		e := optionEntity(o)
		entities = append(entities, e)
		names = append(names, f.weatherName(e))
		if o == chosen {
			cur = i
		}
	}
	return entities, names, cur
}

// weatherName is a weather entity as the screen names it: its name when Home Assistant gave one.
func (f *Feature) weatherName(entity string) string {
	if entity == "" {
		return weatherNone
	}
	name, _ := hastate.Get().Value(entity, "friendly_name")
	if name == "" {
		f.mu.Lock()
		for _, e := range f.weathers {
			if e.ID == entity {
				name = e.Name
			}
		}
		f.mu.Unlock()
	}
	if entity == config.DefaultWeather && (name == "" || name == "Forecast Home") {
		return "Home Assistant forecast"
	}
	if name == "" {
		return entity
	}
	return name
}

// refreshSources asks Home Assistant which weather entities it has, and offers them. A new list
// only reaches Home Assistant at the next connection, so one is asked for only when it changed.
func (f *Feature) refreshSources() {
	f.mu.Lock()
	due := time.Since(f.weathersAt) > sourcesEvery
	f.mu.Unlock()
	if !due || !hass.Get().Ready() {
		return
	}
	list, err := hass.Get().Entities("weather")
	if err != nil {
		slog.Warn("home: listing weather entities", "err", err)
		return
	}
	f.mu.Lock()
	f.weathers, f.weathersAt = list, time.Now()
	f.mu.Unlock()
	ids := make([]string, 0, len(list))
	for _, e := range list {
		ids = append(ids, e.ID)
	}
	if !slices.Equal(ids, config.Get().Home.WeatherSources) {
		if err := config.Set().Home().WeatherSources(ids); err != nil {
			slog.Warn("home: saving the weather sources failed", "err", err)
		}
	}
	opts := weatherOptions(config.Get().Home)
	if slices.Equal(opts, f.weatherSel.Options) {
		return
	}
	slog.Info("home: weather sources", "options", opts)
	f.weatherSel.Options = opts
	f.rewire()
}
