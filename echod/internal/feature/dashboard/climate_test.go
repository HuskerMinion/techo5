//go:build !dot

package dashboard

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// A thermostat's tile has no tap of its own (the screen opens its sheet), and a level to slide only
// when it holds to one temperature; the sheet reads the one temperature, or the range it keeps
// between, its limits and its modes.
func TestAThermostatIsReadForItsSheet(t *testing.T) {
	modes := []any{"off", "heat", "cool", "heat_cool"}
	one := hass.LiveEntity{ID: "climate.hall", State: "heat", Attrs: map[string]any{"friendly_name": "Hall",
		"current_temperature": 68.0, "temperature": 70.0, "min_temp": 45.0, "max_temp": 95.0, "hvac_modes": modes}}
	tile := describe(one, "Hall", nil)
	if tile.Tap != nil || tile.Adjust == nil || tile.Adjust.Value != 70 || tile.Adjust.Step != 1 || tile.Entity != "climate.hall" {
		t.Errorf("one target: tile %+v, level %+v", tile, tile.Adjust)
	}
	c := climateNow(one)
	if !c.HasTarget || c.Target != 70 || c.HasRange || !c.HasCurrent || c.Current != 68 || c.Min != 45 || c.Max != 95 || c.Step != 1 ||
		!reflect.DeepEqual(c.Modes, []string{"off", "heat", "cool", "heat_cool"}) {
		t.Errorf("one target: %+v", c)
	}
	if v := c.Stepped(70, 1); v != 71 {
		t.Errorf("a step up from 70 is %v", v)
	}
	if v := c.Stepped(95, 1); v != 95 {
		t.Errorf("a step up from the top is %v", v)
	}

	both := hass.LiveEntity{ID: "climate.den", State: "heat_cool", Attrs: map[string]any{
		"target_temp_low": 20.0, "target_temp_high": 24.0, "min_temp": 7.0, "max_temp": 35.0, "target_temp_step": 0.5, "hvac_modes": modes}}
	if tile := describe(both, "Den", nil); tile.Tap != nil || tile.Adjust != nil {
		t.Errorf("a range: tile %+v has a tap or a level", tile)
	}
	c = climateNow(both)
	if c.HasTarget || !c.HasRange || c.Low != 20 || c.High != 24 || c.Step != 0.5 {
		t.Errorf("a range: %+v", c)
	}
	if v := c.Stepped(20, -1); v != 19.5 {
		t.Errorf("a step down from 20 is %v", v)
	}

	off := hass.LiveEntity{ID: "climate.attic", State: "off", Attrs: map[string]any{"hvac_modes": modes}}
	if c := climateNow(off); c.HasTarget || c.HasRange {
		t.Errorf("off: %+v", c)
	}
}

// The sheet sets a thermostat through Home Assistant: one temperature, a range, a mode; and its tile
// shows what was asked at once, a thermostat that has no level to slide included.
func TestTheThermostatSheetSetsThroughHomeAssistant(t *testing.T) {
	type call struct {
		service string
		data    map[string]any
	}
	var mu sync.Mutex
	var calls []call
	prev := callService
	callService = func(_ *hass.Live, _ context.Context, domain, service string, data map[string]any) error {
		mu.Lock()
		calls = append(calls, call{domain + "." + service, data})
		mu.Unlock()
		return nil
	}
	t.Cleanup(func() { callService = prev })

	f := &Feature{}
	s := &session{f: f, src: noSource{}, live: &hass.Live{}, states: map[string]hass.LiveEntity{}, pending: map[string]pending{}}
	f.drawn = s
	f.SetClimate("climate.hall", 71, 0)
	f.SetClimate("climate.den", 19.5, 24)
	f.SetHVACMode("climate.attic", "heat")

	want := []call{
		{"climate.set_temperature", map[string]any{"entity_id": "climate.hall", "temperature": 71.0}},
		{"climate.set_temperature", map[string]any{"entity_id": "climate.den", "target_temp_low": 19.5, "target_temp_high": 24.0}},
		{"climate.set_hvac_mode", map[string]any{"entity_id": "climate.attic", "hvac_mode": "heat"}},
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(calls)
		mu.Unlock()
		if n >= len(want) || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, w := range want {
		found := false
		for _, c := range calls {
			found = found || reflect.DeepEqual(c, w)
		}
		if !found {
			t.Errorf("no call %+v in %+v", w, calls)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	attic := Tile{Entity: "climate.attic", Value: "Off"}
	s.pendingOn(&attic)
	if attic.Value != "Heat" || !attic.On {
		t.Errorf("the attic's tile shows %+v, want Heat, lit", attic)
	}
	den := Tile{Entity: "climate.den", Value: "Heat cool"}
	s.pendingOn(&den)
	if den.Value != "19.5–24°" {
		t.Errorf("the den's tile shows %q", den.Value)
	}
}
