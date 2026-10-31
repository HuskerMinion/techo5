//go:build !dot

package dashboard

import (
	"encoding/json"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// A name loses the room in front of it only as a whole word or words, and never all of itself.
func TestWithoutRoom(t *testing.T) {
	for _, c := range []struct{ name, room, want string }{
		{"Master Bedroom ensuite fan", "Master Bedroom", "Ensuite fan"},
		{"master bedroom lamp", "Master Bedroom", "Lamp"},
		{"Kitchen - island lights", "Kitchen", "Island lights"},
		{"Kitchen: kettle", "Kitchen", "Kettle"},
		{"Kitchenette light", "Kitchen", "Kitchenette light"},
		{"Kitchen", "Kitchen", "Kitchen"},
		{"Kitchen ", "Kitchen", "Kitchen "},
		{"Hall light", "", "Hall light"},
		{"Hall light", "Kitchen", "Hall light"},
		{"Küche éclairage", "Küche", "Éclairage"},
	} {
		if got := withoutRoom(c.name, c.room); got != c.want {
			t.Errorf("withoutRoom(%q, %q) = %q, want %q", c.name, c.room, got, c.want)
		}
	}
}

// Under a heading, a tile with an entity's own name shows it without the heading in front; a name
// the card gives stays as it is, and a new section starts with no heading.
func TestTilesUnderAHeadingDropTheRoom(t *testing.T) {
	var first, second []any
	for s, into := range map[string]*[]any{
		`[{"type": "heading", "heading": "Master Bedroom"},
		  {"type": "tile", "entity": "light.mb_ceiling"},
		  {"type": "tile", "entity": "fan.mb_fan", "name": "Master Bedroom fan"},
		  {"type": "glance", "entities": ["light.mb_ceiling"]},
		  {"type": "entities", "title": "Kitchen", "entities": ["light.k_island"]}]`: &first,
		`[{"type": "tile", "entity": "light.mb_ceiling"}]`: &second,
	} {
		if err := json.Unmarshal([]byte(s), into); err != nil {
			t.Fatal(err)
		}
	}
	c := &compiler{seen: map[string]bool{}, need: needs{graphs: map[string]int{}}}
	l := &lovelaceSource{sects: [][]node{c.cards(first), c.cards(second)}}
	named := func(id, name string) hass.LiveEntity {
		return hass.LiveEntity{ID: id, State: "off", Attrs: map[string]any{"friendly_name": name}}
	}
	lk := look{states: map[string]hass.LiveEntity{
		"light.mb_ceiling": named("light.mb_ceiling", "Master Bedroom ceiling light"),
		"fan.mb_fan":       named("fan.mb_fan", "Master Bedroom fan"),
		"light.k_island":   named("light.k_island", "Kitchen island lights"),
	}, width: 960}
	var tiles, rows []string
	for _, s := range l.sections(lk) {
		for _, b := range s.Blocks {
			for _, t := range b.Tiles {
				tiles = append(tiles, t.Name)
			}
			for _, t := range b.Rows {
				rows = append(rows, t.Name)
			}
		}
	}
	want := []string{"Ceiling light", "Master Bedroom fan", "Ceiling light", "Master Bedroom ceiling light"}
	if len(tiles) != len(want) {
		t.Fatalf("tiles %q, want %q", tiles, want)
	}
	for i := range want {
		if tiles[i] != want[i] {
			t.Errorf("tile %d is %q, want %q", i, tiles[i], want[i])
		}
	}
	if len(rows) != 1 || rows[0] != "Island lights" {
		t.Errorf("rows %q, want the entities card's title dropped: [Island lights]", rows)
	}
}
