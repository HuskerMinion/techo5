//go:build !dot

package dashboard

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// A number is shown with the display precision Home Assistant has for its entity, as its own cards
// show it (#759): the first five are the issue's table, measured against a browser. With none, it is
// shown as before.
func TestASensorKeepsItsDisplayPrecision(t *testing.T) {
	precision := map[string]int{"sensor.price": 3, "sensor.energy": 2, "sensor.temp": 1, "sensor.volts": 0, "sensor.speed": 2}
	for _, c := range []struct{ id, state, unit, want string }{
		{"sensor.price", "0.2014201", "$/kWh", "0.201 $/kWh"},
		{"sensor.energy", "0.174", "kWh", "0.17 kWh"},
		{"sensor.temp", "24.96", "°C", "25.0 °C"},
		{"sensor.volts", "2.211", "V", "2 V"},
		{"sensor.speed", "164.16", "Mbit/s", "164.16 Mbit/s"},
		// A whole number is padded out to the precision, as the browser does.
		{"sensor.temp", "21", "°C", "21.0 °C"},
		// No precision: one decimal below 100, none from 100 up, a trailing ".0" dropped.
		{"sensor.other", "0.2014201", "$/kWh", "0.2 $/kWh"},
		{"sensor.other", "164.16", "Mbit/s", "164 Mbit/s"},
		{"sensor.other", "21", "°C", "21 °C"},
	} {
		e := hass.LiveEntity{ID: c.id, State: c.state, Attrs: map[string]any{"unit_of_measurement": c.unit}}
		if got := describe(e, "", precision).Value; got != c.want {
			t.Errorf("%s %s: %q, want %q", c.id, c.state, got, c.want)
		}
	}
	// Not a number: the state as Home Assistant says it, whatever the precision.
	gone := hass.LiveEntity{ID: "sensor.price", State: "unavailable", Attrs: map[string]any{}}
	if tile := describe(gone, "", precision); tile.Value != "Unavailable" || !tile.Gone {
		t.Errorf("unavailable: %+v", tile)
	}
}

// Halfway is rounded away from zero, as Intl.NumberFormat in the browser does, not to even as strconv
// does; a float only near halfway goes the way its exact value lies.
func TestFixedRoundsAsTheBrowserDoes(t *testing.T) {
	for _, c := range []struct {
		f    float64
		dp   int
		want string
	}{
		{2.5, 0, "3"}, {3.5, 0, "4"}, {-2.5, 0, "-3"}, {0.125, 2, "0.13"}, {0.375, 2, "0.38"}, {0.5, 0, "1"},
		{0.05, 1, "0.1"},   // 0.05 is a little over halfway as a float
		{1.005, 2, "1.00"}, // and 1.005 a little under
		{2.211, 0, "2"}, {24.96, 1, "25.0"}, {-0.001, 2, "-0.00"}, {12345.678, 1, "12345.7"},
	} {
		if got := fixed(c.f, c.dp); got != c.want {
			t.Errorf("fixed(%v, %d) = %q, want %q", c.f, c.dp, got, c.want)
		}
	}
}

// A gauge's reading keeps the entity's precision too.
func TestAGaugeKeepsItsDisplayPrecision(t *testing.T) {
	l := look{
		states:    map[string]hass.LiveEntity{"sensor.price": {ID: "sensor.price", State: "0.2014201", Attrs: map[string]any{"unit_of_measurement": "$/kWh"}}},
		precision: map[string]int{"sensor.price": 3},
	}
	b := gaugeNode{entity: "sensor.price", min: 0, max: 1}.blocks(l)
	if len(b) != 1 || b[0].Gauge == nil || b[0].Gauge.Value != "0.201 $/kWh" {
		t.Errorf("gauge %+v", b)
	}
}
