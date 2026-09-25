package hass

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeStates answers Home Assistant's /api/states with what it has, as it sends it.
func fakeStates(t *testing.T, states []map[string]any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/states" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewEncoder(w).Encode(states); err != nil {
			t.Error(err)
		}
	}))
}

// Music Assistant's player for this device is found by the name the device announces to it, because what
// it used to be found by is not what Music Assistant puts there any more.
//
// active_queue reads like the entity id of the device's own player and is a queue id: `up<id>` on the
// device this was found on, which is that entity's unique_id as well. Compared with the device's own
// player entity id it matched nothing, so every caller got an empty string — the resume after a pause, the
// favorite for a Music Assistant track, and the leave before a stop in a grouped room. On the device it
// showed up as a station playing with Music Assistant's track still on the screen.
func TestTheMusicAssistantPlayerIsFoundByName(t *testing.T) {
	states := []map[string]any{
		{"entity_id": "media_player.hall_techo5_hall_speaker",
			"attributes": map[string]any{"friendly_name": "Techo5 Hall Speaker"}},
		// A queue named for the device as well, which is not the thing to ask to unjoin.
		{"entity_id": "media_player.hallway_queue",
			"attributes": map[string]any{"friendly_name": "Hallway",
				"app_id": "music_assistant", "mass_player_type": "queue"}},
		{"entity_id": "media_player.hallway",
			"attributes": map[string]any{"friendly_name": "Hallway",
				"app_id": "music_assistant", "mass_player_type": "player", "active_queue": "up0000abcd"}},
		{"entity_id": "media_player.porch",
			"attributes": map[string]any{"friendly_name": "Porch",
				"app_id": "music_assistant", "mass_player_type": "player", "active_queue": "up1111abcd"}},
		{"entity_id": "media_player.something_else",
			"attributes": map[string]any{"friendly_name": "Something else"}},
	}
	srv := fakeStates(t, states)
	defer srv.Close()
	c := &Client{acc: access{URL: srv.URL, Token: "secret"}, http: &http.Client{}}

	got, err := c.MusicAssistantFor("media_player.hall_techo5_hall_speaker", "Hallway")
	if err != nil {
		t.Fatal(err)
	}
	if got != "media_player.hallway" {
		t.Errorf("MusicAssistantFor = %q, want the player named for this device, not its queue", got)
	}

	// Nothing named for the device is no answer, rather than any media player going.
	srv2 := fakeStates(t, []map[string]any{
		{"entity_id": "media_player.porch",
			"attributes": map[string]any{"friendly_name": "Porch",
				"app_id": "music_assistant", "mass_player_type": "player"}},
	})
	defer srv2.Close()
	c2 := &Client{acc: access{URL: srv2.URL, Token: "secret"}, http: &http.Client{}}
	if got, err := c2.MusicAssistantFor("media_player.hall_techo5_hall_speaker", "Hallway"); err != nil || got != "" {
		t.Errorf("MusicAssistantFor = %q %v with nothing named for the device, want empty", got, err)
	}

	// And the older shape still answers first: a queue attribute holding this device's own player.
	srv3 := fakeStates(t, []map[string]any{
		{"entity_id": "media_player.legacy",
			"attributes": map[string]any{"friendly_name": "Not the device's name at all",
				"app_id": "music_assistant", "active_queue": "media_player.hall_techo5_hall_speaker"}},
	})
	defer srv3.Close()
	c3 := &Client{acc: access{URL: srv3.URL, Token: "secret"}, http: &http.Client{}}
	if got, err := c3.MusicAssistantFor("media_player.hall_techo5_hall_speaker", "Hallway"); err != nil ||
		got != "media_player.legacy" {
		t.Errorf("MusicAssistantFor = %q %v, want the queue match to go on answering", got, err)
	}

	// And a queue named for the device is no answer at all when no player is: it is not the thing to
	// resume or unjoin, so it is not the fallback either.
	srv4 := fakeStates(t, []map[string]any{
		{"entity_id": "media_player.hallway_queue",
			"attributes": map[string]any{"friendly_name": "Hallway",
				"app_id": "music_assistant", "mass_player_type": "queue"}},
	})
	defer srv4.Close()
	c4 := &Client{acc: access{URL: srv4.URL, Token: "secret"}, http: &http.Client{}}
	if got, err := c4.MusicAssistantFor("media_player.hall_techo5_hall_speaker", "Hallway"); err != nil ||
		got != "" {
		t.Errorf("MusicAssistantFor = %q %v for a queue alone, want empty", got, err)
	}
}
