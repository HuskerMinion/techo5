//go:build !dot

package dashboard

import (
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// FanEntityFeature, the bits of a fan's supported_features that its sheet and its slider go by.
const (
	fanSetSpeed   = 1
	fanDirection  = 4
	fanPresetMode = 8
)

// FanNow is a fan on the drawn page as its sheet shows it, from what Home Assistant last said: what it
// is doing, and which of the controls it has.
type FanNow struct {
	Entity, Name string
	On           bool
	// HasSpeed is a fan that takes a speed (set speed); Percentage is where it is, from 0 to 100, and
	// Step the steps it takes (percentage_step; a whole percent when it says none).
	HasSpeed   bool
	Percentage float64
	Step       float64
	// HasDirection is a fan that turns either way; Direction is "forward" or "reverse".
	HasDirection bool
	Direction    string
	// Presets are its preset modes, when it has any, and Preset the one it is in.
	Presets []string
	Preset  string
}

// FanNow is a fan on the drawn page as Home Assistant last said. It is false for anything that is not a
// fan there.
func (f *Feature) FanNow(entity string) (FanNow, bool) {
	if !strings.HasPrefix(entity, "fan.") {
		return FanNow{}, false
	}
	f.mu.Lock()
	s := f.drawn
	f.mu.Unlock()
	if s == nil {
		return FanNow{}, false
	}
	s.mu.Lock()
	e, ok := s.states[entity]
	s.mu.Unlock()
	if !ok {
		return FanNow{}, false
	}
	return fanNow(e), true
}

// FanOf is a fan read from a state and attributes that came from elsewhere than the drawn page: a tile
// of the Tessera page, whose sheet is this one.
func FanOf(entity, state string, attrs map[string]any) FanNow {
	return fanNow(hass.LiveEntity{ID: entity, State: state, Attrs: attrs})
}

// fanNow reads a fan's state: each control only when its supported_features bit is set (and, for
// the preset modes, when it names any).
func fanNow(e hass.LiveEntity) FanNow {
	features, _ := e.Attrs["supported_features"].(float64)
	bits := int(features)
	n := FanNow{Entity: e.ID, On: e.State == "on"}
	n.Name, _ = e.Attrs["friendly_name"].(string)
	if bits&fanSetSpeed != 0 {
		n.HasSpeed = true
		if p, ok := e.Attrs["percentage"].(float64); ok && n.On {
			n.Percentage = p
		}
		n.Step = fanStep(e)
	}
	if bits&fanDirection != 0 {
		n.HasDirection = true
		n.Direction, _ = e.Attrs["direction"].(string)
	}
	if bits&fanPresetMode != 0 {
		if m, ok := e.Attrs["preset_modes"].([]any); ok {
			for _, x := range m {
				if s, ok := x.(string); ok {
					n.Presets = append(n.Presets, s)
				}
			}
		}
		if len(n.Presets) > 0 {
			n.Preset, _ = e.Attrs["preset_mode"].(string)
		}
	}
	return n
}

// fanStep is the steps a fan's speed takes: the percentage_step it gives, else a whole percent.
func fanStep(e hass.LiveEntity) float64 {
	if s, ok := e.Attrs["percentage_step"].(float64); ok && s > 0 && s <= 100 {
		return s
	}
	return 1
}

// Speed is v, a percentage, on the fan's steps within 0 to 100, and whole: what the fan is asked to run
// at.
func (n FanNow) Speed(v float64) int {
	return int(Adjust{Kind: "speed", Min: 0, Max: 100, Step: n.Step}.Snap(v) + 0.5)
}
