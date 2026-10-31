//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

func levelLight(name string, on bool, level float64) dashboard.Tile {
	value := "Off"
	if on {
		value = "On · " + strconv.Itoa(int(level)) + "%"
	}
	return dashboard.Tile{Name: name, Icon: "lightbulb", Value: value, On: on,
		Tap:    &dashboard.Action{Entity: "light." + name, Service: "light.toggle"},
		Adjust: &dashboard.Adjust{Entity: "light." + name, Kind: "brightness", Min: 0, Max: 100, Value: level}}
}

// A level is drawn as a bar on a track along the foot of its tile, and still slides: the tile is a zone
// with its level. With SHOW_PREVIEW set, each size is written there to look at.
func TestDashboardLevelOnATrack(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	at := time.Date(2026, 10, 4, 14, 7, 0, 0, time.Local)
	view := dashboard.Drawn{Sections: []dashboard.Section{{Blocks: []dashboard.Block{{Heading: "Living Room"},
		{Tiles: []dashboard.Tile{levelLight("Kitchen", true, 100), levelLight("Lounge", true, 60),
			levelLight("Hall", true, 5), levelLight("Study", false, 0)}}}}}}
	for _, size := range []string{"", "large", "fill"} {
		img := image.NewRGBA(image.Rect(0, 0, show8Wide, show8High))
		r := newRenderer(img)
		r.draw(scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: view, dashTiles: size})
		tiles, _ := r.dash()
		if len(tiles) != 4 {
			t.Fatalf("%q: %d tiles, want 4", size, len(tiles))
		}
		for i, z := range tiles {
			if want := view.Sections[0].Blocks[1].Tiles[i].Adjust.Entity; z.adjust == nil || z.adjust.Entity != want {
				t.Errorf("%q: tile %d is %+v, want one that slides %s", size, i, z.adjust, want)
			}
		}
		if dir != "" {
			f, err := os.Create(filepath.Join(dir, "dash-level-"+map[string]string{"": "normal", "large": "large", "fill": "fill"}[size]+".png"))
			if err != nil {
				t.Fatal(err)
			}
			png.Encode(f, img)
			f.Close()
		}
	}
}

// trackRows is how many rows of a tile's foot, at x, are not the tile's own color: the track, counted
// up from the tile's bottom edge.
func trackRows(img *image.RGBA, box image.Rectangle, x int, card interface{ RGBA() (r, g, b, a uint32) }) (rows, fromBottom int) {
	same := func(y int) bool {
		r1, g1, b1, _ := img.At(x, y).RGBA()
		r2, g2, b2, _ := card.RGBA()
		return r1 == r2 && g1 == g2 && b1 == b2
	}
	y := box.Max.Y - 1
	for ; y >= box.Min.Y && same(y); y-- {
		fromBottom++
	}
	for ; y >= box.Min.Y && !same(y); y-- {
		rows++
	}
	return rows, fromBottom
}

// A light that is off still has its track, the sign that it can be slid, and no bar on it; a tile that
// is on has the bar from the track's start.
func TestDashboardLevelTrackShowsWhenOff(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, show8Wide, show8High))
	r := newRenderer(img)
	pal := r.palette(dashboard.Theme{})
	box := image.Rect(100, 100, 100+r.s(220), 100+r.s(tileH))
	r.tile(box, levelLight("Kitchen", false, 0), pal, dashAdjusting{})
	if rows, _ := trackRows(img, box, box.Min.X+box.Dx()/2, pal.card); rows == 0 {
		t.Error("no track under a light that is off")
	}
	if rows, _ := trackRows(img, box, box.Min.X+r.s(14)+r.s(3), pal.card); rows == 0 {
		t.Fatal("no track at its start")
	}
	if img.RGBAAt(box.Min.X+r.s(14)+r.s(3), box.Max.Y-r.s(6)) == (pal.active) {
		t.Error("a bar on a light that is off")
	}

	img2 := image.NewRGBA(image.Rect(0, 0, show8Wide, show8High))
	r2 := newRenderer(img2)
	r2.tile(box, levelLight("Kitchen", true, 100), pal, dashAdjusting{})
	if got := img2.RGBAAt(box.Min.X+box.Dx()/2, box.Max.Y-r2.s(6)); got != pal.active {
		t.Errorf("a light on at 100%% has %v along its track, want the active color %v", got, pal.active)
	}
}

// The track is clear of a tile's words, and thicker on a larger tile as its words are.
func TestDashboardLevelTrackIsClearAndScales(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, show8Wide, show8High))
	r := newRenderer(img)
	pal := r.palette(dashboard.Theme{})

	usual := image.Rect(100, 100, 100+r.s(220), 100+r.s(tileH))
	r.tile(usual, levelLight("Kitchen", true, 60), pal, dashAdjusting{})
	usualRows, bottom := trackRows(img, usual, usual.Min.X+usual.Dx()/2, pal.card)
	// The two rows over the track hold nothing: no word runs into it.
	for dy := 1; dy <= 2; dy++ {
		y := usual.Max.Y - bottom - usualRows - dy
		for x := usual.Min.X + r.s(14); x < usual.Max.X-r.s(14); x++ {
			if img.RGBAAt(x, y) != pal.card {
				t.Fatalf("a word runs into the track: pixel %d,%d is not the tile's own", x, y)
			}
		}
	}

	large := image.Rect(100, 300, 100+r.s(330), 300+r.s(tileH)*3/2)
	r.tile(large, levelLight("Kitchen", true, 60), pal, dashAdjusting{})
	largeRows, _ := trackRows(img, large, large.Min.X+large.Dx()/2, pal.card)
	if largeRows <= usualRows {
		t.Errorf("track is %d rows on a larger tile, %d on the usual one: not growing", largeRows, usualRows)
	}
}
