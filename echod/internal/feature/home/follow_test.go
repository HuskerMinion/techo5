package home

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// setFollowed points the follow at players with no connection behind them, as heard() leaves them.
func setFollowed(t *testing.T, entities ...string) {
	t.Helper()
	followed.mu.Lock()
	followed.players = nil
	for _, e := range entities {
		followed.players = append(followed.players, &player{entity: e})
	}
	followed.mu.Unlock()
	t.Cleanup(func() {
		followed.mu.Lock()
		for _, p := range followed.players {
			if p.pauseEnd != nil {
				p.pauseEnd.Stop()
			}
		}
		followed.players = nil
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

// The select offers none, the chosen players and Home Assistant's list, each once; an option that is not
// a media player follows nothing.
func TestFollowOptions(t *testing.T) {
	got := followOptions(config.Home{FollowPlayer: "media_player.den", FollowMore: []string{"media_player.patio"},
		PlayerSources: []string{"media_player.kitchen", "media_player.den"}})
	want := []string{followNone, "media_player.den", "media_player.patio", "media_player.kitchen"}
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
	followedPlayer(den).pausedAt = time.Now().Add(-followPausedFor - time.Second)
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

// Music Assistant pauses a Sonos by stopping it where it is: idle with the track still named (#345).
// An idle straight from playing is a pause, kept for followPausedFor and shown paused so the page's play
// resumes it; it stays one as the player's details change. An idle after a real pause is the queue
// emptied, and so is one with no track named, and another player's idle is not a pause at all.
func TestAMusicAssistantIdleAfterPlayingIsAPause(t *testing.T) {
	const office, den = "media_player.office", "media_player.den"
	setFollowed(t, office, den)
	f := Get()
	ma := map[string]any{"media_title": "Song One", "media_artist": "A Band", "app_id": "music_assistant"}
	paused := func() bool {
		followed.mu.Lock()
		defer followed.mu.Unlock()
		p := shownLocked()
		return p != nil && p.state == "paused"
	}

	f.heard(office, hass.LiveEntity{ID: office, State: "playing", Attrs: ma})
	f.heard(office, hass.LiveEntity{ID: office, State: "idle", Attrs: ma})
	if !Following() || !paused() {
		t.Fatal("a Music Assistant idle straight from playing is not shown paused")
	}
	f.heard(office, hass.LiveEntity{ID: office, State: "idle", Attrs: map[string]any{
		"media_title": "Song One", "media_artist": "A Band", "app_id": "music_assistant", "volume_level": 0.3}})
	if !Following() || !paused() {
		t.Error("a second idle (a volume change) ended the pause")
	}
	followed.mu.Lock()
	followedPlayer(office).pausedAt = time.Now().Add(-followPausedFor - time.Second)
	followed.mu.Unlock()
	if Following() {
		t.Error("an idle pause outlasts followPausedFor")
	}

	f.heard(office, hass.LiveEntity{ID: office, State: "playing", Attrs: ma})
	f.heard(office, hass.LiveEntity{ID: office, State: "paused", Attrs: ma})
	f.heard(office, hass.LiveEntity{ID: office, State: "idle", Attrs: ma})
	if Following() {
		t.Error("an idle after a real pause (the queue emptied) is still on the page")
	}

	f.heard(office, hass.LiveEntity{ID: office, State: "playing", Attrs: ma})
	f.heard(office, hass.LiveEntity{ID: office, State: "idle", Attrs: map[string]any{"app_id": "music_assistant"}})
	if Following() {
		t.Error("an idle with no track named is on the page")
	}

	track := map[string]any{"media_title": "Song Two", "media_artist": "A Band"}
	f.heard(den, hass.LiveEntity{ID: den, State: "playing", Attrs: track})
	f.heard(den, hass.LiveEntity{ID: den, State: "idle", Attrs: track})
	if Following() {
		t.Error("an idle from a player that is not Music Assistant's is taken as a pause")
	}
}

// With several players followed, the page shows the one that started playing last; when it stops, one
// still playing takes the page; Done on one hands the page to another; and the buttons and the lyrics'
// place go with whichever is on the page.
func TestSeveralFollowedPlayersTakeThePageInTurn(t *testing.T) {
	const kitchen, den = "media_player.kitchen", "media_player.den"
	setFollowed(t, kitchen, den)
	f := Get()
	play := func(e, title string, at float64) {
		f.heard(e, hass.LiveEntity{ID: e, State: "playing", Attrs: map[string]any{
			"friendly_name": e, "media_title": title, "media_artist": "A Band", "media_position": at}})
	}
	now := func() string { return withFollowed(Radio{}).Now }

	play(kitchen, "Song One", 1)
	if !Following() || now() != kitchen {
		t.Fatalf("one player playing: page shows %q", now())
	}
	time.Sleep(2 * time.Millisecond) // the den starts after the kitchen
	play(den, "Song Two", 2)
	if now() != den {
		t.Fatalf("the player that started last is not on the page: %q", now())
	}
	if p, ok := followedPos(); !ok || p.Title != "Song Two" {
		t.Errorf("lyrics place = %+v, %v; want the den's", p, ok)
	}
	if shownFollowed() != den {
		t.Errorf("buttons and favorite go to %q, want the den", shownFollowed())
	}

	play(kitchen, "Song Three", 3) // a new track on a player already playing is not a new start
	if now() != den {
		t.Errorf("a track change on the kitchen took the page from the den: %q", now())
	}

	f.heard(den, hass.LiveEntity{ID: den, State: "idle"})
	if now() != kitchen {
		t.Fatalf("the den stopped and the kitchen, still playing, did not take the page: %q", now())
	}

	play(den, "Song Two", 2)
	f.heard(den, hass.LiveEntity{ID: den, State: "paused", Attrs: map[string]any{"media_title": "Song Two", "media_artist": "A Band"}})
	if now() != kitchen {
		t.Errorf("a paused player is shown over one playing: %q", now())
	}

	f.DismissFollowed() // Done on the kitchen's track
	if now() != den {
		t.Errorf("Done on the kitchen did not hand the page to the paused den: %q", now())
	}
	f.DismissFollowed()
	if Following() {
		t.Error("Done on both still shows one")
	}
}

// Followed reads a config from before there could be several the same, and FollowPlayers keeps the first
// where a select and an older release look for it.
func TestFollowedPlayersAreKept(t *testing.T) {
	if got := (config.Home{FollowPlayer: "media_player.den"}).Followed(); len(got) != 1 || got[0] != "media_player.den" {
		t.Errorf("one from before = %v", got)
	}
	if got := (config.Home{FollowMore: []string{"media_player.x"}}).Followed(); got != nil {
		t.Errorf("more with no first = %v, want none", got)
	}
	h := config.Home{FollowPlayer: "media_player.a", FollowMore: []string{"media_player.b", "media_player.a", "", "media_player.c"}}
	if got := h.Followed(); len(got) != 3 || got[0] != "media_player.a" || got[1] != "media_player.b" || got[2] != "media_player.c" {
		t.Errorf("followed = %v", got)
	}
	if followOption(h) != "media_player.a" || followOption(config.Home{}) != followNone {
		t.Error("the select does not show the first chosen, or None")
	}
}

// Ticking another player keeps what was heard of those already followed: the song put away with Done
// stays away, and the page does not move.
func TestTickingAnotherKeepsTheFollowedState(t *testing.T) {
	const kitchen, den = "media_player.kitchen", "media_player.den"
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	setFollowed(t, kitchen)
	f := Get()
	f.heard(kitchen, hass.LiveEntity{ID: kitchen, State: "playing", Attrs: map[string]any{"media_title": "Song One", "media_artist": "A Band"}})
	f.DismissFollowed()
	if err := config.Set().Home().FollowPlayers([]string{kitchen, den}); err != nil {
		t.Fatal(err)
	}
	f.restartFollow()
	t.Cleanup(func() {
		followed.mu.Lock()
		if followed.cancel != nil {
			followed.cancel()
			followed.cancel = nil
		}
		followed.mu.Unlock()
	})
	if Following() {
		t.Error("ticking the den brought back the kitchen's song put away with Done")
	}
	followed.mu.Lock()
	n := len(followed.players)
	followed.mu.Unlock()
	if n != 2 {
		t.Errorf("following %d players, want 2", n)
	}
}
