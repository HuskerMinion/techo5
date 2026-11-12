package home

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
	"github.com/HuskerMinion/techo5/echod/internal/lib/radiometa"
	"github.com/ygelfand/go-esphome-device/api"
)

// The "now" entity's attributes are the song of the station it names, and of no other: attributes
// left from the station before are not this one's, and an entity with no song gives none.
func TestTheNowEntityCarriesTheSongOfTheStationItNames(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	const entity = "sensor.own_now_test_radio"
	if err := config.Set().Home().Radio(config.Radio{Now: entity}); err != nil {
		t.Fatal(err)
	}
	send := func(attribute, v string) {
		t.Helper()
		if err := hastate.Get().Handle(context.Background(), nil,
			&api.HomeAssistantStateResponse{EntityId: entity, Attribute: attribute, State: v}); err != nil {
			t.Fatal(err)
		}
	}

	send("", "SUB/WAVE")
	if n, ok := entityNow("SUB/WAVE"); ok {
		t.Errorf("no song yet, got %+v", n)
	}

	send("media_title", "Night Drive")
	send("media_artist", "Some Band")
	send("media_album_name", "None")
	send("entity_picture", "/local/covers/42.jpg")
	want := radiometa.Now{Title: "Night Drive", Artist: "Some Band", Art: "/local/covers/42.jpg"}
	if n, ok := entityNow("SUB/WAVE"); !ok || n != want {
		t.Errorf("got %+v, %v; want %+v", n, ok, want)
	}
	if n, ok := entityNow(" sub/wave "); !ok || n != want {
		t.Errorf("the name in another case and with spaces: got %+v, %v", n, ok)
	}
	if n, ok := entityNow("KXYZ"); ok {
		t.Errorf("another station took this one's song: %+v", n)
	}

	send("", "KXYZ")
	if n, ok := entityNow("KXYZ"); !ok || n != want {
		// The entity names the new station: what it carries is taken for it, as the user's sensor says.
		t.Errorf("got %+v, %v for the station the entity now names", n, ok)
	}

	if err := config.Set().Home().Radio(config.Radio{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := entityNow("KXYZ"); ok {
		t.Error("a song with no now entity wired")
	}
}

// A value for the "now" entity, a song attribute included, looks at the song at once.
func TestANewSongOnTheNowEntityPokesTheRadio(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	const entity = "sensor.own_now_test_poke"
	if err := config.Set().Home().Radio(config.Radio{Now: entity}); err != nil {
		t.Fatal(err)
	}
	f := &Feature{metaPoke: make(chan struct{}, 1)}
	f.nowChanged(hastate.Update{Key: hastate.Key{Entity: "sensor.something_else"}, Value: "x"})
	select {
	case <-f.metaPoke:
		t.Fatal("another entity poked the radio")
	default:
	}
	f.nowChanged(hastate.Update{Key: hastate.Key{Entity: entity, Attribute: "media_title"}, Value: "Night Drive"})
	select {
	case <-f.metaPoke:
	default:
		t.Fatal("a new song did not poke the radio")
	}
}

// The entity names a station whether or not it carries a song at the moment, so a station it has given
// a song stays with it between songs.
func TestTheNowEntityNamesItsStationBetweenSongs(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	const entity = "sensor.own_now_test_between"
	if err := config.Set().Home().Radio(config.Radio{Now: entity}); err != nil {
		t.Fatal(err)
	}
	if err := hastate.Get().Handle(context.Background(), nil,
		&api.HomeAssistantStateResponse{EntityId: entity, State: "SUB/WAVE"}); err != nil {
		t.Fatal(err)
	}
	if !entityNames("sub/wave") || entityNames("KXYZ") || entityNames("") {
		t.Error("the entity's station named wrongly")
	}
	if _, ok := entityNow("SUB/WAVE"); ok {
		t.Error("a song with no song attributes")
	}
}
