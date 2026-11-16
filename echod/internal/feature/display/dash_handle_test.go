//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
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

// At night the handle is drawn in the night's red, not its day light, so it is not the brightest thing
// on a dark screen; with the setting off it is not drawn at all (#564).
func TestTheHandleFollowsTheNightAndTheSetting(t *testing.T) {
	for _, panel := range showPanels() {
		draw := func(s scene) (image.Point, color.RGBA, *renderer) {
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			r := newRenderer(img)
			s.now, s.phase, s.showDash, s.dashMode, s.drawn = time.Now(), "idle", true, config.DashboardDrawn, fourControls()
			r.draw(s)
			at := image.Pt(r.s(dashHandleIn)+r.s(dashHandleWide)/2, panel.high/2)
			return at, img.RGBAAt(at.X, at.Y), r
		}
		_, day, _ := draw(scene{})
		_, night, _ := draw(scene{nightDim: true})
		if night.R >= day.R || night.G > night.R || night.B != 0 {
			t.Errorf("%s: the handle at night is %v, day %v: want the night's dim red", panel.name, night, day)
		}
		if _, hidden, _ := draw(scene{dashHide: true}); hidden.R >= 0xa0 {
			t.Errorf("%s: the handle is drawn with the setting off (%v)", panel.name, hidden)
		}
	}
}

// With SHOW_PREVIEW set, the drawn dashboard's edge by day, at night and with the handle off is written
// there (dashboard-handle-show8.png, dashboard-handle-night-show8.png, dashboard-handle-hidden-show8.png).
func TestTheDashboardHandlePreviews(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	if dir == "" {
		t.Skip("SHOW_PREVIEW is not set")
	}
	for name, s := range map[string]scene{
		"dashboard-handle-show8":        {},
		"dashboard-handle-night-show8":  {nightDim: true},
		"dashboard-handle-hidden-show8": {dashHide: true},
	} {
		img := image.NewRGBA(image.Rect(0, 0, show8Wide, show8High))
		s.now, s.phase, s.showDash, s.dashMode, s.drawn = time.Date(2026, 10, 17, 23, 40, 0, 0, time.Local), "idle", true, config.DashboardDrawn, fourControls()
		newRenderer(img).draw(s)
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}

// With the handle hidden, a tap on its strip is the page's, not a way out.
func TestATapWhereTheHandleWasIsThePages(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r}
	if !d.handleTapped(r.s(8), showHigh/2, true) {
		t.Fatal("a tap on the shown handle is not the handle's")
	}
	if err := config.Set().Dashboard().HideHandle(true); err != nil {
		t.Skip("cannot change the config here:", err)
	}
	defer config.Set().Dashboard().HideHandle(false)
	if d.handleTapped(r.s(8), showHigh/2, true) || d.handleTapped(r.s(8), showHigh/2, false) {
		t.Error("a tap where the handle was is still the handle's with it hidden")
	}
}
