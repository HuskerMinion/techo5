//go:build !dot && !spot

package display

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/tessera"
)

// A card's parts as the plugin reads them: kind, value, unit, label, color, icon, least and most; a badge
// for an icon that starts with "#"; a kind it does not know left out; a sensor without elements its state.
func TestCustomCardParts(t *testing.T) {
	p, ok := ccParse("bar|820|ppm|CO2|yellow|molecule-co2|400|2000")
	if !ok || p.kind != ccBar || p.value != "820" || p.unit != "ppm" || p.label != "CO2" || !p.colored ||
		p.icon != "molecule-co2" || p.min != 400 || p.max != 2000 {
		t.Errorf("bar %+v", p)
	}
	if p, _ := ccParse("row|4|min|Main station|blue|#16"); p.badge != "16" || p.icon != "" {
		t.Errorf("badge %+v", p)
	}
	if p, _ := ccParse("value|3||Open"); p.unit != "" || p.label != "Open" || p.colored || p.max != 100 {
		t.Errorf("fields left empty %+v", p)
	}
	if _, ok := ccParse("gauge|1"); ok {
		t.Error("an unknown kind was taken")
	}
	parts, note := ccParts(map[string]any{"state": "21", "attributes": map[string]any{"elements": []any{"value|1", "nonsense|2", "row|3|°C|Bath"}}})
	if len(parts) != 2 || note != "" {
		t.Errorf("elements: %d parts, note %q", len(parts), note)
	}
	parts, _ = ccParts(map[string]any{"state": "21.5", "attributes": map[string]any{"unit_of_measurement": "°C"}})
	if len(parts) != 1 || parts[0].value != "21.5" || parts[0].unit != "°C" {
		t.Errorf("a plain sensor %+v", parts)
	}
	if _, note := ccParts(map[string]any{"wait": "wrong_entity"}); note == "" {
		t.Error("a wrong sensor says nothing")
	}
}

// The plugin's own examples on cards of several sizes: each draws something, none past its tile.
func TestCustomCardTiles(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Dashboard().Mode(config.DashboardTessera); err != nil {
		t.Fatal(err)
	}
	slot := func(n int) *int { return &n }
	card := func(i, at int, size, name string, elements ...string) tessera.Tile {
		x, _ := json.Marshal(map[string]any{"state": "1", "name": name, "attributes": map[string]any{"elements": elements}})
		o, _ := json.Marshal(map[string]any{"size": size, "plugin": map[string]any{"show_name": true, "coloured_values": false, "per_row": "auto"}})
		return tessera.Tile{I: i, Slot: slot(at), Entity: "plugin:custom_card.card", Name: name, State: "ok", A: json.RawMessage(`{}`), O: o, X: x}
	}
	l := &tessera.Layout{
		Rev:   "custom0123456789",
		Begin: tessera.Begin{Pages: 1, Tiles: 6},
		Pages: []tessera.Page{{P: 0, Title: "Home", Items: json.RawMessage(`[]`)}},
		Tiles: []tessera.Tile{
			card(0, 0, "square", "Living room", "value|21.4|°C|Temperature|blue|thermometer", "value|46|%|Humidity|green|water-percent",
				"bar|820|ppm|CO2|yellow|molecule-co2|400|2000"),
			card(1, 2, "square", "Air", "ring|820|ppm|CO2|yellow|molecule-co2|400|2000", "ring|46|%|Humidity|green|water-percent|0|100",
				"ring|21|°C|Temperature|blue|thermometer|10|30"),
			card(2, 4, "2x3", "Rooms", "row|20.8|°C|Kitchen", "row|21.8|°C|Bedroom", "row|20.5|°C|Bath", "row|20.7|°C|Office", "row|9.7|°C|Outside"),
			card(3, 12, "3x2", "Departures", "row|now||Central|blue|#16", "row|4|min|Main station|blue|#16", "row|9|min|Airport|purple|#S3",
				"row|12|min|Harbour|blue|#16"),
			card(4, 21, "3x2", "Batteries", "row|8|%|Door sensor|red|battery-high", "row|34|%|Remote|yellow|battery-high",
				"row|61|%|Motion hall|green|battery-high", "row|90|%|Smoke|green|battery-high"),
			card(5, 24, "3x1", "Entrance", "value|5358||Scanned", "value|5933||Valid", "bar|90|%|Share|blue||0|100"),
		},
	}
	tessera.Preview(l, 6, 5)
	t.Cleanup(func() { tessera.Preview(nil, 0, 0) })
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, show8Wide, show8High)))
	v, _ := tessera.Current()
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardTessera, tessera: v, tessOn: true})
	if dir := os.Getenv("TECHO5_PREVIEW"); dir != "" {
		if f, err := os.Create(filepath.Join(dir, "custom-card.png")); err == nil {
			_ = png.Encode(f, r.dst)
			f.Close()
		}
	}
	zones, _ := r.dash()
	boxes := map[int]image.Rectangle{}
	for _, z := range zones {
		if z.tess > 0 {
			boxes[z.tess-1] = z.r
		}
	}
	for i := range 6 {
		b, ok := boxes[i]
		if !ok {
			t.Fatalf("card %d takes no tap", i)
		}
		ground := r.dst.RGBAAt(b.Min.X+3, b.Min.Y+b.Dy()/2)
		inked := 0
		for y := b.Min.Y + 4; y < b.Max.Y-4; y++ {
			for x := b.Min.X + 4; x < b.Max.X-4; x++ {
				if r.dst.RGBAAt(x, y) != ground {
					inked++
				}
			}
		}
		if inked < 200 {
			t.Errorf("card %d drew %d pixels", i, inked)
		}
	}
}

// Rings where they have the room: a card of rings, and a ring beside two counts, each arc in its part's
// color.
func TestCustomCardRings(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Dashboard().Mode(config.DashboardTessera); err != nil {
		t.Fatal(err)
	}
	slot := func(n int) *int { return &n }
	card := func(i, at int, size, name string, elements ...string) tessera.Tile {
		x, _ := json.Marshal(map[string]any{"state": "1", "name": name, "attributes": map[string]any{"elements": elements}})
		o, _ := json.Marshal(map[string]any{"size": size, "plugin": map[string]any{"show_name": true}})
		return tessera.Tile{I: i, Slot: slot(at), Entity: "plugin:custom_card.card", Name: name, State: "ok", A: json.RawMessage(`{}`), O: o, X: x}
	}
	l := &tessera.Layout{
		Rev:   "rings0123456789a",
		Begin: tessera.Begin{Pages: 1, Tiles: 2},
		Pages: []tessera.Page{{P: 0, Title: "Home", Items: json.RawMessage(`[]`)}},
		Tiles: []tessera.Tile{
			card(0, 0, "4x2", "Air", "ring|820|ppm|CO2|yellow|molecule-co2|400|2000", "ring|46|%|Humidity|green|water-percent|0|100",
				"ring|21|°C|Temperature|blue|thermometer|10|30"),
			card(1, 8, "4x1", "Entrance", "value|5358||Scanned", "value|5933||Valid", "ring|90|%|Share|purple||0|100"),
		},
	}
	tessera.Preview(l, 4, 3)
	t.Cleanup(func() { tessera.Preview(nil, 0, 0) })
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, show8Wide, show8High)))
	v, _ := tessera.Current()
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardTessera, tessera: v, tessOn: true})
	if dir := os.Getenv("TECHO5_PREVIEW"); dir != "" {
		if f, err := os.Create(filepath.Join(dir, "custom-card-rings.png")); err == nil {
			_ = png.Encode(f, r.dst)
			f.Close()
		}
	}
	count := func(c color.RGBA) int {
		n := 0
		for i := 0; i < len(r.dst.Pix); i += 4 {
			if r.dst.Pix[i] == c.R && r.dst.Pix[i+1] == c.G && r.dst.Pix[i+2] == c.B {
				n++
			}
		}
		return n
	}
	for _, name := range []string{"yellow", "green", "blue", "purple"} {
		if n := count(swatches[name]); n < 300 {
			t.Errorf("the %s ring drew %d pixels of its color", name, n)
		}
	}
}

// A bar with nothing but its number stands under the value before it, its number at the right of the
// value's, with no dot: the value keeps the room a line of its own would take, on a tile one row high.
func TestCustomCardBareBar(t *testing.T) {
	d := &ccDraw{parts: []ccPart{
		{kind: ccValue, value: "490", unit: "W", label: "Ahora", colored: true, color: swatches["green"], icon: "flash", max: 100},
		{kind: ccBar, value: "10", unit: "%", colored: true, color: swatches["green"], max: 100},
	}, rings: true}
	if l := d.stacked(2); len(l) != 1 || l[0].count != 1 || l[0].bar != 1 {
		t.Fatalf("lines %+v", l)
	}
	d.parts[1].label = "Share"
	if l := d.stacked(2); len(l) != 2 {
		t.Errorf("a bar with a label joined the values: %+v", l)
	}
	d.parts[1].label = ""
	if ccBarOnly(d.parts[1]) || !ccBarOnly(ccPart{kind: ccBar, value: "10"}) {
		t.Error("a bare bar without a unit is the bar alone, one with a unit writes its number")
	}

	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Dashboard().Mode(config.DashboardTessera); err != nil {
		t.Fatal(err)
	}
	slot := func(n int) *int { return &n }
	x, _ := json.Marshal(map[string]any{"state": "490", "name": "Consumo Energía", "attributes": map[string]any{
		"elements": []string{"value|490|W|Ahora|green|flash", "bar|10|||green||0|100"}}})
	small := func(per string) []byte {
		o, _ := json.Marshal(map[string]any{"size": "wide", "plugin": map[string]any{"show_name": true, "coloured_values": true, "per_row": per}})
		return o
	}
	l := &tessera.Layout{
		Rev:   "bare0123456789ab",
		Begin: tessera.Begin{Pages: 1, Tiles: 2},
		Pages: []tessera.Page{{P: 0, Title: "Home", Items: json.RawMessage(`[]`)}},
		Tiles: []tessera.Tile{
			{I: 0, Slot: slot(4), Entity: "plugin:custom_card.card", Name: "Consumo Energía", State: "ok", A: json.RawMessage(`{}`), O: small("two"), X: x},
			{I: 1, Slot: slot(10), Entity: "plugin:custom_card.card", Name: "Consumo Energía", State: "ok", A: json.RawMessage(`{}`), O: small("auto"), X: x},
		},
	}
	tessera.Preview(l, 6, 5)
	t.Cleanup(func() { tessera.Preview(nil, 0, 0) })
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, show8Wide, show8High)))
	v, _ := tessera.Current()
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardTessera, tessera: v, tessOn: true})
	if dir := os.Getenv("TECHO5_PREVIEW"); dir != "" {
		if f, err := os.Create(filepath.Join(dir, "custom-card-consumo.png")); err == nil {
			_ = png.Encode(f, r.dst)
			f.Close()
		}
	}
}
