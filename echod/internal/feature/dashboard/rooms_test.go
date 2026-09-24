//go:build !dot && !spot

package dashboard

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// An entity goes in its own area, or else its device's; settings, diagnostics, hidden ones and
// domains a room does not show stay out. Rooms with nothing in them are left off.
func TestRoomsTakeTheRightEntities(t *testing.T) {
	cat := 0
	areas := []hass.Area{{ID: "kitchen", Name: "Kitchen"}, {ID: "den", Name: "Den"}, {ID: "attic", Name: "Attic"}}
	devices := []hass.Device{{ID: "d1", Area: "den"}}
	entities := []hass.Registered{
		{ID: "light.kitchen_ceiling", Area: "kitchen"},
		{ID: "switch.lamp", Device: "d1"},
		{ID: "light.den_moved", Device: "d1", Area: "kitchen"},
		{ID: "switch.den_setting", Device: "d1", Category: &cat},
		{ID: "light.den_hidden", Device: "d1", Hidden: true},
		{ID: "sensor.den_power", Device: "d1"},
	}
	plan, ids := planRooms(areas, nil, devices, entities)
	if len(plan) != 2 || plan[0].name != "Den" || plan[1].name != "Kitchen" {
		t.Fatalf("rooms %+v, want Den then Kitchen", plan)
	}
	if got := plan[0].entities; len(got) != 1 || got[0] != "switch.lamp" {
		t.Errorf("Den has %v, want only the lamp", got)
	}
	if got := plan[1].entities; len(got) != 2 {
		t.Errorf("Kitchen has %v, want both lights", got)
	}
	if len(ids) != 3 {
		t.Errorf("following %v", ids)
	}
}

func TestTiles(t *testing.T) {
	light := hass.LiveEntity{ID: "light.kitchen_ceiling", State: "on",
		Attrs: map[string]any{"friendly_name": "Kitchen ceiling", "brightness": 153.0}}
	tile, ok := tileOf(light, "Kitchen")
	if !ok || tile.Name != "Ceiling" || tile.Value != "On · 60%" || !tile.On || !tile.Tap || tile.Icon != "lightbulb" {
		t.Errorf("light tile %+v", tile)
	}
	motion := hass.LiveEntity{ID: "binary_sensor.hall_motion", State: "on", Attrs: map[string]any{"device_class": "motion"}}
	if _, ok := tileOf(motion, "Hall"); ok {
		t.Error("a motion sensor got a tile")
	}
	door := hass.LiveEntity{ID: "binary_sensor.back_door", State: "off", Attrs: map[string]any{"device_class": "door"}}
	if tile, ok := tileOf(door, "Hall"); !ok || tile.Value != "Closed" || tile.Tap || tile.Icon != "door-closed" {
		t.Errorf("door tile %+v", tile)
	}
	gone := hass.LiveEntity{ID: "switch.fan", State: "unavailable", Attrs: map[string]any{}}
	if tile, _ := tileOf(gone, "Den"); !tile.Gone || tile.Tap {
		t.Errorf("unavailable tile %+v", tile)
	}
	lock := hass.LiveEntity{ID: "lock.front", State: "locked", Attrs: map[string]any{}}
	if tile, _ := tileOf(lock, "Hall"); tile.Tap {
		t.Error("a lock is tappable")
	}
}
