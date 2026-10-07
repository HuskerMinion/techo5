//go:build !dot

package dashboard

import (
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// A light's color, for the screen to offer when a finger rests on its tile: a white between warm and
// cool, colors, or both, as the light says it can take.

// LightColor is what a light's color can be set to.
type LightColor struct {
	Entity string
	Name   string
	// Kelvin is a white the light can take, between MinK and MaxK; NowK is the one it is on, or 0 when
	// it is off or on a color.
	Kelvin           bool
	MinK, MaxK, NowK float64
	// Colors is any hue at all. NowHue is the one it is on, when HasHue: it is on and on a color.
	Colors bool
	HasHue bool
	NowHue float64
	// Group is a group of lights, which says it can take what any one of them can: a color it is
	// given reaches only those that have colors, and the rest stay white.
	Group bool
}

// Some is whether the light has a color to choose at all.
func (c LightColor) Some() bool { return c.Kelvin || c.Colors }

// The whites a light that does not say its range is taken to have.
const (
	warmestK = 2000
	coolestK = 6500
)

// LightColor says what a light on the drawn page can have its color set to, from what Home Assistant
// last said about it. It is false for anything that is not a light there, and for a light that is
// only on, off or dimmed.
func (f *Feature) LightColor(entity string) (LightColor, bool) {
	if !strings.HasPrefix(entity, "light.") {
		return LightColor{}, false
	}
	f.mu.Lock()
	s := f.drawn
	f.mu.Unlock()
	if s == nil {
		return LightColor{}, false
	}
	s.mu.Lock()
	e, ok := s.states[entity]
	s.mu.Unlock()
	if !ok {
		return LightColor{}, false
	}
	c := lightColor(e)
	return c, c.Some()
}

// lightColor reads a light's color modes and its range of whites.
func lightColor(e hass.LiveEntity) LightColor {
	c := LightColor{Entity: e.ID}
	c.Name, _ = e.Attrs["friendly_name"].(string)
	deconz, _ := e.Attrs["is_deconz_group"].(bool)
	hue, _ := e.Attrs["is_hue_group"].(bool)
	members, _ := e.Attrs["entity_id"].([]any)
	c.Group = deconz || hue || len(members) > 0
	modes, _ := e.Attrs["supported_color_modes"].([]any)
	for _, m := range modes {
		switch m {
		case "color_temp":
			c.Kelvin = true
		case "hs", "xy", "rgb", "rgbw", "rgbww":
			c.Colors = true
		}
	}
	if c.Colors && e.State == "on" && e.Attrs["color_mode"] != "color_temp" {
		if hs, ok := e.Attrs["hs_color"].([]any); ok && len(hs) == 2 {
			if h, ok := hs[0].(float64); ok {
				c.HasHue, c.NowHue = true, h
			}
		}
	}
	if !c.Kelvin {
		return c
	}
	c.MinK, c.MaxK = kelvinAttr(e.Attrs["min_color_temp_kelvin"]), kelvinAttr(e.Attrs["max_color_temp_kelvin"])
	if c.MinK <= 0 || c.MaxK <= c.MinK {
		c.MinK, c.MaxK = warmestK, coolestK
	}
	if e.State == "on" && e.Attrs["color_mode"] == "color_temp" {
		c.NowK = kelvinAttr(e.Attrs["color_temp_kelvin"])
	}
	return c
}

func kelvinAttr(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}
