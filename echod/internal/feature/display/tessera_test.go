//go:build !dot && !spot

package display

import (
	"encoding/json"
	"image"
	"math"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/feature/tessera"
)

// tessLayout is a layout of three pages on a 4 by 2 grid: a light and a sensor on the home page, a
// switch on the second, and a detail page.
func tessLayout() *tessera.Layout {
	slot := func(n int) *int { return &n }
	return &tessera.Layout{
		Rev:   "0123456789abcdef",
		Begin: tessera.Begin{Pages: 3, Tiles: 3, Home: 0},
		Pages: []tessera.Page{
			{P: 0, Title: "Home", Items: json.RawMessage(`[]`)},
			{P: 1, Title: "More", Items: json.RawMessage(`[]`)},
			{P: 2, Title: "Detail", Excluded: true, Items: json.RawMessage(`[]`)},
		},
		Tiles: []tessera.Tile{
			{I: 0, Slot: slot(0), Entity: "light.kitchen", Name: "Kitchen", State: "on", A: json.RawMessage(`{"brightness":255}`), O: json.RawMessage(`{}`)},
			{I: 1, Slot: slot(1), Entity: "sensor.outside", Name: "Outside", State: "12", A: json.RawMessage(`{}`), O: json.RawMessage(`{"size":"wide"}`)},
			{I: 2, Slot: slot(8), Entity: "switch.kettle", Name: "Kettle", State: "off", A: json.RawMessage(`{}`), O: json.RawMessage(`{}`)},
		},
	}
}

// Where a tap lands on the Tessera page: a tile a tap or a slide does something to, a page's dot, the
// way back from a detail page. A sensor is nothing to tap.
func TestTesseraZones(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	v := tessera.Place(tessLayout(), 4, 2)
	for _, size := range []image.Point{{showWide, showHigh}, {show8Wide, show8High}, {skylightWide, skylightHigh}} {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, size.X, size.Y)))
		r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardTessera, tessera: v, tessOn: true})
		zones, _ := r.dash()
		var tiles, dots []int
		for _, z := range zones {
			if z.tess > 0 {
				tiles = append(tiles, z.tess-1)
				if z.tess == 1 && (z.adjust == nil || z.adjust.Entity != "light.kitchen") {
					t.Errorf("%v: the light's tile has no level to slide", size)
				}
				if !z.r.In(image.Rect(0, 0, size.X, size.Y)) || z.r.Min.Y < r.s(tessBarH) {
					t.Errorf("%v: tile %d at %v, off the grid", size, z.tess-1, z.r)
				}
			}
			if z.page > 0 {
				dots = append(dots, z.page-1)
			}
		}
		if len(tiles) != 1 || tiles[0] != 0 {
			t.Errorf("%v: tiles to tap %v, want only the light", size, tiles)
		}
		if len(dots) != 2 || dots[0] != 0 || dots[1] != 1 {
			t.Errorf("%v: dots for pages %v, want 0 and 1 (not the detail page)", size, dots)
		}

		r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardTessera, tessera: v, tessOn: true, tessPage: 2})
		zones, _ = r.dash()
		back := false
		for _, z := range zones {
			back = back || z.page == v.Home+1 && z.r.Min.X == 0
		}
		if !back {
			t.Errorf("%v: no way back from the detail page", size)
		}
	}
}

func TestTurnPage(t *testing.T) {
	v := tessera.Place(tessLayout(), 4, 2)
	for _, c := range []struct {
		page, step, want int
		ok               bool
	}{
		{0, 1, 1, true},
		{1, -1, 0, true},
		{1, 1, 0, false}, // the detail page is not swiped to
		{0, -1, 0, false},
		{2, 1, 1, true}, // from the detail page as from home
	} {
		if got, ok := turnPage(v, c.page, c.step); got != c.want || ok != c.ok {
			t.Errorf("turnPage(%d, %d) = %d, %v; want %d, %v", c.page, c.step, got, ok, c.want, c.ok)
		}
	}
}

// sheetLayout is a page of a light that takes whites, a thermostat and a locked lock.
func sheetLayout() *tessera.Layout {
	slot := func(n int) *int { return &n }
	return &tessera.Layout{
		Rev:   "0123456789abcdef",
		Begin: tessera.Begin{Pages: 1, Tiles: 3},
		Pages: []tessera.Page{{P: 0, Title: "Home", Items: json.RawMessage(`[]`)}},
		Tiles: []tessera.Tile{
			{I: 0, Slot: slot(0), Entity: "light.desk", Name: "Desk", State: "on", O: json.RawMessage(`{}`),
				A: json.RawMessage(`{"brightness":128,"supported_color_modes":["color_temp"],"color_temp_kelvin":3000,"min_color_temp_kelvin":2200,"max_color_temp_kelvin":6500}`)},
			{I: 1, Slot: slot(1), Entity: "climate.hall", Name: "Hall", State: "heat", O: json.RawMessage(`{}`),
				A: json.RawMessage(`{"temperature":21,"current_temperature":20,"min_temp":7,"max_temp":30,"hvac_modes":["off","heat"]}`)},
			{I: 2, Slot: slot(2), Entity: "lock.front", Name: "Front door", State: "locked", O: json.RawMessage(`{}`), A: json.RawMessage(`{}`)},
		},
	}
}

// tessCalls gathers what the device asks Home Assistant to do for the rest of a test.
func tessCalls(t *testing.T) func() []component.Call {
	var mu sync.Mutex
	var got []component.Call
	stop := component.CallService.Listen(func(c component.Call) {
		mu.Lock()
		got = append(got, c)
		mu.Unlock()
	})
	t.Cleanup(stop)
	return func() []component.Call {
		mu.Lock()
		defer mu.Unlock()
		return append([]component.Call(nil), got...)
	}
}

// On the Tessera page a long press on a light opens its colors, read from the tile, and a white chosen
// there goes as the device's own action; a tap on a thermostat opens its controls; a tap on a locked
// lock asks first, and the second unlocks it.
func TestTesseraSheetsAndTheLock(t *testing.T) {
	if !slices.Contains(dashboard.Modes(), config.DashboardTessera) {
		t.Skip("no Tessera on this device: the dashboard is Off in that mode")
	}
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Dashboard().Mode(config.DashboardTessera); err != nil {
		t.Fatal(err)
	}
	tessera.Preview(sheetLayout(), 4, 2)
	t.Cleanup(func() { tessera.Preview(nil, 0, 0) })
	got := tessCalls(t)

	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	draw := func(d *Display) {
		v, on := tessera.Current()
		s := scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardTessera, tessera: v, tessOn: on, dashColor: d.dashColor}
		d.tessSheetViews(&s)
		r.draw(s)
	}
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	draw(d)
	at := map[string]image.Point{}
	zones, _ := r.dash()
	for _, z := range zones {
		at[z.entity] = image.Pt((z.r.Min.X+z.r.Max.X)/2, (z.r.Min.Y+z.r.Max.Y)/2)
	}

	p := at["light.desk"]
	d.tessHold(p.X, p.Y)
	d.tessRelease(p.X, p.Y)
	d.mu.Lock()
	c := d.dashColor
	d.mu.Unlock()
	if c == nil || c.Entity != "light.desk" || !c.Kelvin || c.MinK != 2200 || c.NowK != 3000 || c.Name != "Desk" {
		t.Fatalf("the long press opened %+v", c)
	}
	draw(d)
	r.zmu.Lock()
	var white image.Rectangle
	for _, z := range r.colorZones {
		if z.kind == colorPartWhite {
			white = z.r
		}
	}
	r.zmu.Unlock()
	if white.Empty() {
		t.Fatal("the sheet has no whites")
	}
	d.tessTap(white.Min.X+2, (white.Min.Y+white.Max.Y)/2)
	if calls := got(); len(calls) != 1 || calls[0].Service != "light.turn_on" || calls[0].Data["entity_id"] != "light.desk" || calls[0].Templates["color_temp_kelvin"] == "" {
		t.Fatalf("a white on the sheet called %+v", calls)
	}
	d.tessTap(1, 1) // beside the sheet: it goes
	if d.colorOpen() {
		t.Fatal("a tap beside the sheet left it up")
	}

	draw(d)
	p = at["climate.hall"]
	d.tessTap(p.X, p.Y)
	if v := d.climateViewNow(); v == nil || v.now.Target != 21 || len(v.now.Modes) != 2 {
		t.Fatalf("a tap on the thermostat opened %+v", v)
	}
	d.tessTap(1, 1)

	p = at["lock.front"]
	d.tessTap(p.X, p.Y)
	if calls := got(); len(calls) != 1 {
		t.Fatalf("one tap on a locked lock called %+v", calls[1:])
	}
	d.tessTap(p.X, p.Y)
	if calls := got(); len(calls) != 2 || calls[1].Service != "lock.unlock" {
		t.Fatalf("the second tap called %+v", calls[1:])
	}
}

// waitCalls is what the device asked within a second, once there are n: a player's keys are sent off
// the finger's goroutine.
func waitCalls(t *testing.T, got func() []component.Call, n int) []component.Call {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for len(got()) < n && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	return got()
}

// A big tile's controls are drawn where the add-on says the card has them, and do what they say: the
// thermostat's + steps it, a mode chooses it, and a player's keys go where the drawn dashboard's go,
// on the tile and on its sheet.
func TestTesseraControls(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Dashboard().Mode(config.DashboardTessera); err != nil {
		t.Fatal(err)
	}
	slot := func(n int) *int { return &n }
	l := &tessera.Layout{
		Rev:   "0123456789abcdef",
		Begin: tessera.Begin{Pages: 1, Tiles: 2},
		Pages: []tessera.Page{{P: 0, Title: "Home", Items: json.RawMessage(`[]`)}},
		Tiles: []tessera.Tile{
			{I: 0, Slot: slot(0), Entity: "climate.hall", Name: "Hall", State: "heat", O: json.RawMessage(`{"size":"square","controls":"setpoint_mode"}`),
				A: json.RawMessage(`{"temperature":21,"min_temp":7,"max_temp":30,"target_temp_step":0.5,"hvac_modes":["off","heat"]}`)},
			{I: 1, Slot: slot(2), Entity: "media_player.den", Name: "Den", State: "playing", O: json.RawMessage(`{"size":"wide","controls":"playback"}`), A: json.RawMessage(`{}`)},
		},
	}
	tessera.Preview(l, 4, 2)
	t.Cleanup(func() { tessera.Preview(nil, 0, 0) })
	prev := tessRoute
	tessRoute = func(entity string) string { return "media_player.ma_" + entity[len("media_player."):] }
	t.Cleanup(func() { tessRoute = prev })
	got := tessCalls(t)

	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	v, _ := tessera.Current()
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardTessera, tessera: v, tessOn: true})
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	at := map[string]image.Point{}
	zones, _ := r.dash()
	for _, z := range zones {
		if z.ctl != "" {
			at[z.ctl] = image.Pt((z.r.Min.X+z.r.Max.X)/2, (z.r.Min.Y+z.r.Max.Y)/2)
		}
	}
	for _, ctl := range []string{"down", "up", "mode:off", "mode:heat", "prev", "play", "next"} {
		if _, ok := at[ctl]; !ok {
			t.Fatalf("no %s drawn; controls %v", ctl, at)
		}
	}
	d.tessTap(at["up"].X, at["up"].Y)
	d.tessTap(at["mode:off"].X, at["mode:off"].Y)
	d.tessTap(at["play"].X, at["play"].Y)
	c := waitCalls(t, got, 3)
	if len(c) != 3 || c[0].Service != "climate.set_temperature" || c[0].Templates["temperature"] != "{{ 21.5 }}" ||
		c[1].Data["hvac_mode"] != "off" || c[2].Service != "media_player.media_play_pause" || c[2].Data["entity_id"] != "media_player.ma_den" {
		t.Fatalf("calls %+v", c)
	}

	tessSheets{}.Tap(dashboard.Action{Entity: "media_player.den", Service: "media_player.media_next_track"})
	tessSheets{}.Tap(dashboard.Action{Entity: "media_player.den", Service: "media_player.volume_set", Data: map[string]any{"volume_level": 0.3}})
	c = waitCalls(t, got, 5)
	if len(c) != 5 {
		t.Fatalf("calls %+v", c)
	}
	for _, x := range c[3:] {
		switch x.Service {
		case "media_player.media_next_track":
			if x.Data["entity_id"] != "media_player.ma_den" {
				t.Errorf("the sheet's next went to %s", x.Data["entity_id"])
			}
		case "media_player.volume_set":
			if x.Data["entity_id"] != "media_player.den" {
				t.Errorf("the sheet's volume went to %s", x.Data["entity_id"])
			}
		default:
			t.Errorf("call %+v", x)
		}
	}
}

// A tap acts on the entity whose tile the finger found: a new layout drawn under a finger before it
// lifts leaves the tile now in that place alone. A plain tap on a player plays or pauses it where its
// keys do.
func TestTesseraTapsFollowTheEntity(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Dashboard().Mode(config.DashboardTessera); err != nil {
		t.Fatal(err)
	}
	slot := func(n int) *int { return &n }
	l := &tessera.Layout{
		Rev:   "0123456789abcdef",
		Begin: tessera.Begin{Pages: 1, Tiles: 2},
		Pages: []tessera.Page{{P: 0, Title: "Home", Items: json.RawMessage(`[]`)}},
		Tiles: []tessera.Tile{
			{I: 0, Slot: slot(0), Entity: "light.kitchen", Name: "Kitchen", State: "on", A: json.RawMessage(`{}`), O: json.RawMessage(`{}`)},
			{I: 1, Slot: slot(1), Entity: "media_player.den", Name: "Den", State: "playing", A: json.RawMessage(`{}`), O: json.RawMessage(`{}`)},
		},
	}
	tessera.Preview(l, 4, 2)
	t.Cleanup(func() { tessera.Preview(nil, 0, 0) })
	prev := tessRoute
	tessRoute = func(entity string) string { return "media_player.ma_" + entity[len("media_player."):] }
	t.Cleanup(func() { tessRoute = prev })
	got := tessCalls(t)

	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	v, _ := tessera.Current()
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardTessera, tessera: v, tessOn: true})
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	at := map[string]image.Point{}
	zones, _ := r.dash()
	for _, z := range zones {
		at[z.entity] = image.Pt((z.r.Min.X+z.r.Max.X)/2, (z.r.Min.Y+z.r.Max.Y)/2)
	}

	d.tessTap(at["media_player.den"].X, at["media_player.den"].Y)
	if c := waitCalls(t, got, 1); len(c) != 1 || c[0].Service != "media_player.media_play_pause" || c[0].Data["entity_id"] != "media_player.ma_den" {
		t.Fatalf("a tap on the player called %+v", c)
	}

	// The add-on puts a switch where the light was; the page has not been drawn again.
	l.Tiles[0].Entity, l.Tiles[0].Name, l.Tiles[0].State = "switch.kettle", "Kettle", "off"
	tessera.Preview(l, 4, 2)
	d.tessTap(at["light.kitchen"].X, at["light.kitchen"].Y)
	tessera.SetLevel(0, "light.kitchen", 40)
	if c := got(); len(c) != 1 {
		t.Errorf("a finger on the old light called %+v", c[1:])
	}
}

// A level that is not a number fills nothing.
func TestTessFracOfNotANumber(t *testing.T) {
	for _, c := range []struct{ v, lo, hi, want float64 }{
		{50, 0, 100, 0.5},
		{math.NaN(), 0, 100, 0},
		{math.Inf(1), 0, 100, 1},
		{5, 30, 30, 0},
		{5, math.Inf(-1), math.Inf(1), 0},
	} {
		if got := tessFrac(c.v, c.lo, c.hi); got != c.want {
			t.Errorf("tessFrac(%v, %v, %v) = %v, want %v", c.v, c.lo, c.hi, got, c.want)
		}
	}
}

// The top bar's date is in the screen language, like the clock's.
func TestTesseraDateInTheScreensLanguage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local) // a Wednesday
	date := tessera.BarItem{Kind: "date"}
	if got := barText(date, at); got != "Wed, Sep 16" {
		t.Errorf("English date = %q", got)
	}
	if err := config.Set().Screen().Language("de"); err != nil {
		t.Fatal(err)
	}
	if got := barText(date, at); got != "Mi., 16. Sep." {
		t.Errorf("German date = %q, want %q", got, "Mi., 16. Sep.")
	}
}

// A tap on none of the page's tiles starts a voice turn, as a tap on the clock does, unless Tap on the
// clock says otherwise; a tap on a tile does only what the tile is for.
func TestTesseraTapBesideTheTilesTalks(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Dashboard().Mode(config.DashboardTessera); err != nil {
		t.Fatal(err)
	}
	slot := func(n int) *int { return &n }
	l := &tessera.Layout{
		Rev:   "0123456789abcdef",
		Begin: tessera.Begin{Pages: 1, Tiles: 1},
		Pages: []tessera.Page{{P: 0, Title: "Home", Items: json.RawMessage(`[]`)}},
		Tiles: []tessera.Tile{
			{I: 0, Slot: slot(0), Entity: "light.kitchen", Name: "Kitchen", State: "on", A: json.RawMessage(`{}`), O: json.RawMessage(`{}`)},
		},
	}
	tessera.Preview(l, 4, 2)
	t.Cleanup(func() { tessera.Preview(nil, 0, 0) })
	turns := 0
	prev := tessAssist
	tessAssist = func() { turns++ }
	t.Cleanup(func() { tessAssist = prev })
	tessCalls(t)

	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	v, _ := tessera.Current()
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardTessera, tessera: v, tessOn: true})
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	var light image.Point
	zones, _ := r.dash()
	for _, z := range zones {
		if z.entity == "light.kitchen" {
			light = image.Pt((z.r.Min.X+z.r.Max.X)/2, (z.r.Min.Y+z.r.Max.Y)/2)
		}
	}
	empty := image.Pt(showWide*7/8, showHigh*3/4) // the last cell of the foot row, which nothing is in
	if d.tileAt(empty.X, empty.Y) != nil {
		t.Fatal("the empty cell has a tile")
	}

	d.tessTap(light.X, light.Y)
	if turns != 0 {
		t.Error("a tap on a tile started a voice turn")
	}
	d.tessTap(empty.X, empty.Y)
	if turns != 1 {
		t.Errorf("a tap beside the tiles started %d voice turns, want 1", turns)
	}
	if err := config.Set().Screen().ClockTap("nothing"); err != nil {
		t.Fatal(err)
	}
	d.tessTap(empty.X, empty.Y)
	if turns != 1 {
		t.Error("a tap beside the tiles started a voice turn with Tap on the clock set to nothing")
	}
}
