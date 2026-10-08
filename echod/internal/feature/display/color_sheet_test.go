//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// The color sheet over the page has what the light can take: a band of whites when it has whites,
// the colors when it has colors, and Done. A tap at the band's ends is its warmest and coolest white,
// on kelvinSnap's steps; one beside the sheet is off it. With SHOW_PREVIEW set, each is written there.
func TestTheColorSheetOffersWhatTheLightCanTake(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	at := time.Date(2026, 10, 4, 14, 7, 0, 0, time.Local)
	for _, c := range []struct {
		name         string
		light        dashboard.LightColor
		whites, hues int
	}{
		{"both", dashboard.LightColor{Entity: "light.desk", Name: "Desk lamp", Kelvin: true, MinK: 2202, MaxK: 6535, NowK: 3000, Colors: true}, 1, 1},
		{"whites", dashboard.LightColor{Entity: "light.ceiling", Name: "Ceiling", Kelvin: true, MinK: 2700, MaxK: 6500}, 1, 0},
		{"colors", dashboard.LightColor{Entity: "light.strip", Name: "Strip", Colors: true, HasHue: true, NowHue: 120}, 0, 1},
		{"group", dashboard.LightColor{Entity: "light.lounge", Name: "Lounge", Kelvin: true, MinK: 2000, MaxK: 6535, Colors: true, Group: true}, 1, 1},
	} {
		img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
		r := newRenderer(img)
		light := c.light
		r.draw(scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls(), dashColor: &light})
		r.zmu.Lock()
		zones, card := r.colorZones, r.colorCard
		r.zmu.Unlock()
		count := map[int]int{}
		for _, z := range zones {
			count[z.kind]++
			if !z.r.In(card) {
				t.Errorf("%s: part %v outside the sheet %v", c.name, z.r, card)
			}
		}
		if count[colorPartWhite] != c.whites || count[colorPartHue] != c.hues || count[colorPartDone] != 1 {
			t.Errorf("%s: parts %v, want %d whites, %d colors and Done", c.name, count, c.whites, c.hues)
		}
		for _, z := range zones {
			if z.kind == colorPartHue {
				mid := (z.r.Min.Y + z.r.Max.Y) / 2
				if hit, _ := colorHit(zones, card, z.r.Min.X, mid); hit.value != 0 {
					t.Errorf("%s: the band of colors starts at hue %v, want red", c.name, hit.value)
				}
				if hit, _ := colorHit(zones, card, z.r.Min.X+z.r.Dx()/3, mid); hit.value < 115 || hit.value > 125 {
					t.Errorf("%s: a third along the band of colors is hue %v, want green", c.name, hit.value)
				}
			}
			if z.kind != colorPartWhite {
				continue
			}
			mid := (z.r.Min.Y + z.r.Max.Y) / 2
			if hit, _ := colorHit(zones, card, z.r.Min.X, mid); hit.value != c.light.MinK {
				t.Errorf("%s: the warm end is %v kelvin, want %v", c.name, hit.value, c.light.MinK)
			}
			if hit, _ := colorHit(zones, card, z.r.Max.X-1, mid); hit.value != c.light.MaxK {
				t.Errorf("%s: the cool end is %v kelvin, want %v", c.name, hit.value, c.light.MaxK)
			}
		}
		if _, in := colorHit(zones, card, card.Min.X-5, card.Min.Y-5); in {
			t.Errorf("%s: a tap beside the sheet was on it", c.name)
		}
		if dir != "" {
			f, err := os.Create(filepath.Join(dir, "color-sheet-"+c.name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			png.Encode(f, img)
			f.Close()
		}
	}
}

// Without a sheet up, there is nothing of one to tap.
func TestNoColorSheetLeavesNothingToTap(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: fourControls()})
	if _, in := r.colorAt(showWide/2, showHigh/2); in {
		t.Error("a tap was on a color sheet that is not up")
	}
}
