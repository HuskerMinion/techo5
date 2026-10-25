//go:build !dot

package dashboard

import (
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// ClimateNow is what a thermostat on the drawn page is set to, from what Home Assistant last said:
// for its sheet, which sets it.
type ClimateNow struct {
	Entity, Name, State string
	// Current is the temperature where it is, when it says.
	Current    float64
	HasCurrent bool
	// Target is the one temperature it holds to; Low and High the range it keeps between instead, in
	// heat_cool (HasRange). A thermostat that is off says neither.
	Target         float64
	HasTarget      bool
	Low, High      float64
	HasRange       bool
	Min, Max, Step float64
	Modes          []string // its HVAC modes: "off", "heat", "cool", "heat_cool", "auto", ...
}

// ClimateNow is a thermostat on the drawn page as Home Assistant last said. It is false for anything
// that is not a thermostat there.
func (f *Feature) ClimateNow(entity string) (ClimateNow, bool) {
	if !strings.HasPrefix(entity, "climate.") {
		return ClimateNow{}, false
	}
	f.mu.Lock()
	s := f.drawn
	f.mu.Unlock()
	if s == nil {
		return ClimateNow{}, false
	}
	s.mu.Lock()
	e, ok := s.states[entity]
	s.mu.Unlock()
	if !ok {
		return ClimateNow{}, false
	}
	return climateNow(e), true
}

// ClimateOf is a thermostat read from a state and attributes that came from elsewhere than the drawn
// page: a tile of the Tessera page, whose sheet is this one.
func ClimateOf(entity, state string, attrs map[string]any) ClimateNow {
	return climateNow(hass.LiveEntity{ID: entity, State: state, Attrs: attrs})
}

// climateNow reads a thermostat's state.
func climateNow(e hass.LiveEntity) ClimateNow {
	c := ClimateNow{Entity: e.ID, State: e.State}
	c.Name, _ = e.Attrs["friendly_name"].(string)
	c.Current, c.HasCurrent = e.Attrs["current_temperature"].(float64)
	c.Target, c.HasTarget = e.Attrs["temperature"].(float64)
	lo, hasLo := e.Attrs["target_temp_low"].(float64)
	hi, hasHi := e.Attrs["target_temp_high"].(float64)
	if hasLo && hasHi && !c.HasTarget {
		c.Low, c.High, c.HasRange = lo, hi, true
	}
	around := c.Target
	if c.HasRange {
		around = (c.Low + c.High) / 2
	}
	c.Min, c.Max, c.Step = climateLimits(e, around)
	if m, ok := e.Attrs["hvac_modes"].([]any); ok {
		for _, x := range m {
			if s, ok := x.(string); ok {
				c.Modes = append(c.Modes, s)
			}
		}
	}
	return c
}

// climateLimits is how far a thermostat's target goes, and the step it is set in: what it says, or
// ten degrees either side of around with half a degree (a whole one in Fahrenheit) when it does not.
func climateLimits(e hass.LiveEntity, around float64) (lo, hi, step float64) {
	lo, _ = e.Attrs["min_temp"].(float64)
	hi, _ = e.Attrs["max_temp"].(float64)
	step, _ = e.Attrs["target_temp_step"].(float64)
	if hi <= lo {
		lo, hi = around-10, around+10
	}
	if step <= 0 {
		step = 0.5
		if hi > 40 {
			step = 1 // Fahrenheit
		}
	}
	return lo, hi, step
}

// Stepped is v moved n steps, on the steps and within the thermostat's range.
func (c ClimateNow) Stepped(v float64, n int) float64 {
	return Adjust{Kind: "temperature", Min: c.Min, Max: c.Max, Step: c.Step}.Snap(v + float64(n)*c.Step)
}

// SetClimate sets a thermostat's target: one temperature, or with high above zero the range from low
// to high (heat_cool). The tile shows it at once, until Home Assistant says otherwise.
func (f *Feature) SetClimate(entity string, low, high float64) {
	f.mu.Lock()
	s := f.drawn
	f.mu.Unlock()
	if s == nil {
		return
	}
	data := map[string]any{"entity_id": entity}
	value := trimNumber(low) + "°"
	if high > 0 {
		data["target_temp_low"], data["target_temp_high"] = low, high
		value = trimNumber(low) + "–" + trimNumber(high) + "°"
	} else {
		data["temperature"] = low
	}
	s.call(entity, "climate", "set_temperature", data, &pending{value: value, keepOn: true})
}

// SetHVACMode sets what a thermostat does: heat, cool, off and the rest of its modes.
func (f *Feature) SetHVACMode(entity, mode string) {
	f.mu.Lock()
	s := f.drawn
	f.mu.Unlock()
	if s == nil {
		return
	}
	on := mode == "heat" || mode == "cool" || mode == "heat_cool"
	s.call(entity, "climate", "set_hvac_mode", map[string]any{"entity_id": entity, "hvac_mode": mode},
		&pending{value: title(mode), on: on})
}
