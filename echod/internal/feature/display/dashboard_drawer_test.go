//go:build !dot && !spot

package display

import (
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
)

// The drawer slides in over the dashboard when that is the page, as it does over the clock: what shows to
// the drawer's left is the dashboard, not the clock behind it.
func TestTheDrawerIsOverTheDashboard(t *testing.T) {
	previewFont(t)
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	draw := func(s scene) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, show8Wide, show8High))
		newRenderer(img).draw(s)
		return img
	}
	dash := scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls()}
	over := dash
	over.showDrawer, over.drawerTab, over.demo = true, drawerCall, true
	over.callees = []phone.Callee{{Name: "a", Device: true}, {Name: "b", Device: true}}
	clock := scene{now: at, phase: "idle", showDrawer: true, drawerTab: drawerCall, demo: true, callees: over.callees}

	behind, with, bare := draw(dash), draw(over), draw(clock)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, show8Wide, show8High)))
	// The drawer dims what is under it, and its panel and shadow start a little way in from the right.
	newRenderer(behind).dimAll(0.55)
	edge := r.w - r.cardIn() - r.drawerW() - r.s(60)
	same := func(a, b *image.RGBA) bool {
		for y := 0; y < show8High; y += 7 {
			for x := 0; x < edge; x += 7 {
				if a.RGBAAt(x, y) != b.RGBAAt(x, y) {
					return false
				}
			}
		}
		return true
	}
	if !same(behind, with) {
		t.Error("the drawer changed what shows to its left: the dashboard is not behind it, dimmed")
	}
	if same(bare, with) {
		t.Error("the page behind the drawer is the clock's, not the dashboard's")
	}
	if with.RGBAAt(r.w-r.cardIn()-r.drawerW()/2, show8High/2) == behind.RGBAAt(r.w-r.cardIn()-r.drawerW()/2, show8High/2) {
		t.Error("the drawer is not drawn over the dashboard")
	}
}
