package hass

import (
	"context"
	"encoding/json"
)

// Area is a room as Home Assistant's area registry has it.
type Area struct {
	ID       string `json:"area_id"`
	Name     string `json:"name"`
	Floor    string `json:"floor_id"`
	Icon     string `json:"icon"`
	Temp     string `json:"temperature_entity_id"`
	Humidity string `json:"humidity_entity_id"`
}

// Floor is a level of the house, which areas sit on.
type Floor struct {
	ID    string `json:"floor_id"`
	Name  string `json:"name"`
	Level *int   `json:"level"`
}

// Device is a device's area, which its entities are in unless they say otherwise.
type Device struct {
	ID   string `json:"id"`
	Area string `json:"area_id"`
}

// Registered is an entity as the registry lists it for display: where it is, and whether it is one
// to show at all. Settings and diagnostics are not, and nor is anything hidden.
type Registered struct {
	ID       string `json:"ei"`
	Device   string `json:"di"`
	Area     string `json:"ai"`
	Category *int   `json:"ec"`
	Hidden   bool   `json:"hb"`
}

// Registries is the house's areas, floors, devices and entities.
func (l *Live) Registries(ctx context.Context) (areas []Area, floors []Floor, devices []Device, entities []Registered, err error) {
	get := func(kind string, into any) error {
		raw, err := l.Call(ctx, map[string]any{"type": kind})
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, into)
	}
	if err = get("config/area_registry/list", &areas); err != nil {
		return
	}
	// Older Home Assistant has no floors; the areas stand without them.
	_ = get("config/floor_registry/list", &floors)
	if err = get("config/device_registry/list", &devices); err != nil {
		return
	}
	var display struct {
		Entities []Registered `json:"entities"`
	}
	if err = get("config/entity_registry/list_for_display", &display); err != nil {
		return
	}
	entities = display.Entities
	return
}
