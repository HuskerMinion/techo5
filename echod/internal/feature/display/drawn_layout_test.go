//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// The Dashboard tile layout setting can put a tile's name under its badge and state. Such a tile is
// taller than a side by side one at every size, is still a tap where it is drawn, and a view of four
// of them is still four taps. With SHOW_PREVIEW set, each is written there to look at.
func TestDashboardNameBelowTiles(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	at := time.Date(2026, 10, 4, 14, 7, 0, 0, time.Local)
	tile := func(name, value string) dashboard.Tile {
		return dashboard.Tile{Name: name, Icon: "lightbulb", Value: value, On: true,
			Tap: &dashboard.Action{Entity: "light.x", Service: "light.toggle"}}
	}
	view := dashboard.Drawn{Sections: []dashboard.Section{{Blocks: []dashboard.Block{
		{Heading: "Names"},
		{Tiles: []dashboard.Tile{
			tile("Ceiling", "On · 80%"),
			tile("Kitchen island pendant lights", "On"),
			tile("Master bedroom ensuite extractor fan boost", "Off"),
			tile("Understairs cupboard motion sensor battery", ""),
		}},
	}}}}
	for _, size := range []string{"", "large", "fill"} {
		heights := map[string]int{}
		for _, layout := range []string{"", "below"} {
			img := image.NewRGBA(image.Rect(0, 0, show8Wide, show8High))
			r := newRenderer(img)
			r.draw(scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: view,
				dashTiles: size, dashLayout: layout})
			tiles, _ := r.dash()
			if len(tiles) != 4 {
				t.Fatalf("%q %q: %d tiles to tap, want 4", size, layout, len(tiles))
			}
			for i, z := range tiles {
				if z.r.Dy() != tiles[0].r.Dy() {
					t.Errorf("%q %q: tile %d is %d tall, the first %d", size, layout, i, z.r.Dy(), tiles[0].r.Dy())
				}
				if z.action == nil {
					t.Errorf("%q %q: tile %d is not a tap", size, layout, i)
				}
			}
			heights[layout] = tiles[0].r.Dy()
			if dir != "" {
				name := "dash-layout-" + map[string]string{"": "normal", "large": "large", "fill": "fill"}[size] + "-" +
					map[string]string{"": "side", "below": "below"}[layout] + ".png"
				f, err := os.Create(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
				png.Encode(f, img)
				f.Close()
			}
		}
		if size != "fill" && heights["below"] <= heights[""] {
			t.Errorf("%q: name below tiles are %d tall, side by side %d: not taller", size, heights["below"], heights[""])
		}
	}
}

// A level on a name below tile still slides: the tile is a zone with its level, and the bar along its
// foot is drawn.
func TestDashboardNameBelowTileKeepsItsLevel(t *testing.T) {
	at := time.Date(2026, 10, 4, 14, 7, 0, 0, time.Local)
	light := dashboard.Tile{Name: "Ceiling", Icon: "lightbulb", Value: "80%", On: true,
		Tap:    &dashboard.Action{Entity: "light.ceiling", Service: "light.toggle"},
		Adjust: &dashboard.Adjust{Entity: "light.ceiling", Kind: "brightness", Min: 0, Max: 100, Value: 80}}
	view := dashboard.Drawn{Sections: []dashboard.Section{{Blocks: []dashboard.Block{{Tiles: []dashboard.Tile{light}}}}}}
	img := image.NewRGBA(image.Rect(0, 0, show8Wide, show8High))
	r := newRenderer(img)
	r.draw(scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: view, dashLayout: "below"})
	tiles, _ := r.dash()
	if len(tiles) != 1 || tiles[0].adjust == nil || tiles[0].adjust.Entity != "light.ceiling" {
		t.Fatalf("a name below light is %+v, want one tile that slides", tiles)
	}
}

// A name below tile taller than its words need has them about the middle of it, not floated to the top
// with the room all under them. The foot of the words' own height is kept for a level's track, so the
// room below is a little more than above.
func TestDashboardNameBelowTileIsCentered(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, show8Wide, show8High))
	r := newRenderer(img)
	r.nameBelow = true
	pal := r.palette(dashboard.Theme{})
	box := image.Rect(100, 100, 100+r.s(220), 100+r.s(380))
	tile := dashboard.Tile{Name: "Kitchen", Icon: "lightbulb", Value: "On", On: true}
	r.tile(box, tile, pal, dashAdjusting{})

	first, last := -1, -1
	rad := int(pal.rad) + 1
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X + rad; x < box.Max.X-rad; x++ {
			if img.RGBAAt(x, y) != pal.card {
				if first < 0 {
					first = y
				}
				last = y
				break
			}
		}
	}
	if first < 0 {
		t.Fatal("nothing drawn in the tile")
	}
	above, below := first-box.Min.Y, box.Max.Y-1-last
	spare := box.Dy() - r.s(tileBelowH)
	if above < spare/3 || below < spare/3 {
		t.Errorf("room above the words %d, below %d: not about the middle", above, below)
	}
}
