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

// fanPage is a display with the fans' dashboard drawn, and the middle of each fan's tile.
func fanPage(t *testing.T) (*Display, map[string]image.Point) {
	t.Helper()
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fans()})
	d := &Display{r: r, poke: make(chan struct{}, 1), on: true}
	at := map[string]image.Point{}
	tiles, _ := r.dash()
	for _, tile := range tiles {
		at[tile.entity] = image.Pt((tile.r.Min.X+tile.r.Max.X)/2, (tile.r.Min.Y+tile.r.Max.Y)/2)
	}
	if _, ok := at["fan.ceiling"]; !ok {
		t.Fatal("the page drew no ceiling fan")
	}
	return d, at
}

// A long press opens the tile's sheet at the moment it becomes one, with the finger still down, and the
// lift that follows does nothing more: it does not tap the sheet, put it away, or work the tile.
func TestALongPressOpensWhenItBecomesOne(t *testing.T) {
	calls := fakeFan(t)
	d, at := fanPage(t)
	p := at["fan.ceiling"]

	d.fingerLanded(touch.Point{X: p.X, Y: p.Y})
	d.dashGesture(touch.Gesture{Kind: touch.Hold, X: p.X, Y: p.Y})
	if !d.colorOpen() {
		t.Fatal("the hold came and the sheet is not up: it waits for the lift")
	}
	if d.press.set {
		t.Error("the tile still shows a press under the sheet it opened")
	}
	// The finger drifts a little under the sheet before it lifts: nothing follows it.
	d.dashGesture(touch.Gesture{Kind: touch.Drag, X: p.X + 30, Y: p.Y + 2})
	d.dashGesture(touch.Gesture{Kind: touch.Release, X: p.X + 30, Y: p.Y + 2})
	if !d.colorOpen() {
		t.Error("the lift after the long press put the sheet away")
	}
	if len(*calls) != 0 {
		t.Errorf("the lift after the long press acted: %v", *calls)
	}

	// The finger after that one is the sheet's as ever: a press beside the sheet puts it away.
	d.r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fans(), dashFan: d.fanViewNow()})
	d.dashGesture(touch.Gesture{Kind: touch.Tap, X: showWide - 30, Y: showHigh - 6})
	if on := d.onColorSheet(showWide-30, showHigh-6); !on && d.colorOpen() {
		t.Error("a tap beside the sheet left it up")
	}
}

// A finger the touchscreen followed because it moved is not a long press, however long it stays down:
// it scrolls or slides as it did, and no sheet opens when the hold is reported or when it lifts.
func TestAFingerThatMovedIsNotALongPress(t *testing.T) {
	calls := fakeFan(t)
	d, at := fanPage(t)
	p := at["fan.ceiling"]

	// Down the page: a scroll.
	d.dashGesture(touch.Gesture{Kind: touch.Hold, X: p.X, Y: p.Y, Moved: true})
	if d.colorOpen() {
		t.Fatal("a finger that had only begun to move opened the sheet")
	}
	d.dashGesture(touch.Gesture{Kind: touch.Drag, X: p.X, Y: p.Y - 60})
	d.mu.Lock()
	scrolling := d.dashDrag.scrolling
	d.mu.Unlock()
	if !scrolling {
		t.Error("a finger moving up the page is not scrolling it")
	}
	d.dashGesture(touch.Gesture{Kind: touch.Release, X: p.X, Y: p.Y - 60})
	if d.colorOpen() || len(*calls) != 0 {
		t.Errorf("a scroll opened the sheet (%v) or acted (%v)", d.colorOpen(), *calls)
	}

	// Along the tile: its level slides.
	d.dashGesture(touch.Gesture{Kind: touch.Hold, X: p.X, Y: p.Y, Moved: true})
	d.dashGesture(touch.Gesture{Kind: touch.Drag, X: p.X + 40, Y: p.Y})
	d.mu.Lock()
	sliding := d.dashDrag.sliding
	d.mu.Unlock()
	if !sliding {
		t.Error("a finger moving along a fan's tile is not sliding its speed")
	}
	d.mu.Lock()
	d.dashDrag = drawnDrag{} // lifted, without sending the level to a Home Assistant that is not there
	d.mu.Unlock()
	if d.colorOpen() {
		t.Error("a slide opened the sheet")
	}
}

// A hold that comes of a finger at the page's edge, pulling the page, is the edge's: no tile under it
// opens anything.
func TestAHoldAtTheEdgeIsNotATilesLongPress(t *testing.T) {
	fakeFan(t)
	d, at := fanPage(t)
	p := at["fan.ceiling"]
	d.dashGesture(touch.Gesture{Kind: touch.Hold, X: 2, Y: p.Y})
	if d.colorOpen() {
		t.Error("a finger held at the bezel opened a sheet")
	}
	d.mu.Lock()
	d.dashEdge = edgeNone
	d.mu.Unlock()
}

// A long press on a tile that has an action and no sheet does nothing: a lock or a gate held to see
// more is not unlocked or opened. A tap on it still acts, and so does a tap that wandered a little.
func TestALongPressOnATileWithNoSheetDoesNothing(t *testing.T) {
	var sent []dashboard.Action
	prev := tileTap
	tileTap = func(a dashboard.Action) { sent = append(sent, a) }
	t.Cleanup(func() { tileTap = prev })

	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls()})
	d := &Display{r: r, poke: make(chan struct{}, 1), on: true}
	var gate *dashTile
	tiles, _ := r.dash()
	for i := range tiles {
		if tiles[i].action != nil && tiles[i].action.Entity == "light.Gate" {
			gate = &tiles[i]
		}
	}
	if gate == nil {
		t.Fatal("the page drew no gate")
	}
	p := image.Pt((gate.r.Min.X+gate.r.Max.X)/2, (gate.r.Min.Y+gate.r.Max.Y)/2)

	d.fingerLanded(touch.Point{X: p.X, Y: p.Y})
	d.dashGesture(touch.Gesture{Kind: touch.Hold, X: p.X, Y: p.Y})
	if len(sent) != 0 {
		t.Fatalf("holding the gate ran its action: %v", sent)
	}
	if d.colorOpen() {
		t.Error("holding a tile with no sheet opened one")
	}
	if d.press.set {
		t.Error("the tile still shows a press once the hold has come to nothing")
	}
	d.dashGesture(touch.Gesture{Kind: touch.Release, X: p.X, Y: p.Y})
	if len(sent) != 0 {
		t.Fatalf("lifting after holding the gate ran its action: %v", sent)
	}

	d.dashGesture(touch.Gesture{Kind: touch.Tap, X: p.X, Y: p.Y})
	if len(sent) != 1 || sent[0].Entity != "light.Gate" {
		t.Fatalf("a tap on the gate sent %v, want its action once", sent)
	}
	// A tap that rolled far enough to be followed, and no further than a tap may.
	d.dashGesture(touch.Gesture{Kind: touch.Hold, X: p.X, Y: p.Y, Moved: true})
	d.dashGesture(touch.Gesture{Kind: touch.Drag, X: p.X + 14, Y: p.Y})
	d.dashGesture(touch.Gesture{Kind: touch.Release, X: p.X + 14, Y: p.Y})
	if len(sent) != 2 {
		t.Errorf("a tap that wandered sent %v, want the action a second time", sent)
	}
}
