//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mdi"
)

// thermostats is a view of three thermostats: one holding to a temperature, one keeping a range, one
// off; and one whose card sets a tap of its own (none).
func thermostats() dashboard.Drawn {
	tile := func(entity, name, value string, on bool, adj *dashboard.Adjust, tap *dashboard.Action) dashboard.Tile {
		return dashboard.Tile{Entity: entity, Name: name, Icon: "thermostat", Value: value, On: on, Adjust: adj, Tap: tap}
	}
	return dashboard.Drawn{Sections: []dashboard.Section{{Blocks: []dashboard.Block{
		{Heading: "Climate"},
		{Tiles: []dashboard.Tile{
			tile("climate.hall", "Hall", "68° · Heat to 70°", true,
				&dashboard.Adjust{Entity: "climate.hall", Kind: "temperature", Value: 70, Min: 45, Max: 95, Step: 1}, nil),
			tile("climate.den", "Den", "Heat cool", true, nil, nil),
			tile("climate.attic", "Attic", "Off", false, nil, nil),
			tile("climate.garage", "Garage", "Off", false, nil, &dashboard.Action{}),
		}},
	}}}}
}

var climateStates = map[string]dashboard.ClimateNow{
	"climate.hall": {Entity: "climate.hall", Name: "Hall", State: "heat", Current: 68, HasCurrent: true, Target: 70, HasTarget: true,
		Min: 45, Max: 95, Step: 1, Modes: []string{"off", "heat", "cool", "heat_cool", "auto"}},
	"climate.den": {Entity: "climate.den", Name: "Den", State: "heat_cool", Current: 21.5, HasCurrent: true, Low: 20, High: 24, HasRange: true,
		Min: 7, Max: 35, Step: 0.5, Modes: []string{"off", "heat", "cool", "heat_cool"}},
	"climate.attic":  {Entity: "climate.attic", Name: "Attic", State: "off", Min: 45, Max: 95, Step: 1, Modes: []string{"off", "heat"}},
	"climate.garage": {Entity: "climate.garage", Name: "Garage", State: "off", Min: 45, Max: 95, Step: 1, Modes: []string{"off", "heat"}},
}

// fakeClimate stands in for Home Assistant: what the sheet reads, and what it asks for.
func fakeClimate(t *testing.T) (set *[][3]any, modes *[][2]string) {
	set, modes = &[][3]any{}, &[][2]string{}
	prevNow, prevSet, prevMode := climateNow, climateSet, climateMode
	climateNow = func(entity string) (dashboard.ClimateNow, bool) {
		c, ok := climateStates[entity]
		return c, ok
	}
	climateSet = func(entity string, low, high float64) { *set = append(*set, [3]any{entity, low, high}) }
	climateMode = func(entity, mode string) { *modes = append(*modes, [2]string{entity, mode}) }
	t.Cleanup(func() { climateNow, climateSet, climateMode = prevNow, prevSet, prevMode })
	return set, modes
}

// The sheet has a step down and up for the one temperature, two of each for a range, none for a
// thermostat that is off, and a mode for each it has, every part on the sheet and the sheet on the
// panel. With SHOW_PREVIEW set, each is written there.
func TestTheThermostatSheetOffersWhatTheThermostatCanDo(t *testing.T) {
	for _, icon := range []string{"minus", "plus", "thermostat"} {
		if _, ok := mdi.Rune(icon); !ok {
			t.Errorf("no icon %q", icon)
		}
	}
	dir := os.Getenv("SHOW_PREVIEW")
	at := time.Date(2026, 10, 17, 7, 30, 0, 0, time.Local)
	for _, c := range []struct {
		entity     string
		steps      int
		modes      int
		wide, high int
	}{
		{"climate.hall", 2, 5, showWide, showHigh},
		{"climate.den", 4, 4, showWide, showHigh},
		{"climate.attic", 0, 2, showWide, showHigh},
		{"climate.den", 4, 4, 1280, 800},
		{"climate.den", 4, 4, skylightWide, skylightHigh},
	} {
		img := image.NewRGBA(image.Rect(0, 0, c.wide, c.high))
		r := newRenderer(img)
		r.draw(scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: thermostats(),
			dashClimate: &climateView{now: climateStates[c.entity]}})
		r.zmu.Lock()
		zones, card := r.climateZones, r.climateCard
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
		steps := count[climatePartDown] + count[climatePartUp] + count[climatePartHighDown] + count[climatePartHighUp]
		if steps != c.steps || count[climatePartMode] != c.modes || count[climatePartDone] != 1 {
			t.Errorf("%s: parts %v, want %d steps, %d modes, done", c.entity, count, c.steps, c.modes)
		}
		if dir != "" {
			f, err := os.Create(filepath.Join(dir, "climate-sheet-"+c.entity[len("climate."):]+"-"+strconv.Itoa(c.wide)+".png"))
			if err != nil {
				t.Fatal(err)
			}
			png.Encode(f, img)
			f.Close()
		}
	}
}

// A tap on a thermostat whose card sets no tap opens its sheet, off or keeping a range as much as
// holding to one temperature; one whose card says none does nothing. On the sheet a step up sets the
// next temperature, a step down a range's low, and a mode sets the mode; Done puts it away.
func TestATapOnAThermostatOpensItsSheet(t *testing.T) {
	set, modes := fakeClimate(t)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: thermostats()})
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	tiles, _ := r.dash()
	at := map[string]image.Point{}
	for _, tl := range tiles {
		at[tl.entity] = image.Pt((tl.r.Min.X+tl.r.Max.X)/2, (tl.r.Min.Y+tl.r.Max.Y)/2)
	}
	for _, e := range []string{"climate.hall", "climate.den", "climate.attic", "climate.garage"} {
		if _, ok := at[e]; !ok {
			t.Fatalf("%s can't be pressed", e)
		}
	}

	d.drawnTap(at["climate.garage"].X, at["climate.garage"].Y)
	if d.colorOpen() {
		t.Error("a card whose tap is none opened the sheet")
	}

	tapPart := func(entity string, kind, index int) {
		t.Helper()
		r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: thermostats(),
			dashClimate: d.climateViewNow()})
		r.zmu.Lock()
		zones := r.climateZones
		r.zmu.Unlock()
		for _, z := range zones {
			if z.kind == kind && z.index == index {
				d.drawnTap((z.r.Min.X+z.r.Max.X)/2, (z.r.Min.Y+z.r.Max.Y)/2)
				return
			}
		}
		t.Fatalf("%s: no part %d/%d", entity, kind, index)
	}

	for _, e := range []string{"climate.hall", "climate.den", "climate.attic"} {
		d.drawnTap(at[e].X, at[e].Y)
		d.mu.Lock()
		c := d.dashClimate
		d.mu.Unlock()
		if c == nil || c.entity != e {
			t.Fatalf("a tap on %s opened %+v", e, c)
		}
		switch e {
		case "climate.hall":
			tapPart(e, climatePartUp, 0)
			tapPart(e, climatePartUp, 0) // from what it showed after the first
		case "climate.den":
			tapPart(e, climatePartDown, 0)
		case "climate.attic":
			tapPart(e, climatePartMode, 1)
		}
		tapPart(e, climatePartDone, 0)
		if d.colorOpen() {
			t.Errorf("%s: Done left the sheet up", e)
		}
	}
	want := [][3]any{{"climate.hall", 71.0, 0.0}, {"climate.hall", 72.0, 0.0}, {"climate.den", 19.5, 24.0}}
	if len(*set) != len(want) {
		t.Fatalf("set %v, want %v", *set, want)
	}
	for i := range want {
		if (*set)[i] != want[i] {
			t.Errorf("set %v, want %v", *set, want)
		}
	}
	if len(*modes) != 1 || (*modes)[0] != [2]string{"climate.attic", "heat"} {
		t.Errorf("modes %v", *modes)
	}

	// A long press on one does the same.
	d.longPress(&dashTile{entity: "climate.attic"})
	if !d.colorOpen() {
		t.Error("a long press on a thermostat did not open its sheet")
	}
}
