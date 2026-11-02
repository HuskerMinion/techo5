//go:build !dot

package dashboard

import (
	"encoding/json"
	"image/color"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// A card's color is read as Home Assistant reads it: its names, var(--red-color), hex and rgb; the
// theme's own colors and anything unreadable leave the theme in use.
func TestCardColorIsRead(t *testing.T) {
	for in, want := range map[string]color.RGBA{
		"red": {0xf4, 0x43, 0x36, 255}, " Amber ": {0xff, 0xc1, 0x07, 255}, "deep-orange": {0xff, 0x6f, 0x22, 255},
		"var(--green-color)": {0x4c, 0xaf, 0x50, 255}, "#c00": {0xcc, 0, 0, 255}, "#12ab34": {0x12, 0xab, 0x34, 255},
		"rgb(1, 2, 3)": {1, 2, 3, 255}, "grey": {0x9e, 0x9e, 0x9e, 255},
	} {
		if got, ok := CardColor(in); !ok || got != want {
			t.Errorf("%q: %v %v, want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "primary", "accent", "nonsense", "#12", "transparent", "rgba(1,2,3,0)"} {
		if _, ok := CardColor(in); ok {
			t.Errorf("%q read as a color", in)
		}
	}
	if got, ok := CardColor([]any{1.0, 2.0, 3.0}); !ok || got != (color.RGBA{1, 2, 3, 255}) {
		t.Errorf("a list of three: %v %v", got, ok)
	}
	if _, ok := CardColor([]any{1.0, 2.0, 300.0}); ok {
		t.Error("a list with 300 in it read as a color")
	}
	if _, ok := CardColor(nil); ok {
		t.Error("nil read as a color")
	}
}

// The color of a tile card reaches its tile, a sensor's always and a light's only while it is on; the
// color of a heading or markdown card reaches its block.
func TestCardColorReachesTheBlock(t *testing.T) {
	var cards []any
	if err := json.Unmarshal([]byte(`[
	  {"type": "tile", "entity": "sensor.disk", "color": "red"},
	  {"type": "tile", "entity": "light.lamp", "color": "#00ff00"},
	  {"type": "tile", "entity": "light.on", "color": "blue"},
	  {"type": "tile", "entity": "sensor.plain"},
	  {"type": "heading", "heading": "Problem", "color": "amber"},
	  {"type": "markdown", "content": "Check the server", "color": "orange"}
	]`), &cards); err != nil {
		t.Fatal(err)
	}
	c := &compiler{seen: map[string]bool{}, need: needs{graphs: map[string]int{}}}
	l := &lovelaceSource{sects: [][]node{c.cards(cards)}}
	lk := look{states: map[string]hass.LiveEntity{
		"sensor.disk":  {ID: "sensor.disk", State: "98", Attrs: map[string]any{}},
		"light.lamp":   {ID: "light.lamp", State: "off", Attrs: map[string]any{}},
		"light.on":     {ID: "light.on", State: "on", Attrs: map[string]any{}},
		"sensor.plain": {ID: "sensor.plain", State: "1", Attrs: map[string]any{}},
	}}
	var tiles []Tile
	var blocks []Block
	for _, s := range l.sections(lk) {
		for _, b := range s.Blocks {
			tiles = append(tiles, b.Tiles...)
			if b.Heading != "" || len(b.Text) > 0 {
				blocks = append(blocks, b)
			}
		}
	}
	if len(tiles) != 4 {
		t.Fatalf("%d tiles, want 4", len(tiles))
	}
	red := color.RGBA{0xf4, 0x43, 0x36, 255}
	if tiles[0].Color != red {
		t.Errorf("sensor tile color %v, want red", tiles[0].Color)
	}
	if tiles[1].Color.A != 0 {
		t.Errorf("a light that is off took its color: %v", tiles[1].Color)
	}
	if tiles[2].Color != (color.RGBA{0x21, 0x96, 0xf3, 255}) {
		t.Errorf("a light that is on: %v", tiles[2].Color)
	}
	if tiles[3].Color.A != 0 {
		t.Errorf("a tile with no color has %v", tiles[3].Color)
	}
	if len(blocks) != 2 || blocks[0].Color != (color.RGBA{0xff, 0xc1, 0x07, 255}) || blocks[1].Color != (color.RGBA{0xff, 0x98, 0, 255}) {
		t.Errorf("heading and markdown colors: %+v", blocks)
	}
}
