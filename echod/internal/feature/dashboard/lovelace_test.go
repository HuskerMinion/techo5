//go:build !dot && !spot

package dashboard

import (
	"encoding/json"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

const sampleView = `{
  "badges": ["person.alex"],
  "cards": [
    {"type": "heading", "heading": "Kitchen"},
    {"type": "grid", "cards": [
      {"type": "tile", "entity": "light.counter", "name": "Counter"},
      {"type": "tile", "entity": "switch.kettle", "tap_action": {"action": "none"}}
    ]},
    {"type": "entities", "title": "Doors", "entities": ["binary_sensor.back_door", {"type": "section", "label": "Garage"}, {"entity": "cover.garage", "name": "Big door"}]},
    {"type": "conditional", "conditions": [{"entity": "light.counter", "state": "on"}], "card": {"type": "markdown", "content": "The counter is **on**"}},
    {"type": "custom:mushroom-template-card", "entity": "light.counter", "primary": "{{ states('sensor.temp') }}°", "secondary": "Inside"},
    {"type": "button", "name": "Movie", "icon": "mdi:movie", "tap_action": {"action": "perform-action", "perform_action": "scene.turn_on", "target": {"entity_id": "scene.movie"}}},
    {"type": "custom:apexcharts-card"}
  ]}`

func TestADashboardBecomesBlocks(t *testing.T) {
	var view raw
	if err := json.Unmarshal([]byte(sampleView), &view); err != nil {
		t.Fatal(err)
	}
	c := &compiler{seen: map[string]bool{}}
	c.badges(view["badges"])
	c.cards(view["cards"])
	if len(c.templates) != 1 || c.templates[0].vars["entity"] != "light.counter" {
		t.Fatalf("templates %+v", c.templates)
	}
	src := &lovelaceSource{nodes: c.nodes}
	states := map[string]hass.LiveEntity{
		"person.alex":             {ID: "person.alex", State: "home", Attrs: map[string]any{"friendly_name": "Alex"}},
		"light.counter":           {ID: "light.counter", State: "on", Attrs: map[string]any{}},
		"switch.kettle":           {ID: "switch.kettle", State: "off", Attrs: map[string]any{"friendly_name": "Kettle"}},
		"binary_sensor.back_door": {ID: "binary_sensor.back_door", State: "off", Attrs: map[string]any{"device_class": "door", "friendly_name": "Back door"}},
		"cover.garage":            {ID: "cover.garage", State: "closed", Attrs: map[string]any{"device_class": "garage"}},
	}
	blocks := src.blocks(states, map[int]string{c.templates[0].id: "71°"})

	want := []struct {
		heading string
		tiles   int
		text    int
	}{{"", 1, 0}, {"Kitchen", 2, 0}, {"Doors", 1, 0}, {"Garage", 1, 0}, {"", 0, 1}, {"", 3, 0}}
	if len(blocks) != len(want) {
		for _, b := range blocks {
			t.Logf("%q tiles=%d text=%v", b.Heading, len(b.Tiles), b.Text)
		}
		t.Fatalf("%d blocks, want %d", len(blocks), len(want))
	}
	for i, w := range want {
		b := blocks[i]
		if b.Heading != w.heading || len(b.Tiles) != w.tiles || len(b.Text) != w.text {
			t.Errorf("block %d: %q %d tiles %d text, want %q %d %d", i, b.Heading, len(b.Tiles), len(b.Text), w.heading, w.tiles, w.text)
		}
	}
	if k := blocks[1].Tiles[1]; k.Tap != nil {
		t.Errorf("tap_action none still taps: %+v", k.Tap)
	}
	if blocks[4].Text[0] != "The counter is on" {
		t.Errorf("markdown %q", blocks[4].Text[0])
	}
	last := blocks[5].Tiles
	if last[0].Name != "71°" || last[0].Value != "Inside" {
		t.Errorf("template tile %+v", last[0])
	}
	if last[1].Tap == nil || last[1].Tap.Service != "scene.turn_on" || last[1].Tap.Data["entity_id"] != "scene.movie" {
		t.Errorf("button tile %+v", last[1].Tap)
	}
	if last[2].Name != "Apexcharts card" {
		t.Errorf("unknown card %+v", last[2])
	}

	// With the light off, the conditional card goes.
	off := states["light.counter"]
	off.State = "off"
	states["light.counter"] = off
	for _, b := range src.blocks(states, nil) {
		if len(b.Text) > 0 {
			t.Error("a conditional card showed with its condition false")
		}
	}
}
