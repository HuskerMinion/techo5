//go:build !dot && !spot

package display

import (
	"image"
	"sync"
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
	d = &Display{poke: make(chan struct{}, 1), videoOnScreen: true}
	if !d.overDashboard(&scene{}) {
		t.Error("the video page is up, but nothing was over the dashboard")
	}
}

// A finger that comes down on a sheet's slider moves it as it goes - the whites' mark along the band,
// the volume's fill along the bar - also once it has wandered off the slider above or below, and
// the sheet stays up when it lifts.
func TestASheetsSliderFollowsTheFinger(t *testing.T) {
	prevSettle := sliderSettle
	sliderSettle = func(time.Duration, func()) *time.Timer { return nil } // no real timer to outlive the test
	t.Cleanup(func() { sliderSettle = prevSettle })
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	light := dashboard.LightColor{Entity: "light.desk", Name: "Desk lamp", Kelvin: true, MinK: 2700, MaxK: 6500, NowK: 2700}
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls(), dashColor: &light})
	var band image.Rectangle
	r.zmu.Lock()
	for _, z := range r.colorZones {
		if z.kind == colorPartWhite {
			band = z.r
		}
	}
	r.zmu.Unlock()
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	d.dashColor = &light
	mid := (band.Min.Y + band.Max.Y) / 2
	d.drawnHold(band.Min.X+2, mid)
	d.drawnMove(band.Min.X+band.Dx()/2, mid)
	if k := d.dashColor.NowK; k <= 2700 || k >= 6500 {
		t.Errorf("halfway along the band the mark is at %v kelvin", k)
	}
	d.drawnMove(band.Max.X+40, band.Max.Y+60) // past the end, and off the band below
	if k := d.dashColor.NowK; k != 6500 {
		t.Errorf("past the band's end the mark is at %v kelvin, want 6500", k)
	}
	d.drawnRelease(band.Max.X+40, band.Max.Y+60)
	if !d.colorOpen() || d.dashColor.NowK != 6500 {
		t.Errorf("lifted past the end: sheet up %v, mark at %v", d.colorOpen(), d.dashColor.NowK)
	}

	view := mediaView{sheet: mediaSheet{entity: "media_player.den", volume: -1},
		now: dashboard.MediaNow{Entity: "media_player.den", Name: "Den", State: "playing", Volume: 0.5}}
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls(), dashMedia: &view})
	var bar image.Rectangle
	r.zmu.Lock()
	for _, z := range r.mediaZones {
		if z.kind == mediaPartVolume {
			bar = z.r
		}
	}
	r.zmu.Unlock()
	if bar.Empty() {
		t.Fatal("the media sheet has no volume")
	}
	d = &Display{r: r, poke: make(chan struct{}, 1)}
	d.dashMedia = &view.sheet
	my := (bar.Min.Y + bar.Max.Y) / 2
	d.drawnHold(bar.Min.X+bar.Dx()/2, my)
	d.drawnMove(bar.Min.X+bar.Dx()/4, my-50)
	if v := d.dashMedia.volume; v < 0.2 || v > 0.3 {
		t.Errorf("a quarter along the bar the volume is %v", v)
	}
	d.drawnRelease(bar.Min.X-30, my)
	if d.dashMedia == nil || d.dashMedia.volume != 0 {
		t.Errorf("lifted left of the bar: the volume is %v, want 0", d.dashMedia)
	}
}

// While a finger moves a sheet's slider, the light follows it: the first step at once, the rest a
// few a second, the one it stops on shortly after, and where it lifts - each value once. The steps
// on the way fade over as long as they come apart; where it lifts fades as the light always does.
func TestASheetsSliderSendsAsTheFingerMoves(t *testing.T) {
	var mu sync.Mutex
	var sent []float64
	var fades []any
	prev := sliderTap
	sliderTap = func(a dashboard.Action) {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, float64(a.Data["color_temp_kelvin"].(int)))
		fades = append(fades, a.Data["transition"])
	}
	prevSettle := sliderSettle
	sliderSettle = func(time.Duration, func()) *time.Timer { return nil } // the lift sent again: not what this checks
	t.Cleanup(func() { sliderTap, sliderSettle = prev, prevSettle })
	got := func() []float64 {
		mu.Lock()
		defer mu.Unlock()
		return append([]float64(nil), sent...)
	}

	d := &Display{poke: make(chan struct{}, 1)}
	d.dashColor = &dashboard.LightColor{Entity: "light.desk", Kelvin: true, MinK: 2000, MaxK: 6500}
	whites := sheetSlider{kind: colorPartWhite}
	d.sendSlider(whites, 3000, false)
	d.sendSlider(whites, 3500, false) // held back: too soon after the first
	d.sendSlider(whites, 4000, false) // and this one replaces it
	if s := got(); len(s) != 1 || s[0] != 3000 {
		t.Fatalf("right away sent %v, want only the first step", s)
	}
	time.Sleep(sheetSendEvery + 150*time.Millisecond)
	if s := got(); len(s) != 2 || s[1] != 4000 {
		t.Fatalf("after a pause sent %v, want the value it stopped on as well", s)
	}
	d.sendSlider(whites, 4000, true) // lifted where it stopped: nothing new to send
	d.sendSlider(whites, 5000, true) // the next finger, lifted at once: sent
	if s := got(); len(s) != 3 || s[2] != 5000 {
		t.Errorf("sent %v, want 3000, 4000 and 5000", s)
	}
	mu.Lock()
	defer mu.Unlock()
	step := sheetSendEvery.Seconds()
	if len(fades) != 3 || fades[0] != step || fades[1] != step || fades[2] != nil {
		t.Errorf("transitions %v, want %v for the steps on the way and the light's own for the last", fades, step)
	}
}

// A held-back send whose timer fires only after the finger has lifted - too late to be stopped -
// sends nothing: the light keeps where the finger lifted, not the value waiting before it. And as a
// step went out just before the lift, where it lifted is sent once more, in case that step arrives last.
func TestALateHeldBackSendSendsNothing(t *testing.T) {
	var mu sync.Mutex
	var sent []int
	prevTap, prevAfter := sliderTap, sliderAfter
	sliderTap = func(a dashboard.Action) {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, a.Data["color_temp_kelvin"].(int))
	}
	var late func()
	sliderAfter = func(_ time.Duration, f func()) *time.Timer {
		late = f
		return time.NewTimer(time.Hour) // stopped at the lift, but the callback is fired by hand
	}
	var settle func()
	prevSettle := sliderSettle
	sliderSettle = func(_ time.Duration, f func()) *time.Timer {
		settle = f
		return nil
	}
	t.Cleanup(func() { sliderTap, sliderAfter, sliderSettle = prevTap, prevAfter, prevSettle })

	d := &Display{poke: make(chan struct{}, 1)}
	d.dashColor = &dashboard.LightColor{Entity: "light.desk", Kelvin: true, MinK: 2000, MaxK: 6500}
	whites := sheetSlider{kind: colorPartWhite}
	d.sendSlider(whites, 3000, false)
	d.sendSlider(whites, 3500, false) // held back: its timer is waiting
	d.sendSlider(whites, 4500, true)  // the finger lifts
	if late == nil {
		t.Fatal("the held-back send started no timer")
	}
	late() // and the timer fires anyway
	if settle == nil {
		t.Fatal("the lift was not sent again")
	}
	settle()
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 3 || sent[0] != 3000 || sent[1] != 4500 || sent[2] != 4500 {
		t.Errorf("sent %v, want 3000, then where the finger lifted, 4500, twice, and nothing else", sent)
	}
}

// Holds are reported for the dashboard only while the panel is lit, and for the night light always.
func TestHoldsAreForALitDashboard(t *testing.T) {
	for _, c := range []struct {
		dash, on, glow, want bool
	}{
		{true, true, false, true},
		{true, false, false, false},
		{false, false, true, true},
		{false, true, false, false},
	} {
		d := &Display{dashHolds: c.dash, on: c.on, nightGlow: c.glow}
		if got := d.holdsWanted(); got != c.want {
			t.Errorf("dashboard %v, lit %v, night light %v: holds %v, want %v", c.dash, c.on, c.glow, got, c.want)
		}
	}
}
