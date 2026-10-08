//go:build !dot

package dashboard

import (
	"encoding/json"
	"maps"
	"testing"
)

// A card's action is called with its data and target, and on the card's entity when it names none;
// an ESPHome device's own action never gets the entity, which it refuses. A picture of a camera whose
// tap shows it on a device is the case that broke.
func TestAnESPHomeActionIsNotGivenTheCardsEntity(t *testing.T) {
	for _, c := range []struct {
		name, card string
		want       map[string]any
	}{
		{"an ESPHome action with data", `{"tap_action": {"action": "perform-action",
			"perform_action": "esphome.hall_home_show_camera_sound",
			"data": {"entity": "camera.porch", "seconds": 60, "sound": "off"}}}`,
			map[string]any{"entity": "camera.porch", "seconds": float64(60), "sound": "off"}},
		{"a target of its own", `{"tap_action": {"action": "perform-action", "perform_action": "scene.turn_on",
			"target": {"entity_id": "scene.movie"}}}`,
			map[string]any{"entity_id": "scene.movie"}},
		{"data for the card's entity", `{"tap_action": {"action": "call-service", "service": "light.turn_on",
			"service_data": {"brightness_pct": 40}}}`,
			map[string]any{"brightness_pct": float64(40), "entity_id": "camera.porch"}},
		{"nothing but the service", `{"tap_action": {"action": "perform-action", "perform_action": "light.toggle"}}`,
			map[string]any{"entity_id": "camera.porch"}},
		{"a toggle", `{"tap_action": {"action": "toggle"}}`,
			map[string]any{"entity_id": "camera.porch"}},
	} {
		var card raw
		if err := json.Unmarshal([]byte(c.card), &card); err != nil {
			t.Fatal(err)
		}
		a := actionOf(card, "camera.porch")
		if a == nil {
			t.Fatalf("%s: no action", c.name)
		}
		if got := serviceData(*a); !maps.Equal(got, c.want) {
			t.Errorf("%s: called with %v, want %v", c.name, got, c.want)
		}
	}
}
