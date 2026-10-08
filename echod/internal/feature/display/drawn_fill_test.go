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

// fourControls is the hallway panel #84 asked for: three lights and a gate, under one heading.
func fourControls() dashboard.Drawn {
	tile := func(name, icon, value string, on bool) dashboard.Tile {
		return dashboard.Tile{Name: name, Icon: icon, Value: value, On: on,
			Tap: &dashboard.Action{Entity: "light." + name, Service: "light.toggle"}}
	}
	return dashboard.Drawn{Sections: []dashboard.Section{{Blocks: []dashboard.Block{
		{Heading: "Hallway"},
		{Tiles: []dashboard.Tile{
			tile("Ceiling", "lightbulb", "On · 80%", true),
			tile("Porch", "outdoor-lamp", "Off", false),
			tile("Stairs", "stairs", "On", true),
			tile("Gate", "gate", "Closed", false),
		}},
	}}}}
}

// Fill the screen lays a view of four tiles out two by two over the whole page, each tile tappable
// where it is drawn and a quarter of the page; Large draws them a row each and taller; the usual size
// is untouched. With SHOW_PREVIEW set, each is written there to look at.
func TestDashboardTilesFillTheScreen(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	at := time.Date(2026, 10, 4, 14, 7, 0, 0, time.Local)
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}} {
		sizes := map[string]int{}
		for _, size := range []string{"", "large", "fill"} {
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			r := newRenderer(img)
			r.draw(scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls(), dashTiles: size})
			tiles, _ := r.dash()
			if len(tiles) != 4 {
				t.Fatalf("%s%s: %d tiles to tap, want 4", size, panel.name, len(tiles))
			}
			sizes[size] = tiles[0].r.Dy()
			if size == "fill" {
				quarter := tiles[0].r.Dx() * tiles[0].r.Dy() * 5
				if quarter < panel.wide*panel.high {
					t.Errorf("fill%s: a tile is %v, not near a quarter of the page", panel.name, tiles[0].r)
				}
				if tiles[1].r.Min.Y != tiles[0].r.Min.Y || tiles[2].r.Min.Y <= tiles[0].r.Max.Y {
					t.Errorf("fill%s: not two by two: %v %v %v", panel.name, tiles[0].r, tiles[1].r, tiles[2].r)
				}
			}
			if dir != "" {
				name := "dash-tiles-" + map[string]string{"": "normal", "large": "large", "fill": "fill"}[size] + panel.name + ".png"
				f, err := os.Create(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
				png.Encode(f, img)
				f.Close()
			}
		}
		if !(sizes[""] < sizes["large"] && sizes["large"] < sizes["fill"]) {
			t.Errorf("%s: tile heights normal %d, large %d, fill %d: not growing", panel.name, sizes[""], sizes["large"], sizes["fill"])
		}
	}
}

// A view with anything but tiles on it is not filled: it is drawn large, and scrolls as usual.
func TestDashboardFillNeedsOnlyTiles(t *testing.T) {
	v := fourControls()
	if _, ok := fewTiles(v); !ok {
		t.Fatal("four tiles under a heading were not few enough to fill")
	}
	v.Sections[0].Blocks = append(v.Sections[0].Blocks, dashboard.Block{Text: []string{"Hello"}})
	if _, ok := fewTiles(v); ok {
		t.Error("a view with a text card was filled")
	}
	for n, want := range map[int]int{1: 1, 2: 2, 3: 3, 4: 2, 5: 3, 6: 3, 9: 3} {
		if got := fillColumns(n); got != want {
			t.Errorf("%d tiles: %d across, want %d", n, got, want)
		}
	}
}
