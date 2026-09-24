//go:build !dot && !spot

package dashboard

import (
	"encoding/json"
	"image"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

const sampleView = `{
  "badges": ["person.alex"],
  "sections": [
    {"type": "grid", "cards": [
      {"type": "heading", "heading": "Kitchen"},
      {"type": "tile", "entity": "light.counter", "name": "Counter"},
      {"type": "tile", "entity": "switch.kettle", "tap_action": {"action": "none"}}
    ]},
    {"type": "grid", "cards": [
      {"type": "entities", "title": "Doors", "entities": ["binary_sensor.back_door", {"type": "section", "label": "Garage"}, {"entity": "cover.garage", "name": "Big door"}, "switch.kettle"]},
      {"type": "conditional", "conditions": [{"entity": "light.counter", "state": "on"}], "card": {"type": "markdown", "content": "The counter is **on**"}}
    ]},
    {"type": "grid", "cards": [
      {"type": "custom:mushroom-template-card", "entity": "light.counter", "primary": "{{ states('sensor.temp') }}°", "secondary": "Inside"},
      {"type": "button", "name": "Movie", "icon": "mdi:movie", "tap_action": {"action": "perform-action", "perform_action": "scene.turn_on", "target": {"entity_id": "scene.movie"}}},
      {"type": "custom:weather-radar-card"},
      {"type": "sensor", "entity": "sensor.temp", "hours_to_show": 12},
      {"type": "gauge", "entity": "sensor.cpu", "severity": {"green": 0, "yellow": 50, "red": 80}},
      {"type": "picture-entity", "entity": "camera.porch"}
    ]}
  ]}`

func compileSample(t *testing.T) (*lovelaceSource, needs) {
	var view raw
	if err := json.Unmarshal([]byte(sampleView), &view); err != nil {
		t.Fatal(err)
	}
	l := &lovelaceSource{}
	c := &compiler{seen: map[string]bool{}, need: needs{graphs: map[string]int{}}}
	if b := c.badges(view["badges"]); b != nil {
		l.sects = append(l.sects, []node{b})
	}
	for _, s := range view["sections"].([]any) {
		l.sects = append(l.sects, c.cards(s.(raw)["cards"]))
	}
	return l, c.need
}

func TestADashboardBecomesSections(t *testing.T) {
	l, need := compileSample(t)
	if len(need.templates) != 1 || need.templates[0].vars["entity"] != "light.counter" {
		t.Fatalf("templates %+v", need.templates)
	}
	if need.graphs["sensor.temp"] != 12 || len(need.pictures) != 1 || need.pictures[0].camera != "camera.porch" {
		t.Fatalf("graphs %v pictures %+v", need.graphs, need.pictures)
	}
	states := map[string]hass.LiveEntity{
		"person.alex":             {ID: "person.alex", State: "home", Attrs: map[string]any{"friendly_name": "Alex"}},
		"light.counter":           {ID: "light.counter", State: "on", Attrs: map[string]any{}},
		"switch.kettle":           {ID: "switch.kettle", State: "off", Attrs: map[string]any{"friendly_name": "Kettle"}},
		"binary_sensor.back_door": {ID: "binary_sensor.back_door", State: "off", Attrs: map[string]any{"device_class": "door", "friendly_name": "Back door"}},
		"cover.garage":            {ID: "cover.garage", State: "closed", Attrs: map[string]any{"device_class": "garage"}},
		"sensor.temp":             {ID: "sensor.temp", State: "71.4", Attrs: map[string]any{"unit_of_measurement": "°F"}},
		"sensor.cpu":              {ID: "sensor.cpu", State: "62", Attrs: map[string]any{"unit_of_measurement": "%"}},
	}
	lk := look{states: states, rendered: map[int]string{need.templates[0].id: "71°"},
		history: map[string][]point{}, pictures: map[string]image.Image{}}
	secs := l.sections(lk)
	if len(secs) != 4 {
		t.Fatalf("%d sections, want 4", len(secs))
	}
	if b := secs[0].Blocks; len(b) != 1 || len(b[0].Tiles) != 1 {
		t.Errorf("badges %+v", b)
	}
	kitchen := secs[1].Blocks
	if len(kitchen) != 2 || kitchen[0].Heading != "Kitchen" || len(kitchen[1].Tiles) != 2 || kitchen[1].Tiles[1].Tap != nil {
		t.Errorf("kitchen %+v", kitchen)
	}
	doors := secs[2].Blocks
	if len(doors) != 3 || doors[0].Title != "Doors" || len(doors[0].Rows) != 1 || doors[1].Title != "Garage" ||
		len(doors[1].Rows) != 2 || !doors[1].Rows[1].Switch || doors[2].Text[0] != "The counter is on" {
		t.Errorf("doors %+v", doors)
	}
	last := secs[3].Blocks
	if len(last) != 4 {
		t.Fatalf("last section %+v", last)
	}
	tiles := last[0].Tiles
	if len(tiles) != 3 || tiles[0].Name != "71°" || tiles[1].Tap == nil || tiles[1].Tap.Data["entity_id"] != "scene.movie" ||
		tiles[2].Name != "Weather radar card" {
		t.Errorf("tiles %+v", tiles)
	}
	if g := last[1].Graph; g == nil || g.Value != "71.4 °F" || len(g.Points) != 0 {
		t.Errorf("graph %+v", g)
	}
	if g := last[2].Gauge; g == nil || g.Severity != "yellow" || g.Frac != 0.62 {
		t.Errorf("gauge %+v", g)
	}
	if p := last[3].Picture; p == nil || p.Image != nil {
		t.Errorf("picture %+v", p)
	}

	// With the light off, the conditional card goes.
	off := states["light.counter"]
	off.State = "off"
	states["light.counter"] = off
	for _, b := range l.sections(lk)[2].Blocks {
		if len(b.Text) > 0 {
			t.Error("a conditional card showed with its condition false")
		}
	}
}

func TestThemeColors(t *testing.T) {
	vars := map[string]string{}
	for k, v := range haDefault {
		vars[k] = v
	}
	for k, v := range map[string]string{
		"primary-background-color": "var(--catppuccin-mantle)", "catppuccin-mantle": "#241c17",
		"card-background-color": "rgb(42, 33, 28)", "primary-color": "var(--ha-color-primary-40)",
		"ha-color-primary-40": "rgb(214, 158, 110)", "ha-card-border-radius": "16px",
	} {
		vars[k] = v
	}
	th := themeOf(vars)
	if !th.Set || th.Background.R != 0x24 || th.Card.G != 33 || th.Accent.R != 214 || th.Radius != 16 {
		t.Errorf("theme %+v", th)
	}
}

func TestBuckets(t *testing.T) {
	if got := buckets(nil, 24, 10); got != nil {
		t.Errorf("no history: %v", got)
	}
}
