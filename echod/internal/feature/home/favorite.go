package home

import (
	"errors"
	"log/slog"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// FavoriteNow saves what this device is playing to the favorites it belongs in: a Music Assistant
// track to Music Assistant's, by pressing its "favorite current song" button for this speaker; a
// station of the house's radio to Radio Favorites, by name, through the script that finds its stream;
// or, when radio favorites are not wired in Home Assistant, a stream this device is playing itself to
// the stations kept on the device.
func (f *Feature) FavoriteNow() error {
	if Following() {
		// Another player's song: its own favorite button, when it is one of Music Assistant's.
		return favoriteOn(shownFollowed())
	}
	if _, _, _, held := media.Get().Held(); media.Get().Carried() || held {
		ma, err := musicAssistantPlayer()
		if err != nil {
			return err
		}
		if ma == "" {
			return errors.New("no Music Assistant player for this device")
		}
		return favoriteOn(ma)
	}
	station := f.Radio().Now
	if station == "" {
		station, _ = f.NowPlaying()
	}
	if station == "" {
		return errors.New("nothing playing to save")
	}
	if !config.Get().Home.Radio.Configured() {
		f.mu.Lock()
		addr := f.ownURL
		f.mu.Unlock()
		if addr == "" {
			addr, _ = f.StreamOf(station)
		}
		if addr != "" {
			slog.Info("favorite: keeping station on this device", "station", station)
			return SaveStation(station, addr)
		}
	}
	slog.Info("favorite: radio station", "station", station)
	if err := addFavorite(station); err != nil {
		// Nothing on the screen said so before (#174): the star stayed as it was, and the reason was a
		// line in the log.
		f.favoriteFailed()
		return err
	}
	return nil
}

// addFavorite asks Home Assistant's script to add a station to the favorites; a variable for the tests.
var addFavorite = func(station string) error {
	return hass.Get().Call("script", "radio_favorite_add_station", map[string]any{"station": station})
}

// favoriteOn presses the "favorite current song" button Music Assistant gives a player's device.
func favoriteOn(player string) error {
	if !entityID.MatchString(player) {
		return errors.New("not an entity id: " + player)
	}
	button, err := hass.Get().Render("{{ device_entities(device_id('" + player + "')) | select('search', 'favorite_current_song') | list | first | default('') }}")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(button, "button.") {
		return errors.New("Music Assistant has no favorite button for " + player)
	}
	slog.Info("favorite: Music Assistant track", "button", button)
	return hass.Get().Call("button", "press", map[string]any{"entity_id": button})
}
