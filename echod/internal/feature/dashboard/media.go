//go:build !dot

package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// A media player's sheet, for the screen to offer when a finger rests on its tile: what it is playing
// and how loud, the music to put on it, and the other speakers to play it on as well.
//
// The music is Music Assistant's, and so is the grouping where it plays. A tile can show a player Music
// Assistant also plays to under its own entity (a Sonos, a Cast device): the sheet then plays and
// groups through Music Assistant's player of the same name, and keeps the tile's own player for the
// rest. Without Music Assistant, a player Home Assistant can group itself (a Sonos) is grouped there,
// with the others that can be, and there is no music to choose.

// MediaNow is a media player as the sheet shows it.
type MediaNow struct {
	Entity string
	Name   string
	State  string // playing, paused, idle, off, ...
	Title  string
	Artist string
	// Volume is from 0 to 1, or -1 when the player does not say.
	Volume float64
	// Members is what it plays together with, itself included.
	Members []string
}

// Playing is whether it is playing now.
func (m MediaNow) Playing() bool { return m.State == "playing" }

// MediaChoice is music the sheet offers: one of Music Assistant's favorites.
type MediaChoice struct {
	Name string
	URI  string
	Kind string // playlist, radio, album, artist, track
}

// Speaker is another player that music can be grouped onto, and how loud it is: from 0 to 1, or -1
// when it does not say.
type Speaker struct {
	Entity string
	Name   string
	Volume float64
}

// MediaLists is what the sheet offers besides the controls.
type MediaLists struct {
	// MusicAssistant is whether Music Assistant is there at all.
	MusicAssistant bool
	// Target is the player music is put on: the tile's own when Music Assistant plays to it directly,
	// Music Assistant's player of the same name when not, and empty when there is none.
	Target    string
	Favorites []MediaChoice
	// Group is the player the speakers are grouped with, Target or the tile's own; empty when it
	// cannot be grouped.
	Group    string
	Speakers []Speaker
	// Members is what Group plays together with now, itself included.
	Members []string
}

// groupingFeature is MediaPlayerEntityFeature.GROUPING: a player that can be joined to others.
const groupingFeature = 524288

// MediaNow is what a media player on the drawn page is doing, from what Home Assistant last said. It
// is false for anything that is not a media player there.
func (f *Feature) MediaNow(entity string) (MediaNow, bool) {
	if !strings.HasPrefix(entity, "media_player.") {
		return MediaNow{}, false
	}
	f.mu.Lock()
	s := f.drawn
	f.mu.Unlock()
	if s == nil {
		return MediaNow{}, false
	}
	s.mu.Lock()
	e, ok := s.states[entity]
	s.mu.Unlock()
	if !ok {
		return MediaNow{}, false
	}
	return mediaNow(e), true
}

// mediaNow reads a media player's state.
func mediaNow(e hass.LiveEntity) MediaNow {
	m := MediaNow{Entity: e.ID, State: e.State, Volume: -1}
	m.Name, _ = e.Attrs["friendly_name"].(string)
	m.Title, _ = e.Attrs["media_title"].(string)
	m.Artist, _ = e.Attrs["media_artist"].(string)
	if v, ok := e.Attrs["volume_level"].(float64); ok {
		m.Volume = v
	}
	if g, ok := e.Attrs["group_members"].([]any); ok {
		for _, x := range g {
			if s, ok := x.(string); ok {
				m.Members = append(m.Members, s)
			}
		}
	}
	return m
}

// player is one media player as Home Assistant lists it, for finding Music Assistant's.
type player struct {
	entity, name, mass string
	features           int
	available          bool
}

// maPlayers reads Music Assistant's players from a list of states.
func maPlayers(states []hass.LiveEntity) []player {
	var out []player
	for _, e := range states {
		if !strings.HasPrefix(e.ID, "media_player.") {
			continue
		}
		p := player{entity: e.ID, available: e.State != "unavailable"}
		p.name, _ = e.Attrs["friendly_name"].(string)
		p.mass, _ = e.Attrs["mass_player_type"].(string)
		if n, ok := e.Attrs["supported_features"].(float64); ok {
			p.features = int(n)
		}
		if p.mass == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// mediaTarget is the player music for entity is put on: entity itself when it is one of Music
// Assistant's, else Music Assistant's player of the same name. Empty when there is none.
func mediaTarget(entity, name string, players []player) string {
	for _, p := range players {
		if p.entity == entity {
			return entity
		}
	}
	var found string
	for _, p := range players {
		if p.mass != "player" || !p.available || !strings.EqualFold(strings.TrimSpace(p.name), strings.TrimSpace(name)) {
			continue
		}
		// The one that can be grouped, when there are two of a name: an older provider's entry
		// lingers beside the current one.
		if found == "" || p.features&groupingFeature != 0 {
			found = p.entity
		}
	}
	return found
}

// groupable is the players target can be grouped with: Music Assistant's own, there, able to join,
// in name order.
func groupable(target string, players []player) []Speaker {
	var out []Speaker
	for _, p := range players {
		if p.entity == target || p.mass != "player" || !p.available || p.features&groupingFeature == 0 {
			continue
		}
		out = append(out, Speaker{Entity: p.entity, Name: p.name})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// allStates is every entity's state, as Home Assistant has it now.
func allStates() ([]hass.LiveEntity, error) {
	b, err := hass.Get().Fetch("/api/states")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID    string         `json:"entity_id"`
		State string         `json:"state"`
		Attrs map[string]any `json:"attributes"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("reading Home Assistant's states: %w", err)
	}
	states := make([]hass.LiveEntity, len(raw))
	for i, r := range raw {
		states[i] = hass.LiveEntity{ID: r.ID, State: r.State, Attrs: r.Attrs}
	}
	return states, nil
}

// A tile's play or pause, next and back, on a player Music Assistant also plays to under its own
// entity: while Music Assistant is playing there, they go to Music Assistant's player, which reaches
// every speaker grouped in. Sent to the tile's own player, they stopped that speaker alone and left
// the others playing. While Music Assistant is not (the speaker plays from its own app), they stay
// with the tile's player, which is the one playing.

// routesFor is how long which Music Assistant player goes with which tile's player is kept.
const routesFor = 10 * time.Minute

var routes struct {
	sync.Mutex
	to map[string]string
	at time.Time
}

// transportTo is the player a tile's play or pause, next or back go to.
func transportTo(entity string) string {
	target := maPlayerFor(entity)
	if target == "" || target == entity {
		return entity
	}
	st, err := hass.Get().State(target)
	if err != nil {
		return entity
	}
	return routeTo(entity, target, st.State)
}

// routeTo is entity's commands' player given Music Assistant's player of the same name and what it
// is doing: Music Assistant's while it plays or is paused, else the tile's own.
func routeTo(entity, target, targetState string) string {
	if target != "" && (targetState == "playing" || targetState == "paused") {
		return target
	}
	return entity
}

// maPlayerFor is Music Assistant's player for a tile's player, as last worked out.
func maPlayerFor(entity string) string {
	routes.Lock()
	defer routes.Unlock()
	if routes.to == nil || time.Since(routes.at) > routesFor {
		states, err := allStates()
		if err != nil {
			return routes.to[entity]
		}
		players := maPlayers(states)
		routes.to, routes.at = map[string]string{}, time.Now()
		for _, e := range states {
			if strings.HasPrefix(e.ID, "media_player.") {
				name, _ := e.Attrs["friendly_name"].(string)
				routes.to[e.ID] = mediaTarget(e.ID, name, players)
			}
		}
	}
	return routes.to[entity]
}

// favorites is how long Music Assistant's favorites are kept before they are asked for again.
const favoritesFor = 10 * time.Minute

var favs struct {
	sync.Mutex
	list []MediaChoice
	at   time.Time
}

// LoadMedia asks Home Assistant for what the sheet of entity offers: Music Assistant's player for it,
// its favorites and the speakers it can group with. It can take a few seconds; the sheet shows the
// controls meanwhile.
func LoadMedia(entity, name string) (MediaLists, error) {
	states, err := allStates()
	if err != nil {
		return MediaLists{}, err
	}
	players := maPlayers(states)
	l := MediaLists{MusicAssistant: len(players) > 0, Target: mediaTarget(entity, name, players)}
	if l.Target != "" {
		l.Group, l.Speakers = l.Target, groupable(l.Target, players)
	} else if speakers, ok := haGroupable(entity, states); ok {
		l.Group, l.Speakers = entity, speakers
	}
	byID := make(map[string]hass.LiveEntity, len(states))
	for _, e := range states {
		byID[e.ID] = e
	}
	l.Members = mediaNow(byID[l.Group]).Members
	for i := range l.Speakers {
		l.Speakers[i].Volume = mediaNow(byID[l.Speakers[i].Entity]).Volume
	}
	if l.Target == "" {
		return l, nil // nothing Music Assistant plays to: no music to choose
	}
	l.Favorites, err = maFavorites()
	return l, err
}

// haGroupable is, for a player Home Assistant can group itself, the others it can group with: players
// that say what they are grouped with and can be grouped, there, and not Music Assistant's (which group
// among themselves). False when entity cannot be grouped.
func haGroupable(entity string, states []hass.LiveEntity) ([]Speaker, bool) {
	groups := func(e hass.LiveEntity) bool {
		n, _ := e.Attrs["supported_features"].(float64)
		_, says := e.Attrs["group_members"]
		_, ma := e.Attrs["mass_player_type"]
		return strings.HasPrefix(e.ID, "media_player.") && says && !ma && int(n)&groupingFeature != 0
	}
	found := false
	var out []Speaker
	for _, e := range states {
		if !groups(e) {
			continue
		}
		if e.ID == entity {
			found = true
			continue
		}
		if e.State == "unavailable" {
			continue
		}
		name, _ := e.Attrs["friendly_name"].(string)
		out = append(out, Speaker{Entity: e.ID, Name: name})
	}
	if !found {
		return nil, false
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, true
}

// favoritesOf is how many of each kind are fetched: more than a page holds, which the sheet pages
// through, and fewer than anybody would page through on a screen this size.
var favoritesOf = map[string]int{"playlist": 40, "radio": 20}

// maFavorites is Music Assistant's favorite playlists and radio stations, or the ones played last when
// none is a favorite yet, as last fetched.
func maFavorites() ([]MediaChoice, error) {
	favs.Lock()
	defer favs.Unlock()
	if favs.list != nil && time.Since(favs.at) < favoritesFor {
		return favs.list, nil
	}
	entry, err := maEntry()
	if err != nil {
		return favs.list, err
	}
	library := func(favorite bool) ([]MediaChoice, error) {
		var out []MediaChoice
		for _, kind := range []string{"playlist", "radio"} {
			// The ones played last first: what is played often is on the first page.
			data := map[string]any{"config_entry_id": entry, "media_type": kind, "limit": favoritesOf[kind],
				"order_by": "last_played_desc"}
			if favorite {
				data["favorite"] = true
			}
			r, err := hass.Get().CallResponse("music_assistant", "get_library", data)
			if err != nil {
				return nil, err
			}
			out = append(out, libraryItems(r, kind)...)
		}
		return out, nil
	}
	out, err := library(true)
	if err == nil && len(out) == 0 {
		// Nothing marked a favorite yet: what was played last, so the sheet is of use from the start.
		out, err = library(false)
	}
	if err != nil {
		return favs.list, err
	}
	favs.list, favs.at = out, time.Now()
	return out, nil
}

// cleanName is a library name the screen's font can draw: emoji and other symbols out, and the
// spaces they leave closed up.
func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r > 0xFFFF || (r >= 0x2190 && r <= 0x2BFF) || (r >= 0xFE00 && r <= 0xFE0F) || r == 0x200D {
			return -1
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// libraryItems reads get_library's answer.
func libraryItems(r map[string]any, kind string) []MediaChoice {
	items, _ := r["items"].([]any)
	var out []MediaChoice
	for _, it := range items {
		m, _ := it.(map[string]any)
		name, _ := m["name"].(string)
		name = cleanName(name)
		uri, _ := m["uri"].(string)
		if name == "" || uri == "" {
			continue
		}
		k, _ := m["media_type"].(string)
		if k == "" {
			k = kind
		}
		out = append(out, MediaChoice{Name: name, URI: uri, Kind: k})
	}
	return out
}

// maEntry is Music Assistant's config entry, which its services are called with.
func maEntry() (string, error) {
	b, err := hass.Get().Fetch("/api/config/config_entries/entry?domain=music_assistant")
	if err != nil {
		return "", err
	}
	var entries []struct {
		ID    string `json:"entry_id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(b, &entries); err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.State == "loaded" {
			return e.ID, nil
		}
	}
	return "", errors.New("Music Assistant is not set up in Home Assistant")
}
