//go:build !dot && !spot

package display

import (
	"image"
	"reflect"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// fans is a view of four fans: one with every control, one that only has speed, one that only turns
// on and off; and one whose card sets a tap of its own.
func fans() dashboard.Drawn {
	toggle := func(e string) *dashboard.Action { return &dashboard.Action{Entity: e, Service: "fan.toggle"} }
	tile := func(entity, name string, tap *dashboard.Action, adj *dashboard.Adjust) dashboard.Tile {
		return dashboard.Tile{Entity: entity, Name: name, Icon: "fan", Value: "On", On: true, Tap: tap, Adjust: adj}
	}
	speed := func(e string) *dashboard.Adjust {
		return &dashboard.Adjust{Entity: e, Kind: "speed", Value: 50, Min: 0, Max: 100, Step: 25}
	}
	return dashboard.Drawn{Sections: []dashboard.Section{{Blocks: []dashboard.Block{
		{Heading: "Fans"},
		{Tiles: []dashboard.Tile{
			tile("fan.ceiling", "Ceiling", toggle("fan.ceiling"), speed("fan.ceiling")),
			tile("fan.desk", "Desk", toggle("fan.desk"), speed("fan.desk")),
			tile("fan.plain", "Plain", toggle("fan.plain"), nil),
			tile("fan.custom", "Custom", &dashboard.Action{Entity: "fan.custom", Service: "script.turn_on"}, nil),
		}},
	}}}}
}

var fanStates = map[string]dashboard.FanNow{
	"fan.ceiling": {Entity: "fan.ceiling", Name: "Ceiling", On: true, HasSpeed: true, Percentage: 50, Step: 25,
		HasDirection: true, Direction: "forward", Presets: []string{"auto", "sleep", "turbo"}, Preset: "auto"},
	"fan.desk":   {Entity: "fan.desk", Name: "Desk", On: true, HasSpeed: true, Percentage: 50, Step: 25},
	"fan.plain":  {Entity: "fan.plain", Name: "Plain", On: true},
	"fan.custom": {Entity: "fan.custom", Name: "Custom", On: true},
}

// fakeFan stands in for Home Assistant: what the sheet reads, and what it asks for.
func fakeFan(t *testing.T) *[]dashboard.Action {
	calls := &[]dashboard.Action{}
	prevNow, prevCall, prevTap := fanNow, fanCall, sliderTap
	fanNow = func(entity string) (dashboard.FanNow, bool) {
		n, ok := fanStates[entity]
		return n, ok
	}
	fanCall = func(a dashboard.Action) { *calls = append(*calls, a) }
	sliderTap = func(a dashboard.Action) { *calls = append(*calls, a) }
	t.Cleanup(func() { fanNow, fanCall, sliderTap = prevNow, prevCall, prevTap })
	return calls
}

// The sheet has a switch, and a speed, a direction and preset modes only where the fan has them; every
// part is on the sheet and the sheet on the panel.
func TestTheFanSheetOffersWhatTheFanCanDo(t *testing.T) {
	for _, c := range []struct {
		entity                    string
		speed, direction, presets int
		wide, high                int
	}{
		{"fan.ceiling", 1, 2, 3, showWide, showHigh},
		{"fan.desk", 1, 0, 0, showWide, showHigh},
		{"fan.plain", 0, 0, 0, showWide, showHigh},
		{"fan.ceiling", 1, 2, 3, 1280, 800},
	} {
		img := image.NewRGBA(image.Rect(0, 0, c.wide, c.high))
		r := newRenderer(img)
		r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fans(),
			dashFan: &fanView{now: fanStates[c.entity]}})
		r.zmu.Lock()
		zones, card := r.fanZones, r.fanCard
		r.zmu.Unlock()
		count := map[int]int{}
		for _, z := range zones {
			count[z.kind]++
			if !z.r.In(card) {
				t.Errorf("%s: part %d at %v outside the sheet %v", c.entity, z.kind, z.r, card)
			}
		}
		if !card.In(img.Bounds()) {
			t.Errorf("%s: the sheet %v is bigger than the panel", c.entity, card)
		}
		if count[fanPartSpeed] != c.speed || count[fanPartDirection] != c.direction || count[fanPartPreset] != c.presets ||
			count[fanPartPower] != 1 || count[fanPartDone] != 1 {
			t.Errorf("%s: parts %v, want %d speed, %d directions, %d presets, power, done", c.entity, count, c.speed, c.direction, c.presets)
		}
	}
}

// A long press on a fan whose card sets no tap of its own, or the plain toggle, opens its sheet; one
// whose card sets another tap does what it says. On the sheet the switch, the direction and a preset
// call their services, and Done puts it away.
func TestALongPressOnAFanOpensItsSheet(t *testing.T) {
	calls := fakeFan(t)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fans()})
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	tiles, _ := r.dash()
	byEntity := map[string]*dashTile{}
	for i := range tiles {
		byEntity[tiles[i].entity] = &tiles[i]
	}

	if byEntity["fan.custom"] == nil || fanOwnTap(byEntity["fan.custom"]) {
		t.Fatal("a card with a tap of its own counts as the plain toggle")
	}
	d.longPress(byEntity["fan.custom"]) // runs its own tap: no Home Assistant, so nothing
	if d.colorOpen() {
		t.Error("a card whose tap is its own opened the sheet")
	}
	d.longPress(byEntity["fan.ceiling"])
	if !d.colorOpen() {
		t.Fatal("a long press on a fan did not open its sheet")
	}

	tapPart := func(kind, index int) {
		t.Helper()
		r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fans(), dashFan: d.fanViewNow()})
		r.zmu.Lock()
		zones := r.fanZones
		r.zmu.Unlock()
		for _, z := range zones {
			if z.kind == kind && z.index == index {
				d.drawnTap((z.r.Min.X+z.r.Max.X)/2, (z.r.Min.Y+z.r.Max.Y)/2)
				return
			}
		}
		t.Fatalf("no part %d/%d", kind, index)
	}
	tapPart(fanPartPower, 0)
	tapPart(fanPartPower, 0) // off, then on again, from what it showed after the first
	tapPart(fanPartDirection, 1)
	tapPart(fanPartPreset, 2)
	tapPart(fanPartDone, 0)
	if d.colorOpen() {
		t.Error("Done left the sheet up")
	}
	want := []dashboard.Action{
		{Entity: "fan.ceiling", Service: "fan.turn_off"},
		{Entity: "fan.ceiling", Service: "fan.turn_on"},
		{Entity: "fan.ceiling", Service: "fan.set_direction", Data: map[string]any{"direction": "reverse"}},
		{Entity: "fan.ceiling", Service: "fan.set_preset_mode", Data: map[string]any{"preset_mode": "turbo"}},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Errorf("calls %+v, want %+v", *calls, want)
	}
}

// A finger on the speed slider sets the speed on the fan's steps: 80 percent along a bar on steps of a
// quarter asks for 75, and the sheet shows it at once.
func TestTheFanSheetsSliderSnapsToTheFansSteps(t *testing.T) {
	calls := fakeFan(t)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	if !d.openFan("fan.ceiling") {
		t.Fatal("no sheet")
	}
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fans(), dashFan: d.fanViewNow()})
	var bar image.Rectangle
	r.zmu.Lock()
	for _, z := range r.fanZones {
		if z.kind == fanPartSpeed {
			bar = z.r
		}
	}
	r.zmu.Unlock()
	if bar.Empty() {
		t.Fatal("no speed slider")
	}
	x := bar.Min.X + (bar.Dx()-1)*80/100
	y := (bar.Min.Y + bar.Max.Y) / 2
	s, ok := d.sliderAt(x, y)
	if !ok || s.kind != fanPartSpeed || s.step != 25 {
		t.Fatalf("slider %+v %v", s, ok)
	}
	d.slideSheet(s, x, true)
	if len(*calls) != 1 || (*calls)[0].Service != "fan.set_percentage" || (*calls)[0].Data["percentage"] != 75 {
		t.Errorf("calls %+v, want fan.set_percentage 75", *calls)
	}
	if v := d.fanViewNow(); v == nil || v.now.Percentage != 75 {
		t.Errorf("the sheet shows %+v", v)
	}
}
