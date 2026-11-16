//go:build !dot && !spot

package display

import (
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A finger that rolled a little as it pressed a tile, further than the touchscreen follows a finger from
// but no further than a tap may wander anywhere else, tapped it. One that went further, slid a level,
// never moved, or lifted off the tile did not.
func TestATapThatWanderedOnATileIsATap(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls()})
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	tiles, _ := r.dash()
	if len(tiles) == 0 {
		t.Fatal("the page drew no tiles")
	}
	tile := tiles[0]
	at := image.Pt(tile.r.Min.X+tile.r.Dx()/2, tile.r.Min.Y+tile.r.Dy()/2)
	if tile.r.Dx() < 4*tapWander || tile.r.Dy() < 2*tapWander+4 {
		t.Fatalf("the tile %v is too small for this test", tile.r)
	}
	down := drawnDrag{at: at, tile: &tile, down: true, moved: true}

	if !d.wanderedTap(down, at.X+holdStill+3, at.Y+5) {
		t.Error("a finger that rolled 15 px on its tile did not tap it")
	}
	scrolled := down
	scrolled.scrolling = true
	if !d.wanderedTap(scrolled, at.X, at.Y+tapWander) {
		t.Error("a finger that drifted down its tile by a tap's allowance did not tap it")
	}
	if d.wanderedTap(down, at.X+tapWander+1, at.Y) {
		t.Error("a finger that went further than a tap may wander tapped")
	}
	slid := down
	slid.sliding = true
	if d.wanderedTap(slid, at.X+15, at.Y) {
		t.Error("a finger that slid a level tapped")
	}
	still := down
	still.moved = false
	if d.wanderedTap(still, at.X, at.Y) {
		t.Error("a finger that never moved is a long press, not a wandered tap")
	}
	edge := drawnDrag{at: image.Pt(tile.r.Max.X-2, at.Y), tile: &tile, down: true, moved: true}
	if d.wanderedTap(edge, tile.r.Max.X+10, at.Y) {
		t.Error("a finger that lifted off its tile tapped it")
	}
}
