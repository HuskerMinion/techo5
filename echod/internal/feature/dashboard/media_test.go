//go:build !dot

package dashboard

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The sheet plays and groups through Music Assistant: a player of its own directly, a player it also
// plays to (a Sonos, a Cast device) through its player of the same name - the one that can be grouped
// when an older one of that name lingers - and nothing for a player it does not know. The speakers
// offered are its other players that can be grouped, by name, never a group of its own or one that is
// gone.
func TestTheMediaSheetPlaysThroughMusicAssistant(t *testing.T) {
	ma := func(id, name, kind string, features float64, state string) hass.LiveEntity {
		return hass.LiveEntity{ID: id, State: state, Attrs: map[string]any{
			"friendly_name": name, "mass_player_type": kind, "supported_features": features}}
	}
	states := []hass.LiveEntity{
		ma("media_player.ma_kitchen", "Kitchen", "player", 8322623, "idle"),
		ma("media_player.kitchen_old", "Kitchen", "player", 7796279, "idle"),
		ma("media_player.ma_bath", "Bathroom", "player", 8322623, "playing"),
		ma("media_player.ma_deck", "Deck", "player", 8322623, "unavailable"),
		ma("media_player.ma_all", "Everywhere", "group", 8320575, "idle"),
		{ID: "media_player.sonos_kitchen", State: "idle", Attrs: map[string]any{"friendly_name": "Kitchen"}},
		{ID: "light.kitchen", State: "on", Attrs: map[string]any{"friendly_name": "Kitchen"}},
	}
	players := maPlayers(states)
	for _, c := range []struct{ entity, name, want string }{
		{"media_player.ma_bath", "Bathroom", "media_player.ma_bath"},
		{"media_player.sonos_kitchen", "Kitchen", "media_player.ma_kitchen"},
		{"media_player.tv", "TV", ""},
	} {
		if got := mediaTarget(c.entity, c.name, players); got != c.want {
			t.Errorf("%s: plays through %q, want %q", c.entity, got, c.want)
		}
	}
	want := []Speaker{{Entity: "media_player.ma_bath", Name: "Bathroom"}}
	if got := groupable("media_player.ma_kitchen", players); !reflect.DeepEqual(got, want) {
		t.Errorf("speakers %+v, want %+v", got, want)
	}
}

// What a player is doing is read from its state; its favorites from Music Assistant's library, each
// with what kind it is, leaving out anything without a name or an address.
func TestTheMediaSheetReadsThePlayerAndTheLibrary(t *testing.T) {
	now := mediaNow(hass.LiveEntity{ID: "media_player.ma_bath", State: "playing", Attrs: map[string]any{
		"friendly_name": "Bathroom", "media_title": "Slow Morning", "media_artist": "The Example Band", "volume_level": 0.4,
		"group_members": []any{"media_player.ma_bath", "media_player.ma_kitchen"}}})
	if now.Name != "Bathroom" || now.Title != "Slow Morning" || now.Artist != "The Example Band" || now.Volume != 0.4 ||
		len(now.Members) != 2 || !now.Playing() {
		t.Errorf("read as %+v", now)
	}
	if v := mediaNow(hass.LiveEntity{ID: "media_player.tv", State: "off"}).Volume; v != -1 {
		t.Errorf("a player that says no volume has %v, want -1", v)
	}
	items := libraryItems(map[string]any{"items": []any{
		map[string]any{"name": "Liked Songs", "uri": "library://playlist/155", "media_type": "playlist"},
		map[string]any{"name": "Radio One", "uri": "library://radio/3"},
		map[string]any{"name": "", "uri": "library://playlist/9"},
		map[string]any{"name": "SUNSET 🌴 IBIZA 🌅  DEEP ", "uri": "library://playlist/7"},
	}}, "radio")
	want := []MediaChoice{{"Liked Songs", "library://playlist/155", "playlist"}, {"Radio One", "library://radio/3", "radio"},
		{"SUNSET IBIZA DEEP", "library://playlist/7", "radio"}}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("favorites %+v, want %+v", items, want)
	}
}

// Without Music Assistant, a player Home Assistant groups itself is grouped with the others that say
// what they are grouped with and can be, there, by name; one that cannot be grouped has no speakers.
func TestAPlayerHomeAssistantGroupsIsGroupedWithoutMusicAssistant(t *testing.T) {
	sonos := func(id, name, state string) hass.LiveEntity {
		return hass.LiveEntity{ID: id, State: state, Attrs: map[string]any{
			"friendly_name": name, "supported_features": 8319551.0, "group_members": []any{id}}}
	}
	states := []hass.LiveEntity{
		sonos("media_player.office", "Office", "idle"),
		sonos("media_player.living_room", "Living Room", "playing"),
		sonos("media_player.bedroom", "Bedroom", "paused"),
		sonos("media_player.garage", "Garage", "unavailable"),
		{ID: "media_player.tv", State: "on", Attrs: map[string]any{"friendly_name": "TV", "supported_features": 22961.0}},
	}
	got, ok := haGroupable("media_player.living_room", states)
	want := []Speaker{{Entity: "media_player.bedroom", Name: "Bedroom"}, {Entity: "media_player.office", Name: "Office"}}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("speakers %+v (%v), want %+v", got, ok, want)
	}
	if _, ok := haGroupable("media_player.tv", states); ok {
		t.Error("a player that cannot be grouped was offered speakers")
	}
}

// A Sonos that Music Assistant also plays to is grouped through Music Assistant while Music Assistant
// has music there, a track a pause stopped included; while the Sonos plays from its own app, it is
// grouped the Sonos's own way, which takes that music along to the speakers grouped in.
func TestASpeakerPlayingFromElsewhereIsGroupedItsOwnWay(t *testing.T) {
	sonos := func(id, name, state string) hass.LiveEntity {
		return hass.LiveEntity{ID: id, State: state, Attrs: map[string]any{
			"friendly_name": name, "supported_features": 8319551.0, "group_members": []any{id}}}
	}
	ma := func(id, name, state, title string) hass.LiveEntity {
		return hass.LiveEntity{ID: id, State: state, Attrs: map[string]any{"friendly_name": name, "mass_player_type": "player",
			"supported_features": 8322623.0, "media_title": title, "group_members": []any{}}}
	}
	for _, c := range []struct {
		name                  string
		own, maState, maTitle string
		elsewhere, queued     bool
		group                 string
	}{
		{"the speaker plays from its own app", "playing", "idle", "Old Song", true, false, "media_player.sonos_kitchen"},
		{"and is paused there", "paused", "idle", "", true, false, "media_player.sonos_kitchen"},
		{"Music Assistant plays there", "playing", "playing", "Song", false, true, "media_player.ma_kitchen"},
		{"a pause stopped Music Assistant's group", "idle", "idle", "Song", false, true, "media_player.ma_kitchen"},
		{"nothing anywhere", "idle", "idle", "", false, false, "media_player.ma_kitchen"},
	} {
		states := []hass.LiveEntity{
			sonos("media_player.sonos_kitchen", "Kitchen", c.own),
			sonos("media_player.sonos_bath", "Bathroom", "idle"),
			ma("media_player.ma_kitchen", "Kitchen", c.maState, c.maTitle),
			ma("media_player.ma_bath", "Bathroom", "idle", ""),
		}
		l := listsFor("media_player.sonos_kitchen", "Kitchen", states)
		if l.Elsewhere != c.elsewhere || l.Queued != c.queued || l.Group != c.group || l.Target != "media_player.ma_kitchen" {
			t.Errorf("%s: elsewhere %v, queued %v, grouped by %s, music on %s; want %v, %v, %s and the Music Assistant player",
				c.name, l.Elsewhere, l.Queued, l.Group, l.Target, c.elsewhere, c.queued, c.group)
		}
		if want := "media_player.sonos_bath"; c.elsewhere && (len(l.Speakers) != 1 || l.Speakers[0].Entity != want) {
			t.Errorf("%s: speakers %+v, want the other Sonos", c.name, l.Speakers)
		}
	}
}

// A media player's tile plays and pauses while it plays, is paused or is idle: idle is where Music
// Assistant leaves a group it could not pause, and a play there goes on with its queue.
func TestAMediaPlayersTileTapsWhenIdle(t *testing.T) {
	for _, state := range []string{"playing", "paused", "idle"} {
		tile := describe(hass.LiveEntity{ID: "media_player.den", State: state}, "Den")
		if tile.Tap == nil || tile.Tap.Service != "media_player.media_play_pause" {
			t.Errorf("%s: tap %+v, want play or pause", state, tile.Tap)
		}
	}
	if tile := describe(hass.LiveEntity{ID: "media_player.den", State: "off"}, "Den"); tile.Tap != nil {
		t.Errorf("off: tap %+v, want none", tile.Tap)
	}
}

// A media player that says its volume and takes one slides it, from 0 to 100; one that is off, or
// that cannot be set, has no level.
func TestAMediaPlayersVolumeSlides(t *testing.T) {
	playing := hass.LiveEntity{ID: "media_player.den", State: "playing", Attrs: map[string]any{"volume_level": 0.35, "supported_features": 8320575.0}}
	a := adjustOf(playing, "media_player")
	if a == nil || a.Kind != "volume" || a.Value != 35 || a.Min != 0 || a.Max != 100 {
		t.Errorf("volume %+v, want 35 of 0 to 100", a)
	}
	for _, e := range []hass.LiveEntity{
		{ID: "media_player.tv", State: "off", Attrs: map[string]any{"supported_features": 22961.0}},
		{ID: "media_player.radio", State: "on", Attrs: map[string]any{"volume_level": 0.5, "supported_features": 1.0}},
	} {
		if a := adjustOf(e, "media_player"); a != nil {
			t.Errorf("%s slides %+v, want nothing", e.ID, a)
		}
	}
}

// A tile's play or pause goes to Music Assistant's player of the same name while Music Assistant plays
// or has paused there, so that it reaches the whole group, and while it holds a track a pause stopped;
// it stays with the tile's player while that plays from elsewhere, or when Music Assistant has nothing.
func TestATilesPlayPauseReachesTheGroup(t *testing.T) {
	const own, ma = "media_player.sonos_kitchen", "media_player.ma_kitchen"
	for _, c := range []struct{ own, target, state, title, want string }{
		{"playing", ma, "playing", "Song", ma},
		{"idle", ma, "paused", "Song", ma},
		{"idle", ma, "idle", "Song", ma},        // a group's pause stopped it: play goes on with the queue
		{"paused", ma, "idle", "Song", own},     // the speaker plays from its own app
		{"playing", ma, "idle", "", own},        // and Music Assistant has nothing
		{"idle", ma, "idle", "", own},           // nothing anywhere
		{"playing", "", "playing", "Song", own}, // no Music Assistant player for it
	} {
		if got := routeTo(own, c.own, c.target, c.state, c.title); got != c.want {
			t.Errorf("own %s, Music Assistant's %q %s %q: sent to %s, want %s", c.own, c.target, c.state, c.title, got, c.want)
		}
	}
}

// A tile's play or pause is sent without waiting for the look at Home Assistant that says which
// player it goes to: that is worked out with the call, off the finger's goroutine.
func TestATapDoesNotWaitForItsRoute(t *testing.T) {
	routed := make(chan string, 1)
	prevRoute, prevCall := routeTransport, callService
	routeTransport = func(string) string {
		time.Sleep(300 * time.Millisecond) // Home Assistant slow to answer
		return "media_player.ma_kitchen"
	}
	callService = func(_ *hass.Live, _ context.Context, _, _ string, data map[string]any) error {
		routed <- data["entity_id"].(string)
		return nil
	}
	t.Cleanup(func() { routeTransport, callService = prevRoute, prevCall })

	f := &Feature{}
	f.drawn = &session{f: f, src: noSource{}, live: &hass.Live{}, states: map[string]hass.LiveEntity{},
		pending: map[string]pending{}}
	start := time.Now()
	f.Tap(Action{Entity: "media_player.sonos_kitchen", Service: "media_player.media_play_pause"})
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Errorf("the tap took %v, waiting for the route", took)
	}
	select {
	case to := <-routed:
		if to != "media_player.ma_kitchen" {
			t.Errorf("sent to %s, want the routed player", to)
		}
	case <-time.After(2 * time.Second):
		t.Error("the call was never made")
	}
}

// noSource is a dashboard with nothing on it, for a session that only takes taps.
type noSource struct{}

func (noSource) load(context.Context, *hass.Live) (needs, error) { return needs{}, nil }
func (noSource) sections(look) []Section                         { return nil }
