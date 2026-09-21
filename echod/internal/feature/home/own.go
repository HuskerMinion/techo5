package home

import (
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// The device's own radio stations: a name and a stream address, kept on the device and played by it.
//
// Everything else on the radio page is Home Assistant's — its Radio Browser lists, and the script
// favorites are wired to — so a device with no Home Assistant has nothing to play. These are the
// answer to that: the address goes straight to the player, and nothing in the path leaves the house
// except the stream itself. They are typed on the setup page, since a stream address is not
// something anybody wants to enter on a five-inch screen.

// OwnStations are the stations kept on the device.
func OwnStations() []config.Station { return config.Get().Home.Radio.Own }

// SetOwnStations replaces the list, keeping the ones that make sense: a name, an http address, and
// no more of them than the device holds.
func SetOwnStations(list []config.Station) error {
	clean := make([]config.Station, 0, len(list))
	for _, s := range list {
		s.Name = strings.TrimSpace(s.Name)
		s.URL = strings.TrimSpace(s.URL)
		if s.Name == "" || !playableURL(s.URL) {
			continue
		}
		if len(clean) == config.MaxOwnStations {
			break
		}
		clean = append(clean, s)
	}
	r := config.Get().Home.Radio
	r.Own = clean
	if err := config.Set().Home().Radio(r); err != nil {
		return err
	}
	slog.Info("home: the device's own stations", "count", len(clean))
	Get().Changed.Emit(struct{}{})
	return nil
}

// playableURL is whether an address is one the player can be handed: a web address, not a file on
// the device or something with a scheme of its own.
func playableURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// playOwn starts one of the device's own stations by name; false when it is not one of them.
func (f *Feature) playOwn(name string) bool {
	for _, s := range OwnStations() {
		if s.Name != name {
			continue
		}
		f.mu.Lock()
		f.chosen, f.listed, f.listedAt = name, "", time.Time{}
		f.mu.Unlock()
		slog.Info("home: playing a station kept on the device", "station", name)
		media.Get().PlayURL(s.URL)
		return true
	}
	return false
}
