//go:build !dot && !spot

package display

import (
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// With the color sheet up, a finger that comes down on the page beside it and is dragged to lift on
// one of its parts chooses nothing there; one that comes down on the part and lifts on it does.
func TestADragFromThePageChoosesNothingOnTheSheet(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	light := dashboard.LightColor{Entity: "light.desk", Name: "Desk lamp", Kelvin: true, MinK: 2700, MaxK: 6500}
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls(), dashColor: &light})
	r.zmu.Lock()
	var done image.Rectangle
	for _, z := range r.colorZones {
		if z.kind == colorPartDone {
			done = z.r
		}
	}
	card := r.colorCard
	r.zmu.Unlock()
	if done.Empty() {
		t.Fatal("the sheet has no Done")
	}
	mid := image.Pt((done.Min.X+done.Max.X)/2, (done.Min.Y+done.Max.Y)/2)
	off := image.Pt(card.Min.X/2, card.Min.Y+card.Dy()/2)
	if off.In(card) {
		t.Fatalf("%v is on the sheet %v", off, card)
	}

	d := &Display{r: r, poke: make(chan struct{}, 1)}
	d.dashColor = &light
	d.drawnHold(off.X, off.Y)
	d.drawnMove(mid.X, mid.Y)
	d.drawnRelease(mid.X, mid.Y)
	if !d.colorOpen() {
		t.Error("a drag from the page that lifted on Done put the sheet away")
	}

	d.drawnHold(mid.X, mid.Y)
	d.drawnRelease(mid.X, mid.Y)
	if d.colorOpen() {
		t.Error("a press on Done left the sheet up")
	}
}

// Something drawn over the dashboard that takes a tap alone - the PIN pad, an event's pop-up - keeps
// the dashboard from asking for holds; with none of them up, nothing is over it.
func TestTheDashboardKnowsWhatIsDrawnOverIt(t *testing.T) {
	d := &Display{poke: make(chan struct{}, 1)}
	if d.overDashboard(&scene{}) {
		t.Error("nothing is up, but something was over the dashboard")
	}
	if !d.overDashboard(&scene{pin: pinView{open: true}}) {
		t.Error("the PIN pad is up, but nothing was over the dashboard")
	}
	d.popup = &hass.Event{Summary: "Dentist"}
	if !d.overDashboard(&scene{}) {
		t.Error("an event's pop-up is up, but nothing was over the dashboard")
	}
}
