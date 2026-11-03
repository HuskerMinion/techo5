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

// A fan's sheet has a control only where the fan has it: the speed with set speed (bit 1), the
// direction with direction (bit 4), the preset modes with preset mode (bit 8) and a list of them. Its
// tile slides only with set speed, on the steps it says.
func TestAFanIsReadForItsSheet(t *testing.T) {
	presets := []any{"auto", "sleep", "turbo"}
	for _, c := range []struct {
		name     string
		features float64
		attrs    map[string]any
		state    string
		speed    bool
		dir      bool
		presets  []string
		pct      float64
		step     float64
	}{
		{"all of it", 1 | 4 | 8 | 16 | 32, map[string]any{"percentage": 66.0, "percentage_step": 33.333, "direction": "reverse",
			"preset_modes": presets, "preset_mode": "sleep"}, "on", true, true, []string{"auto", "sleep", "turbo"}, 66, 33.333},
		{"on and off only", 16 | 32, map[string]any{"percentage": 50.0}, "on", false, false, nil, 0, 0},
		{"speed, no step given", 1, map[string]any{"percentage": 40.0}, "on", true, false, nil, 40, 1},
		{"speed, off", 1, map[string]any{"percentage": 40.0}, "off", true, false, nil, 0, 1},
		{"direction only", 4, map[string]any{"direction": "forward"}, "on", false, true, nil, 0, 0},
		{"preset bit, no modes", 8, map[string]any{"preset_modes": []any{}}, "on", false, false, nil, 0, 0},
		{"modes, no preset bit", 1, map[string]any{"preset_modes": presets}, "on", true, false, nil, 0, 1},
		{"no features", 0, map[string]any{"percentage": 50.0, "direction": "forward"}, "on", false, false, nil, 0, 0},
	} {
		attrs := withFeatures(c.attrs, c.features)
		n := FanOf("fan.bedroom", c.state, attrs)
		if n.HasSpeed != c.speed || n.HasDirection != c.dir || !reflect.DeepEqual(n.Presets, c.presets) ||
			n.Percentage != c.pct || n.Step != c.step || n.On != (c.state == "on") {
			t.Errorf("%s: %+v", c.name, n)
		}
		tile := describe(hass.LiveEntity{ID: "fan.bedroom", State: c.state, Attrs: attrs}, "Fan")
		if (tile.Adjust != nil) != c.speed {
			t.Errorf("%s: level %+v, want one: %v", c.name, tile.Adjust, c.speed)
		}
		if tile.Tap == nil || tile.Tap.Service != "fan.toggle" {
			t.Errorf("%s: tap %+v, want the toggle", c.name, tile.Tap)
		}
		if a := tile.Adjust; a != nil && (a.Kind != "speed" || a.Min != 0 || a.Max != 100 || a.Step != c.step || a.Value != c.pct) {
			t.Errorf("%s: level %+v", c.name, a)
		}
	}
	n := FanOf("fan.bedroom", "on", withFeatures(map[string]any{"preset_modes": presets, "preset_mode": "sleep", "direction": "reverse"}, 1|4|8))
	if n.Preset != "sleep" || n.Direction != "reverse" {
		t.Errorf("preset and direction: %+v", n)
	}
}

func withFeatures(attrs map[string]any, features float64) map[string]any {
	out := map[string]any{"supported_features": features}
	for k, v := range attrs {
		out[k] = v
	}
	return out
}

// A speed is snapped to the fan's steps (three speeds are steps of a third) and given to Home
// Assistant whole.
func TestAFansSpeedSnapsToItsSteps(t *testing.T) {
	third := FanNow{Step: 100.0 / 3}
	for in, want := range map[float64]int{0: 0, 10: 0, 20: 33, 50: 67, 80: 67, 90: 100, 100: 100, 140: 100, -5: 0} {
		if got := third.Speed(in); got != want {
			t.Errorf("a third's steps, %v: %d, want %d", in, got, want)
		}
	}
	if got := (FanNow{Step: 1}).Speed(42.4); got != 42 {
		t.Errorf("whole steps, 42.4: %d", got)
	}
}

// Sliding along a fan's tile calls fan.set_percentage, and zero is asked for as zero.
func TestASlideAlongAFanSetsItsSpeed(t *testing.T) {
	var mu sync.Mutex
	var got []map[string]any
	var services []string
	prev := callService
	callService = func(_ *hass.Live, _ context.Context, domain, service string, data map[string]any) error {
		mu.Lock()
		services = append(services, domain+"."+service)
		got = append(got, data)
		mu.Unlock()
		return nil
	}
	t.Cleanup(func() { callService = prev })

	f := &Feature{}
	s := &session{f: f, src: noSource{}, live: &hass.Live{}, states: map[string]hass.LiveEntity{}, pending: map[string]pending{}}
	f.drawn = s
	a := Adjust{Entity: "fan.bedroom", Kind: "speed", Min: 0, Max: 100, Step: 25}
	f.SetLevel(a, 60)
	f.SetLevel(a, 0)
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("calls %v %v", services, got)
	}
	for _, pct := range []int{50, 0} {
		found := false
		for i := range got {
			found = found || services[i] == "fan.set_percentage" && got[i]["entity_id"] == "fan.bedroom" && got[i]["percentage"] == pct
		}
		if !found {
			t.Errorf("no fan.set_percentage to %d in %v %v", pct, services, got)
		}
	}
}
