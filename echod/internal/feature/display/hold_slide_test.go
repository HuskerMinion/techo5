//go:build !dot && !spot

package display

import (
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// A finger that rests on a level tile with no sheet until it is a hold, and then slides, is sliding:
// the hold opened nothing, so its lift is the slide's and sets the level, and the tile is not shown
// pressed while its level moves.
func TestAHoldThenASlideStillSlides(t *testing.T) {
	view := dashboard.Drawn{Sections: []dashboard.Section{{Blocks: []dashboard.Block{
		{Tiles: []dashboard.Tile{levelLight("Hall", true, 40), levelLight("Study", true, 60)}}}}}}
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: view})
	d := &Display{r: r, poke: make(chan struct{}, 1), on: true}
	tiles, _ := r.dash()
	if len(tiles) == 0 || tiles[0].adjust == nil {
		t.Fatal("the page drew no level tile")
	}
	p := image.Pt((tiles[0].r.Min.X+tiles[0].r.Max.X)/2, (tiles[0].r.Min.Y+tiles[0].r.Max.Y)/2)

	d.fingerLanded(touch.Point{X: p.X, Y: p.Y})
	d.dashGesture(touch.Gesture{Kind: touch.Hold, X: p.X, Y: p.Y})
	if d.colorOpen() {
		t.Skip("this light has a color sheet here; the case is a light with none")
	}
	if !d.dashDrag.held {
		t.Fatal("the hold was not taken as one")
	}
	d.dashGesture(touch.Gesture{Kind: touch.Drag, X: p.X + d.r.s(60), Y: p.Y})
	d.mu.Lock()
	dr, press := d.dashDrag, d.press
	d.mu.Unlock()
	if !dr.sliding {
		t.Fatal("a drag across after the hold did not slide")
	}
	if dr.held {
		t.Error("the slide is still marked a hold: its lift would set nothing and the level snap back")
	}
	if press.set {
		t.Error("the tile still shows pressed while its level slides")
	}
}

// A tap that wandered and scrolled the page a little acts on the tile it came down on, not on what is
// under that point in the frame drawn scrolled.
func TestAWanderedTapActsOnItsOwnTile(t *testing.T) {
	var sent []dashboard.Action
	prev := tileTap
	tileTap = func(a dashboard.Action) { sent = append(sent, a) }
	t.Cleanup(func() { tileTap = prev })

	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls()})
	d := &Display{r: r, poke: make(chan struct{}, 1), on: true}
	tiles, _ := r.dash()
	var gate *dashTile
	for i := range tiles {
		if tiles[i].action != nil && tiles[i].action.Entity == "light.Gate" {
			gate = &tiles[i]
		}
	}
	if gate == nil {
		t.Fatal("the page drew no gate")
	}
	// Came down just inside the gate's top edge, then the page scrolled under the finger and the frame
	// was drawn so: at that point now is whatever is above the gate.
	at := image.Pt((gate.r.Min.X+gate.r.Max.X)/2, gate.r.Min.Y+2)
	d.dashDrag = drawnDrag{at: at, tile: gate, down: true, moved: true, scrolling: true}
	d.dashScroll = d.r.s(20)
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls(), dashScroll: d.dashScroll})
	d.drawnRelease(at.X, at.Y+d.r.s(4))
	if len(sent) != 1 || sent[0].Entity != "light.Gate" {
		t.Fatalf("the wandered tap sent %v, want the gate's action once", sent)
	}
	if d.dashScroll != 0 {
		t.Errorf("the scroll the tap made was not put back: %d", d.dashScroll)
	}
}
