package home

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Stations without any setup: Home Assistant's Radio Browser integration, which a new installation
// adds on its own, lists the stations within 100 km of home and the most popular ones. The lists come
// over Home Assistant's websocket API with the device's token, and a station plays through
// media_player.play_media on this device's own player, which Home Assistant resolves and converts.
// The stations wired with home_radio stay the Favorites list and play exactly as before.

const (
	// listsEvery is how long a fetched list is kept.
	listsEvery = 12 * time.Hour
	// listRetry is how long a failed fetch waits before the next.
	listRetry = time.Minute

	// popularMax keeps the popular list to pages a finger will turn: Radio Browser gives 250.
	popularMax = 56
)

// station is one entry of a Radio Browser list.
type station struct {
	Name, ID, Kind string
}

type radioList struct {
	stations []station
	at       time.Time
	err      string
	busy     bool
}

type radioLists struct {
	local, popular radioList
}

// RadioSources are the lists this device can show, in the order the sheet steps through them.
func RadioSources() []string {
	var out []string
	if config.Get().Home.Radio.Configured() {
		out = append(out, config.RadioFavorites)
	}
	if len(config.Get().Home.Radio.Own) > 0 {
		out = append(out, config.RadioOwn)
	}
	if hass.Get().Ready() {
		out = append(out, config.RadioLocal, config.RadioPopular)
	}
	return out
}

// radioSource is the list shown: the one chosen when it is available, else the first there is.
func radioSource() string {
	sources := RadioSources()
	if len(sources) == 0 {
		return ""
	}
	chosen := config.Get().Home.RadioSource
	for _, s := range sources {
		if s == chosen {
			return s
		}
	}
	return sources[0]
}

// SetRadioSource shows one list by name, if this device has it.
func (f *Feature) SetRadioSource(source string) {
	for _, s := range RadioSources() {
		if s != source {
			continue
		}
		if err := config.Set().Home().RadioSource(source); err != nil {
			slog.Warn("home: saving the radio list failed", "err", err)
		}
		f.fetchList(source)
		f.Changed.Emit(struct{}{})
		return
	}
}

// NextRadioSource steps the radio page to the next list.
func (f *Feature) NextRadioSource() {
	sources := RadioSources()
	if len(sources) < 2 {
		return
	}
	cur := radioSource()
	next := sources[0]
	for i, s := range sources {
		if s == cur {
			next = sources[(i+1)%len(sources)]
		}
	}
	if err := config.Set().Home().RadioSource(next); err != nil {
		slog.Warn("home: saving the radio list failed", "err", err)
	}
	f.fetchList(next)
	f.Changed.Emit(struct{}{})
}

// SourceLabel is how the sheet names a list.
func SourceLabel(source string) string {
	switch source {
	case config.RadioFavorites:
		return "Favorites"
	case config.RadioLocal:
		return "Local stations"
	case config.RadioPopular:
		return "Popular worldwide"
	case config.RadioOwn:
		return "On this device"
	}
	return source
}

func (f *Feature) list(source string) *radioList {
	switch source {
	case config.RadioLocal:
		return &f.lists.local
	case config.RadioPopular:
		return &f.lists.popular
	}
	return nil
}

// fetchList gets a Radio Browser list in the background, unless a fresh one is at hand.
func (f *Feature) fetchList(source string) {
	f.mu.Lock()
	l := f.list(source)
	keep := listsEvery
	if l != nil && l.err != "" {
		keep = listRetry // the page asks every frame; a failing Home Assistant is not asked that often
	}
	if l == nil || l.busy || (!l.at.IsZero() && time.Since(l.at) < keep) {
		f.mu.Unlock()
		return
	}
	l.busy = true
	f.mu.Unlock()
	go func() {
		stations, err := browseStations(source)
		f.mu.Lock()
		l.busy, l.at = false, time.Now()
		if err != nil {
			l.err = err.Error()
			slog.Warn("radio: listing stations", "list", source, "err", err)
		} else {
			l.stations, l.err = stations, ""
			slog.Info("radio: stations listed", "list", source, "count", len(stations))
		}
		f.mu.Unlock()
		f.Changed.Emit(struct{}{})
	}()
}

func browseStations(source string) ([]station, error) {
	m, err := hass.Get().Browse(context.Background(), "media-source://radio_browser/"+source)
	if err != nil {
		return nil, err
	}
	return stationsOf(m, source), nil
}

// stationsOf is the playable entries of a listing, names tidied, the popular list cut short.
func stationsOf(m hass.Media, source string) []station {
	var out []station
	seen := map[string]bool{}
	for _, c := range m.Children {
		name := strings.Join(strings.Fields(c.Title), " ")
		if !c.CanPlay || name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		out = append(out, station{Name: name, ID: c.ID, Kind: c.Kind})
		if source == config.RadioPopular && len(out) == popularMax {
			break
		}
	}
	return out
}

// playListed plays a Radio Browser station by its name in the list shown; false when it is not one.
func (f *Feature) playListed(source, name string) bool {
	f.mu.Lock()
	l := f.list(source)
	var st station
	if l != nil {
		for _, s := range l.stations {
			if s.Name == name {
				st = s
			}
		}
	}
	if st.ID == "" {
		f.mu.Unlock()
		return false
	}
	f.chosen, f.listed, f.listedAt = name, name, time.Now()
	f.mu.Unlock()
	go func() {
		if err := hass.Get().PlayMedia(speakerEntity(), st.ID, st.Kind); err != nil {
			slog.Warn("radio: playing a station", "station", name, "err", err)
			f.mu.Lock()
			if f.chosen == name {
				f.chosen = ""
			}
			f.mu.Unlock()
			f.Changed.Emit(struct{}{})
			return
		}
		slog.Info("radio: station asked for", "station", name, "list", source)
	}()
	return true
}

// listedFor is how long a station tapped in a Radio Browser list names the stream that starts: the
// stream comes through Home Assistant's proxy, so its URL does not say.
const listedFor = time.Minute

// callFavorite plays a station through the script wired with home_radio. It is handed to a goroutine
// rather than done where it was asked for: naming this device's media player can wait on Home Assistant
// — the registries are a websocket away — and the caller is a finger on the screen. playListed already
// starts its own request the same way.
//
// Naming the player is also what lets two quick taps arrive out of order: the second sets the chosen
// station while the first is still looking the player up, and the first then reaches Home Assistant after
// the second and starts the station the finger has moved off. Only the station still chosen is asked for.
func (f *Feature) callFavorite(h config.Radio, station string) {
	safe.Go("radio: playing a favorite", func() {
		speaker := speakerEntity()
		f.mu.Lock()
		chosen := f.chosen
		f.mu.Unlock()
		if chosen != station {
			return
		}
		component.CallService.Emit(component.Call{
			Service: h.Service,
			Data:    map[string]string{h.Field: askFor(station), h.SpeakerField: speaker},
		})
	})
}
