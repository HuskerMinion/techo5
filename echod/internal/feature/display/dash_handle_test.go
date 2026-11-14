//go:build !dot && !spot

package display

import (
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// A drawn dashboard shows its handle at the middle of the left edge, clear of the tiles, and a tap on
// it is the handle's; one on a tile, or beside the handle's height, is not.
func TestTheDashboardShowsItsWayOut(t *testing.T) {
	for _, panel := range showPanels() {
		img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		r := newRenderer(img)
		r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls()})
		at := image.Pt(r.s(dashHandleIn)+r.s(dashHandleWide)/2, panel.high/2)
		if c := img.RGBAAt(at.X, at.Y); c.R < 0xa0 {
			t.Errorf("%s: no handle drawn at %v (%v)", panel.name, at, c)
		}
		if !r.dashHandleAt(at.X, at.Y) {
			t.Errorf("%s: the handle is not where it is tapped", panel.name)
		}
		tiles, _ := r.dash()
		for _, tile := range tiles {
			mid := image.Pt(tile.r.Min.X+tile.r.Dx()/2, tile.r.Min.Y+tile.r.Dy()/2)
			if r.dashHandleAt(mid.X, mid.Y) {
				t.Errorf("%s: the middle of the tile at %v counts as the handle", panel.name, tile.r)
			}
			if tile.r.Min.X < r.s(dashHandleIn)+r.s(dashHandleWide)+r.s(2) && abs(mid.Y-panel.high/2) < tile.r.Dy() {
				t.Errorf("%s: the tile at %v is under the handle", panel.name, tile.r)
			}
		}
		if r.dashHandleAt(at.X, r.s(8)) || r.dashHandleAt(panel.wide/2, panel.high/2) {
			t.Errorf("%s: the handle is tapped away from itself", panel.name)
		}
	}
}

// A tap on the handle puts the dashboard away.
func TestATapOnTheHandleLeavesTheDashboard(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls()})
	d := &Display{r: r, poke: make(chan struct{}, 1), dash: true}
	d.dashGesture(touch.Gesture{Kind: touch.Tap, X: r.s(8), Y: showHigh / 2})
	if d.dash {
		t.Error("a tap on the handle left the dashboard up")
	}
}
