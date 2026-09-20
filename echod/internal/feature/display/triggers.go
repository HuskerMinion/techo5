//go:build !dot

package display

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/triggers"
)

// What the screen listens for, for both screens: the Show's pages and the Spot's faces react to the
// same sentences, so the words live in lib/triggers once rather than in each. Which language's words
// are used is the Screen language setting; unset, every language is matched.

// The Screen language row's choices. Match all is first, and is what a device comes up in.
var (
	langOptions = []string{"Match all", "English", "Deutsch", "Español", "Français", "Italiano", "Nederlands"}
	langCodes   = []string{triggers.MatchAll, "en", "de", "es", "fr", "it", "nl"}
)

func langIndex() int {
	cur := config.Get().Screen.Language
	for i, c := range langCodes {
		if c == cur {
			return i
		}
	}
	return 0
}

// langSelect is Home Assistant's side of the same setting; the screen's row and this one show each
// other's changes, since both read the config.
func langSelect() *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_language",
			Name:     "Screen language",
			Icon:     "mdi:translate",
			Category: esphome.CategoryConfig,
		},
		Options: langOptions,
	}
	s.OnCommand = func(v string) {
		for i, o := range langOptions {
			if o == v {
				if err := config.Set().Screen().Language(langCodes[i]); err != nil {
					slog.Error("saving the screen language failed", "err", err)
					return
				}
				s.Set(v)
				return
			}
		}
	}
	s.Set(langOptions[langIndex()])
	return s
}

// aboutWeather is whether what was heard asked about the weather.
func aboutWeather(heard string) bool {
	return triggers.AboutWeather(heard, config.Get().Screen.Language)
}

// aboutRadar is whether what was heard asked for the rain map rather than the forecast.
func aboutRadar(heard string) bool {
	return triggers.AboutRadar(heard, config.Get().Screen.Language)
}

// aboutGoingHome is whether what was heard asked for the clock back.
func aboutGoingHome(heard string) bool {
	return triggers.AboutGoingHome(heard, config.Get().Screen.Language)
}
