package home

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// Which media player in Home Assistant is this device's is answered by the device registry, not by the
// name: an entity id says nothing about which device provides it, and a device that was renamed keeps the
// entity id it was first given while the id Home Assistant would build from the new name belongs to
// nobody. This is the shape that beat the name guess — a device that calls itself Hallway, which Home
// Assistant shows "Techo5 Hall Speaker" under media_player.hall_techo5_hall_speaker.
func TestTheMediaPlayerIsFoundByTheDeviceItBelongsTo(t *testing.T) {
	devices := []hass.Device{
		{ID: "dev-neighbor", Connections: [][]string{{"mac", "aa:bb:cc:dd:ee:01"}}},
		{ID: "dev-this", Connections: [][]string{{"mac", "aa:bb:cc:dd:ee:02"}}},
	}
	entities := []hass.RegistryEntity{
		{ID: "media_player.hall_techo5_hall_speaker", DeviceID: "dev-this", Platform: "esphome"},
		{ID: "media_player.hall", DeviceID: "dev-ma", Platform: "music_assistant"},
		{ID: "media_player.porch_speaker", DeviceID: "dev-neighbor", Platform: "esphome"},
	}

	if got := mine(devices, entities, "aa:bb:cc:dd:ee:02"); got != "media_player.hall_techo5_hall_speaker" {
		t.Errorf("mine(...) = %q, want the media player this device's own device provides", got)
	}
	// The address is what the two share, and it has to be matched however it was written: the registries
	// and the hardware file do not have to agree on case.
	if got := mine(devices, entities, "AA:BB:CC:DD:EE:02"); got != "media_player.hall_techo5_hall_speaker" {
		t.Errorf("mine(...) = %q for the same address in the other case", got)
	}
	if got := mine(devices, entities, "aa:bb:cc:dd:ee:09"); got != "" {
		t.Errorf("mine(...) = %q for an address no device carries", got)
	}
	if got := mine(devices, entities, ""); got != "" {
		t.Errorf("mine(...) = %q with no address to look for", got)
	}
}

// A node Home Assistant added twice leaves an entry behind with the address still on it, so two devices
// answer to the same MAC and only one of them has the entities. Any of them is a candidate: the first
// version kept the last, which on the device this was found on was the empty one, and the lookup came back
// with nothing while the real player sat on the other — the screen on "Starting…" again.
func TestTheMediaPlayerIsFoundWhenTwoDevicesShareTheAddress(t *testing.T) {
	devices := []hass.Device{
		{ID: "dev-live", Connections: [][]string{{"mac", "aa:bb:cc:dd:ee:02"}}},
		{ID: "dev-stale", Connections: [][]string{{"mac", "aa:bb:cc:dd:ee:02"}}},
	}
	entities := []hass.RegistryEntity{
		{ID: "media_player.hall_techo5_hall_speaker", DeviceID: "dev-live", Platform: "esphome"},
	}

	if got := mine(devices, entities, "aa:bb:cc:dd:ee:02"); got != "media_player.hall_techo5_hall_speaker" {
		t.Errorf("mine(...) = %q, want the media player on either device carrying the address", got)
	}
}

// A device with no media player of ESPHome's is no answer: the callers of this play stations through an
// ESPHome media player, and Music Assistant's own entity for the same device is a different one.
func TestOnlyAnEsphomeMediaPlayerAnswers(t *testing.T) {
	devices := []hass.Device{{ID: "dev-this", Connections: [][]string{{"mac", "aa:bb:cc:dd:ee:02"}}}}
	entities := []hass.RegistryEntity{
		{ID: "media_player.hall", DeviceID: "dev-this", Platform: "music_assistant"},
		{ID: "switch.hall_sendspin", DeviceID: "dev-this", Platform: "esphome"},
	}
	if got := mine(devices, entities, "aa:bb:cc:dd:ee:02"); got != "" {
		t.Errorf("mine(...) = %q, want nothing: that device has no ESPHome media player", got)
	}
}

// The name comes second, and it is the name Home Assistant shows rather than the one the device calls
// itself: Home Assistant keeps the two in step, and the entity id is built from whichever it has.
func TestTheMediaPlayerIsFoundByNameWhenTheRegistryCannotSay(t *testing.T) {
	players := []hass.Entity{
		{ID: "media_player.hall", Name: "Hallway"},
		{ID: "media_player.techo5_hall_speaker", Name: "Techo5 Hall Speaker"},
	}

	if got := named(players, "Techo5 Hall"); got != "media_player.techo5_hall_speaker" {
		t.Errorf("named(...) = %q, want the player named for the device", got)
	}
	if got := named(players, "Hallway"); got != "" {
		t.Errorf("named(...) = %q, and nothing there is named for that device", got)
	}
}

// The last resort is only worth taking when the entity it names is one Home Assistant has; the caller
// says so out loud when it is not, because a play request to an entity nobody has is answered with
// success.
func TestTheGuessIsOnlyTrustedWhenHomeAssistantHasIt(t *testing.T) {
	players := []hass.Entity{
		{ID: "media_player.hall", Name: "Hallway"},
		{ID: "media_player.techo5_hall_speaker", Name: "Techo5 Hall Speaker"},
	}

	if !has(players, "media_player.hall") {
		t.Error("has(...) missed a player that is there")
	}
	if has(players, "media_player.hallway_speaker") {
		t.Error("has(...) found the guess among the players, and it is not there")
	}
}
