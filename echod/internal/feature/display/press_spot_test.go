//go:build spot

package display

import (
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// The press on the round panel (press.go): the ring menu's items, the face's buttons, the settings'
// tiles and rows and the dashboard's tiles show a finger on them, as their tap finds it, and nothing
// shows where a tap does nothing.

func spotMid(b image.Rectangle) image.Point { return b.Min.Add(b.Max).Div(2) }

func spotRenderer() *roundRenderer {
	return newRoundRenderer(image.NewRGBA(image.Rect(0, 0, side, side)))
}

// spotPlace is a face, a point where a tap acts and the box the press must keep to there, and points off
// what it has to press: where a tap does nothing, or (the music's face, the clock) where it is the
// whole face's and no button's.
type spotPlace struct {
	name  string
	scene roundScene
	on    image.Point
	box   image.Rectangle
	off   []image.Point
}

func spotPlaces() []spotPlace {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	menu := roundScene{now: at, phase: "idle", menuOpen: true, menuMode: modeMain}
	n := len(dialItems(menu))
	menu.menuSel, menu.menuRot = 0, restFor(0, n)
	item := func(i int) (image.Point, image.Rectangle) {
		x, y := itemPos(i, n, menu.menuRot)
		p := image.Pt(int(x), int(y))
		return p, image.Rect(p.X-chosenRing-10, p.Y-chosenRing-10, p.X+chosenRing+10, p.Y+chosenRing+10)
	}
	two, twoBox := item(2)
	_, topBox := item(0)
	// Between the middle and the ring, halfway round from the top: a tap there puts the menu away.
	between := image.Pt(center+int(110*math.Sin(math.Pi/float64(n))), center-int(110*math.Cos(math.Pi/float64(n))))

	playing := roundScene{now: at, phase: "idle", nowPlaying: true, playing: true,
		radio: home.Radio{Now: "KXYZ 101.1", Title: "Take It Easy", Artist: "Eagles"}}
	contacts := roundScene{now: at, phase: "idle", menuOpen: true, menuMode: modeContacts, phoneReady: true,
		contacts: []phone.Callee{{Name: "Kitchen", Device: true}, {Name: "Alex", Number: "15551234567"}}}
	tiles := dashboard.Drawn{Sections: []dashboard.Section{{Blocks: []dashboard.Block{{Tiles: []dashboard.Tile{
		{Entity: "light.porch", Name: "Porch", Icon: "lightbulb", Value: "On", On: true,
			Tap: &dashboard.Action{Entity: "light.porch", Service: "light.toggle"}},
		{Entity: "light.hall", Name: "Hall", Icon: "lightbulb", Value: "Off",
			Tap: &dashboard.Action{Entity: "light.hall", Service: "light.toggle"}},
	}}}}}}
	dash := roundScene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: tiles}
	grid := spotScene(catDisplay)
	grid.sheetGrid = true

	return []spotPlace{
		{name: "dial-item", scene: menu, on: two, box: twoBox, off: []image.Point{between}},
		// The middle does the item at the top, so the press shows there.
		{name: "dial-middle", scene: menu, on: image.Pt(center, center), box: topBox},
		{name: "music-done", scene: playing, on: image.Pt(int(doneX), int(doneY)),
			box: image.Rect(int(doneX)-18-int(doneR)-4, int(doneY)-int(doneR)-4, int(doneX)+18+int(doneR)+4, int(doneY)+int(doneR)+4),
			off: []image.Point{image.Pt(center, 260)}},
		{name: "music-star", scene: playing, on: image.Pt(int(starX), int(starY)),
			box: image.Rect(int(starX-starR)-4, int(starY-starR)-4, int(starX+starR)+4, int(starY+starR)+4)},
		{name: "clock-call", scene: roundScene{now: at, phase: "idle", callButton: true},
			on: image.Pt(callButtonAt.x, callButtonAt.y), box: image.Rect(callButtonAt.x-callButtonAt.r-4, callButtonAt.y-callButtonAt.r-4,
				callButtonAt.x+callButtonAt.r+4, callButtonAt.y+callButtonAt.r+4), off: []image.Point{image.Pt(center, center)}},
		// Below the last contact there is nobody to call.
		{name: "contact-row", scene: contacts, on: image.Pt(center, contactFirstY-12),
			box: image.Rect(center-200, contactFirstY-12-contactRowH/2-4, center+200, contactFirstY-12+contactRowH/2+4),
			off: []image.Point{image.Pt(center, contactFirstY+3*contactRowH-12)}},
		{name: "dashboard-tile", scene: dash, off: []image.Point{image.Pt(center, side-30)}},
		{name: "settings-tile", scene: grid},
	}
}

// A finger where a face's tap acts shows there and nowhere else, and the face after it is as it was; a
// finger off what there is to press changes nothing. With SPOT_PREVIEW set, each pressed face is written
// there to look at.
func TestASpotPressShowsWhereItsTapActs(t *testing.T) {
	dir := os.Getenv("SPOT_PREVIEW")
	for _, c := range spotPlaces() {
		plain := spotRenderer()
		plain.draw(c.scene)
		on, box := c.on, c.box
		switch c.name {
		case "dashboard-tile":
			tiles, _ := plain.dash()
			if len(tiles) == 0 {
				t.Fatalf("%s: no tiles", c.name)
			}
			on, box = spotMid(tiles[0].r), tiles[0].r.Inset(-4)
		case "settings-tile":
			for _, z := range plain.zones {
				if z.kind == zoneCat {
					on, box = spotMid(z.r), z.r.Inset(-4)
					break
				}
			}
		}
		press := func(p image.Point) *roundRenderer {
			r := spotRenderer()
			s := c.scene
			s.press = pressView{on: true, at: p}
			r.draw(s)
			return r
		}
		pressed := press(on)
		if got, differs := spotDiffBox(pressed, plain); !differs {
			t.Errorf("%s: a finger at %v looks as the face does without one", c.name, on)
		} else if !got.In(box) {
			t.Errorf("%s: the press shows over %v, outside %v", c.name, got, box)
		}
		if dir != "" {
			f, err := os.Create(filepath.Join(dir, "pressed-"+c.name+"-spot.png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, pressed.dst); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
		pressed.draw(c.scene)
		if got, differs := spotDiffBox(pressed, plain); differs {
			t.Errorf("%s: after the press the face still differs over %v", c.name, got)
		}
		for _, p := range c.off {
			if got, differs := spotDiffBox(press(p), plain); differs {
				t.Errorf("%s: a finger at %v, where a tap does nothing, changed the face over %v", c.name, p, got)
			}
		}
	}
}

// The face the ring menu is over shows no press: the finger is on the menu. Between the menu's middle
// and its ring, over a dashboard tile, a finger shows on neither.
func TestTheFaceUnderTheRingMenuIsNotPressed(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	tiles := dashboard.Drawn{Sections: []dashboard.Section{{Blocks: []dashboard.Block{{Tiles: []dashboard.Tile{
		{Entity: "light.porch", Name: "Porch", Icon: "lightbulb", Value: "On", On: true,
			Tap: &dashboard.Action{Entity: "light.porch", Service: "light.toggle"}},
	}}}}}}
	s := roundScene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: tiles}
	bare := spotRenderer()
	bare.draw(s)
	shown, _ := bare.dash()
	if len(shown) == 0 {
		t.Fatal("no tile")
	}
	s.menuOpen, s.menuMode = true, modeMain
	n := len(dialItems(s))
	s.menuRot = restFor(0, n)
	var p image.Point
	found := false
	for a := 0.0; a < 2*math.Pi && !found; a += 0.05 {
		p = image.Pt(center+int(105*math.Sin(a)), center-int(105*math.Cos(a)))
		item, middle := dialHitAt(p.X, p.Y, s.menuRot, n)
		found = item < 0 && !middle && p.In(shown[0].r)
	}
	if !found {
		t.Fatal("no point on the tile between the menu's middle and its ring")
	}
	plain, pressed := spotRenderer(), spotRenderer()
	plain.draw(s)
	s.press = pressView{on: true, at: p}
	pressed.draw(s)
	if got, differs := spotDiffBox(pressed, plain); differs {
		t.Errorf("a finger at %v under the menu shows over %v", p, got)
	}
}

// spotPressDisplay is a round display with its panel lit and a face drawn, as a finger finds it.
func spotPressDisplay(s roundScene) *Display {
	d := &Display{r: spotRenderer(), poke: make(chan struct{}, 1), on: true}
	d.pressScene(&s, s.now)
	d.r.draw(s)
	return d
}

// A finger on the round panel is a press from the next frame and for a moment after it lifts; one the
// panel says is held, swiped or dragged is over at once, but on the dashboard a finger held still is
// still the tile's tap until it moves. A press made on one face is not shown on the next.
func TestASpotPressSetsAndClears(t *testing.T) {
	now := time.Now()
	menu := roundScene{now: now, phase: "idle", menuOpen: true, menuMode: modeMain}
	d := spotPressDisplay(menu)
	d.fingerLanded(touch.Point{X: center, Y: center})
	s := menu
	if d.pressScene(&s, now); !s.press.on {
		t.Fatal("no press after a finger landed")
	}
	d.pressHeld(false)
	if d.press.set {
		t.Error("a finger held on the ring menu, which turns it, is still a press")
	}

	d.fingerLanded(touch.Point{X: center, Y: center})
	d.fingerDown(false)
	s = menu
	if d.pressScene(&s, time.Now()); !s.press.on {
		t.Error("the press went the moment the finger lifted")
	}
	s = menu
	if d.pressScene(&s, time.Now().Add(pressLinger+time.Millisecond)); s.press.on {
		t.Error("the press is still shown after its moment")
	}

	d.fingerLanded(touch.Point{X: center, Y: center})
	other := roundScene{now: now, phase: "idle", sheetOpen: true}
	if d.pressScene(&other, now); other.press.on || d.press.set {
		t.Error("the settings came up with the menu's press shown")
	}

	dash := roundScene{now: now, phase: "idle", showDash: true}
	d = spotPressDisplay(dash)
	d.dashShowing = true
	d.fingerLanded(touch.Point{X: center, Y: center})
	if d.pressHeld(false); !d.press.set {
		t.Error("a finger held still on the dashboard, a tap until it moves, lost its press")
	}
	if d.pressHeld(true); d.press.set {
		t.Error("a finger moving on the dashboard is still a press")
	}
	d.fingerLanded(touch.Point{X: center, Y: center})
	d.pressMoved(center+d.r.s(tapWander)+1, center)
	if d.press.set {
		t.Error("a finger dragged further than a tap wanders is still a press")
	}
}

// A frame that showed the press on nothing lets it go, so the finger's lift asks for no frame; one that
// showed it keeps it; and a press landed since that frame is never the one let go.
func TestASpotPressOnNothingIsLetGoAfterItsFrame(t *testing.T) {
	d := &Display{on: true}
	d.fingerLanded(touch.Point{X: 240, Y: 240})
	first := d.pressNow(pageID{}, time.Now())
	if !first.on {
		t.Fatal("no press after a finger landed")
	}
	d.pressUnshown(first, true)
	if !d.press.set {
		t.Fatal("a press a control took was let go")
	}
	d.pressUnshown(first, false)
	if d.press.set {
		t.Fatal("a press nothing took is still kept")
	}
	d.fingerLanded(touch.Point{X: 240, Y: 240})
	d.pressUnshown(first, false) // the frame of the earlier press, drawn late
	if !d.press.set {
		t.Error("a later press was let go for an earlier one's frame")
	}
}
