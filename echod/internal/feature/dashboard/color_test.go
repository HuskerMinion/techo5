//go:build !dot

package dashboard

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// A light says what its color can be from its color modes: whites with their range, colors, both, or
// neither. The white it is on counts only while it is on one.
func TestALightSaysWhatColorItCanBe(t *testing.T) {
	for _, c := range []struct {
		name string
		e    hass.LiveEntity
		want LightColor
		some bool
	}{
		{"whites", hass.LiveEntity{ID: "light.desk", State: "on", Attrs: map[string]any{
			"friendly_name": "Desk", "supported_color_modes": []any{"color_temp"}, "color_mode": "color_temp",
			"min_color_temp_kelvin": 2202.0, "max_color_temp_kelvin": 6535.0, "color_temp_kelvin": 3000.0}},
			LightColor{Entity: "light.desk", Name: "Desk", Kelvin: true, MinK: 2202, MaxK: 6535, NowK: 3000}, true},
		{"both, on a color", hass.LiveEntity{ID: "light.strip", State: "on", Attrs: map[string]any{
			"supported_color_modes": []any{"color_temp", "xy"}, "color_mode": "xy", "color_temp_kelvin": 4000.0}},
			LightColor{Entity: "light.strip", Kelvin: true, MinK: warmestK, MaxK: coolestK, Colors: true}, true},
		{"on a color", hass.LiveEntity{ID: "light.bulb", State: "on", Attrs: map[string]any{
			"supported_color_modes": []any{"hs"}, "color_mode": "hs", "hs_color": []any{200.0, 80.0}}},
			LightColor{Entity: "light.bulb", Colors: true, HasHue: true, NowHue: 200}, true},
		{"colors only", hass.LiveEntity{ID: "light.led", State: "off", Attrs: map[string]any{
			"supported_color_modes": []any{"rgb"}}},
			LightColor{Entity: "light.led", Colors: true}, true},
		{"dimmed only", hass.LiveEntity{ID: "light.hall", State: "on", Attrs: map[string]any{
			"supported_color_modes": []any{"brightness"}}},
			LightColor{Entity: "light.hall"}, false},
		{"a group in the hub", hass.LiveEntity{ID: "light.lounge", State: "on", Attrs: map[string]any{
			"supported_color_modes": []any{"color_temp", "hs"}, "is_deconz_group": true}},
			LightColor{Entity: "light.lounge", Kelvin: true, MinK: warmestK, MaxK: coolestK, Colors: true, Group: true}, true},
		{"a group in Home Assistant", hass.LiveEntity{ID: "light.office", State: "off", Attrs: map[string]any{
			"supported_color_modes": []any{"rgb"}, "entity_id": []any{"light.panel", "light.strip"}}},
			LightColor{Entity: "light.office", Colors: true, Group: true}, true},
	} {
		got := lightColor(c.e)
		if got != c.want || got.Some() != c.some {
			t.Errorf("%s: %+v (some %v), want %+v (some %v)", c.name, got, got.Some(), c.want, c.some)
		}
	}
}
