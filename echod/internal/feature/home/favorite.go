package home

import (
	"errors"
	"log/slog"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// FavoriteNow saves what this device is playing to the favorites it belongs in: a Music Assistant
// track to Music Assistant's, by pressing its "favorite current song" button for this speaker; a
// station of the house's radio to Radio Favorites, by name, through the script that finds its stream.
// The same two the favorite-by-voice automation uses, but it knows which station this speaker is
// playing, where Home Assistant only knows the last one started anywhere in the house.
func (f *Feature) FavoriteNow() error {
	if _, _, _, held := media.Get().Held(); media.Get().Carried() || held {
		ma, err := musicAssistantPlayer()
		if err != nil {
			return err
		}
		if ma == "" {
			return errors.New("no Music Assistant player for this device")
		}
		button, err := hass.Get().Render("{{ device_entities(device_id('" + ma + "')) | select('search', 'favorite_current_song') | list | first | default('') }}")
		if err != nil {
			return err
		}
		if !strings.HasPrefix(button, "button.") {
			return errors.New("Music Assistant has no favorite button for " + ma)
		}
		slog.Info("favorite: Music Assistant track", "button", button)
		return hass.Get().Call("button", "press", map[string]any{"entity_id": button})
	}
	station := f.Radio().Now
	if station == "" {
		return errors.New("nothing playing to save")
	}
	slog.Info("favorite: radio station", "station", station)
	return hass.Get().Call("script", "radio_favorite_add_station", map[string]any{"station": station})
}
