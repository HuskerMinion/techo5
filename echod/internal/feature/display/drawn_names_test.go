//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// A tile's name is drawn at the usual size when it fits; when it does not, smaller on one line, then on
// two, and cut short only at the end of the second.
func TestTileNameWrapsBeforeItClips(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	usual, small := r.textFace(false, 29), r.textFace(false, 21)
	smaller := func() font.Face { return small }

	lines, face := r.tileName(usual, smaller, "Porch", r.width(usual, "Porch"))
	if len(lines) != 1 || lines[0] != "Porch" || face != usual {
		t.Errorf("a name that fits: %q at the usual size %v", lines, face == usual)
	}

	long := "Kitchen island pendant lights"
	lines, face = r.tileName(usual, smaller, long, r.width(small, long))
	if len(lines) != 1 || lines[0] != long || face != small {
		t.Errorf("a name that fits smaller: %q, smaller %v", lines, face == small)
	}

	room := r.width(small, "Kitchen island pendant") + 2
	lines, face = r.tileName(usual, smaller, long, room)
	if len(lines) != 2 || lines[0] != "Kitchen island pendant" || lines[1] != "lights" || face != small {
		t.Errorf("a name on two lines: %q, smaller %v", lines, face == small)
	}

	words := strings.Repeat("Hallway ceiling light ", 4)
	lines, _ = r.tileName(usual, smaller, words, room)
	if len(lines) != 2 || !strings.HasSuffix(lines[1], "…") {
		t.Errorf("a name too long for two lines: %q, want two, the second ending in an ellipsis", lines)
	}
	for _, l := range lines {
		if w := r.width(small, l); w > room {
			t.Errorf("line %q is %d wide, room is %d", l, w, room)
		}
	}

	if lines, _ = r.tileName(usual, smaller, "", room); len(lines) != 1 || lines[0] != "" {
		t.Errorf("no name: %q", lines)
	}

	// A Japanese word too long for a line is split by the line, and the pieces stay one word: no space
	// is put in it, whether the name is that word alone or has others before it.
	for _, name := range []string{strings.Repeat("あ", 60), "Hall " + strings.Repeat("あ", 60)} {
		lines, _ = r.tileName(usual, smaller, name, room)
		if len(lines) != 2 {
			t.Errorf("%q: %d lines, want 2", name, len(lines))
			continue
		}
		if strings.Contains(lines[1], " ") || strings.Contains(lines[0], " ") && !strings.HasPrefix(name, lines[0]) {
			t.Errorf("%q drawn as %q: a space where there was none", name, lines)
		}
	}

	// The smaller face is not made for a name that fits.
	asked := false
	r.tileName(usual, func() font.Face { asked = true; return small }, "Porch", r.width(usual, "Porch"))
	if asked {
		t.Error("the smaller face was made for a name that fits")
	}
}

// Long names change how a tile's words are drawn and nothing else: its height is the usual one, and
// it is still a tap. With SHOW_PREVIEW set, each size is written there to look at.
func TestDashboardLongTileNames(t *testing.T) {
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
		img := image.NewRGBA(image.Rect(0, 0, show8Wide, show8High))
		r := newRenderer(img)
		r.draw(scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: view, dashTiles: size})
		tiles, _ := r.dash()
		if len(tiles) != 4 {
			t.Fatalf("%q: %d tiles to tap, want 4", size, len(tiles))
		}
		for i, z := range tiles {
			if z.r.Dy() != tiles[0].r.Dy() {
				t.Errorf("%q: tile %d is %d tall, the first %d", size, i, z.r.Dy(), tiles[0].r.Dy())
			}
		}
		if dir != "" {
			name := "dash-names-" + map[string]string{"": "normal", "large": "large", "fill": "fill"}[size] + ".png"
			f, err := os.Create(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			png.Encode(f, img)
			f.Close()
		}
	}
}
