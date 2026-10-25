//go:build !dot && !spot

package display

import (
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/tessera"
)

// The places of a world clock: the plugin's defaults where the options say nothing, none left out, a
// name of the person's own over the city's, and how far each is put as the plugin puts it.
func TestWorldClockPlaces(t *testing.T) {
	p := wcPlaces(map[string]any{})
	if len(p) != 3 || p[0].name != "New York" || !p[1].here || p[1].name != "Here" || p[2].name != "London" {
		t.Fatalf("defaults %+v", p)
	}
	p = wcPlaces(map[string]any{"zone_1": "Asia/Kolkata", "zone_2": "none", "zone_3": "local", "name_3": " Home "})
	if len(p) != 2 || p[0].name != "Mumbai" || p[1].name != "Home" {
		t.Fatalf("chosen %+v", p)
	}
	for _, c := range []struct {
		diff time.Duration
		days int
		want string
	}{
		{-6 * time.Hour, 0, "-6 h"}, {5*time.Hour + 30*time.Minute, 0, "+5:30 h"}, {0, 0, "Same time"},
		{8 * time.Hour, 1, "Tomorrow · +8 h"}, {-10 * time.Hour, -1, "Yesterday · -10 h"},
	} {
		if got := wcAway(c.diff, c.days); got != c.want {
			t.Errorf("%v %d days: %q, want %q", c.diff, c.days, got, c.want)
		}
	}
}

// A world clock's tile draws its clocks, dials or, after a tap, digits; a tap takes nothing to Home
// Assistant or the assistant. A plugin the screen lacks is a card that says so.
func TestWorldClockTile(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Dashboard().Mode(config.DashboardTessera); err != nil {
		t.Fatal(err)
	}
	slot := func(n int) *int { return &n }
	l := &tessera.Layout{
		Rev:   "plugin0123456789",
		Begin: tessera.Begin{Pages: 1, Tiles: 3},
		Pages: []tessera.Page{{P: 0, Title: "Home", Items: json.RawMessage(`[]`)}},
		Tiles: []tessera.Tile{
			{I: 0, Slot: slot(0), Entity: "plugin:world_clock.clocks", Name: "World clock", State: "ok", A: json.RawMessage(`{}`),
				O: json.RawMessage(`{"size":"3x2","plugin":{"zone_1":"America/New_York","zone_2":"local","zone_3":"Asia/Tokyo","face":"analog","second_hand":"home"}}`)},
			{I: 1, Slot: slot(12), Entity: "plugin:world_clock.clocks", Name: "World clock", State: "ok", A: json.RawMessage(`{}`),
				O: json.RawMessage(`{"size":"3x1","plugin":{"zone_1":"Europe/London","zone_2":"local","zone_3":"Australia/Sydney","face":"calm","second_hand":"all"}}`)},
			{I: 2, Slot: slot(3), Entity: "plugin:departures.next", Name: "Departures", State: "ok", A: json.RawMessage(`{}`), O: json.RawMessage(`{"size":"wide"}`)},
		},
	}
	tessera.Preview(l, 6, 5)
	t.Cleanup(func() { tessera.Preview(nil, 0, 0) })
	t.Cleanup(func() { wcDigits.Store(false) })
	got := tessCalls(t)

	r := newRenderer(image.NewRGBA(image.Rect(0, 0, show8Wide, show8High)))
	v, _ := tessera.Current()
	now := time.Date(2026, 10, 25, 10, 10, 30, 0, time.Local)
	sc := scene{now: now, phase: "idle", showDash: true, dashMode: config.DashboardTessera, tessera: v, tessOn: true}
	r.draw(sc)
	save := func(name string) {
		if dir := os.Getenv("TECHO5_PREVIEW"); dir != "" {
			f, err := os.Create(filepath.Join(dir, name))
			if err == nil {
				_ = png.Encode(f, r.dst)
				f.Close()
			}
		}
	}
	save("world-clock-dials.png")
	var clock image.Rectangle
	zones, _ := r.dash()
	for _, z := range zones {
		if z.tess == 1 {
			clock = z.r
		}
	}
	if clock.Empty() {
		t.Fatal("the world clock takes no tap")
	}
	// Ink of a dial in the middle of each of its three columns.
	inked := func() int {
		card := r.dst.RGBAAt(clock.Min.X+3, clock.Min.Y+clock.Dy()/2) // inside the card's padding
		n := 0
		for i := range 3 {
			x := clock.Min.X + clock.Dx()*(2*i+1)/6
			for y := clock.Min.Y; y < clock.Max.Y; y++ {
				if c := r.dst.RGBAAt(x, y); c != card {
					n++
				}
			}
		}
		return n
	}
	dials := inked()
	if dials < 60 {
		t.Fatalf("the clocks drew %d pixels down their middles", dials)
	}
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	d.tessTap(clock.Min.X+clock.Dx()/2, clock.Min.Y+clock.Dy()/2)
	if !wcDigits.Load() || len(got()) != 0 {
		t.Fatalf("a tap on the clock: digits %v, calls %v", wcDigits.Load(), got())
	}
	r.draw(sc)
	save("world-clock-digits.png")
	if digits := inked(); digits >= dials {
		t.Errorf("the digits drew %d pixels down the middles, no fewer than the dials' %d", digits, dials)
	}
}
