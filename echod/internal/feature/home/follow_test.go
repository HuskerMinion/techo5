package home

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// setFollowed points the follow at a player with no connection behind it, as heard() leaves it.
func setFollowed(t *testing.T, entity string) {
	t.Helper()
	followed.mu.Lock()
	followed.entity, followed.state, followed.dismissed = entity, "", ""
	followed.title, followed.artist, followed.album, followed.name, followed.picture = "", "", "", "", ""
	followed.art, followed.thumb, followed.artFor = nil, nil, ""
	followed.played, followed.pausedAt = false, time.Time{}
	followed.mu.Unlock()
	t.Cleanup(func() {
		followed.mu.Lock()
		followed.entity, followed.state, followed.dismissed = "", "", ""
		followed.played, followed.pausedAt = false, time.Time{}
		if followed.pauseEnd != nil {
			followed.pauseEnd.Stop()
			followed.pauseEnd = nil
		}
		followed.mu.Unlock()
	})
}

// The followed player is on the page while it plays a named track and this device plays nothing; its
// state reaches the page as it is; Done puts it away until the next track; and a player that is merely
// on, or one that stopped, is nothing to show.
func TestTheFollowedPlayerIsOnThePageUntilItIsPutAway(t *testing.T) {
	const kitchen = "media_player.kitchen"
	setFollowed(t, kitchen)
	f := Get()

	f.heard(kitchen, hass.LiveEntity{ID: kitchen, State: "on", Attrs: map[string]any{"friendly_name": "Kitchen"}})
	if Following() {
		t.Fatal("a player that is on with nothing playing is on the page")
	}

	f.heard(kitchen, hass.LiveEntity{ID: kitchen, State: "playing", Attrs: map[string]any{
		"friendly_name": "Kitchen", "media_title": "Song One", "media_artist": "A Band", "media_album_name": "An Album",
	}})
	if !Following() {
		t.Fatal("a playing track is not on the page")
	}
	r := withFollowed(Radio{Chosen: "a station"})
	if !r.Followed || !r.Playing || r.Paused || r.Now != "Kitchen" || r.Title != "Song One" || r.Artist != "A Band" ||
		r.Album != "An Album" || r.Chosen != "" || !r.Music {
		t.Errorf("page = %+v", r)
	}

	f.heard(kitchen, hass.LiveEntity{ID: kitchen, State: "paused", Attrs: map[string]any{"media_title": "Song One", "media_artist": "A Band"}})
	if r := withFollowed(Radio{}); !r.Paused || r.Playing || r.Now != kitchen {
		t.Errorf("paused page = %+v (an unnamed player is named by its entity)", r)
	}

	f.DismissFollowed()
	if Following() {
		t.Error("Done did not put the track away")
	}
	f.heard(kitchen, hass.LiveEntity{ID: kitchen, State: "playing", Attrs: map[string]any{"media_title": "Song Two", "media_artist": "A Band"}})
	if !Following() {
		t.Error("the next track did not bring the page back")
	}

	f.heard(kitchen, hass.LiveEntity{ID: kitchen, State: "idle", Attrs: map[string]any{"media_title": "Song Two"}})
	if Following() {
		t.Error("an idle player is still on the page")
	}

	// A state for a player no longer followed is a late one from the last follow, and is dropped.
	f.heard("media_player.den", hass.LiveEntity{ID: "media_player.den", State: "playing", Attrs: map[string]any{"media_title": "Elsewhere"}})
	if Following() {
		t.Error("a player not followed got onto the page")
	}
}

// The select offers none, the chosen player and Home Assistant's list, each once; an option that is not
// a media player follows nothing.
func TestFollowOptions(t *testing.T) {
	got := followOptions(config.Home{FollowPlayer: "media_player.den", PlayerSources: []string{"media_player.kitchen", "media_player.den"}})
	want := []string{followNone, "media_player.den", "media_player.kitchen"}
	if len(got) != len(want) {
		t.Fatalf("options = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("options = %v, want %v", got, want)
		}
	}
	for v, want := range map[string]string{followNone: "", "light.lamp": "", " media_player.den ": "media_player.den",
		"media_player.x') }}{{ y": ""} {
		if got := followEntity(v); got != want {
			t.Errorf("followEntity(%q) = %q, want %q", v, got, want)
		}
	}
}

// A pause keeps the page only for a while: a Sonos that is stopped stays paused on its last track for
// good. A player first heard paused, as after a restart, is not put on the page until it plays, and a
// reconnect that hears the same pause again does not start it over.
func TestAPausedFollowedPlayerLetsThePageGo(t *testing.T) {
	const den = "media_player.den"
	setFollowed(t, den)
	f := Get()
	track := map[string]any{"media_title": "Song One", "media_artist": "A Band"}

	f.heard(den, hass.LiveEntity{ID: den, State: "paused", Attrs: track})
	if Following() {
		t.Fatal("a player only ever heard paused is on the page")
	}

	f.heard(den, hass.LiveEntity{ID: den, State: "playing", Attrs: track})
	f.heard(den, hass.LiveEntity{ID: den, State: "paused", Attrs: track})
	if !Following() {
		t.Fatal("a fresh pause took the page away")
	}

	// The pause began a while ago; hearing it again (a reconnect) keeps that time.
	followed.mu.Lock()
	followed.pausedAt = time.Now().Add(-followPausedFor - time.Second)
	followed.mu.Unlock()
	f.heard(den, hass.LiveEntity{ID: den, State: "unavailable"})
	f.heard(den, hass.LiveEntity{ID: den, State: "paused", Attrs: track})
	if Following() {
		t.Error("a long pause is still on the page")
	}

	f.heard(den, hass.LiveEntity{ID: den, State: "playing", Attrs: track})
	if !Following() {
		t.Error("playing again did not bring the page back")
	}
}
