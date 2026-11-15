//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mealie"
)

// testRecipes returns n recipes named "Recipe 000", "Recipe 001" and so on.
func testRecipes(n int) []mealie.RecipeEntry {
	out := make([]mealie.RecipeEntry, n)
	for i := range out {
		out[i] = mealie.RecipeEntry{ID: fmt.Sprintf("id-%03d", i), Slug: fmt.Sprintf("recipe-%03d", i), Name: fmt.Sprintf("Recipe %03d", i)}
	}
	return out
}

// loadedMealieSheet returns a recipe sheet for a Mealie that is set up and has answered with recipes.
func loadedMealieSheet(recipes []mealie.RecipeEntry) mealieSheet {
	return mealieSheet{set: true, recipes: recipes, listLoaded: true}
}

// drawMealieSheet draws the sheet s on the renderer r, over a plain backdrop standing in for the dashboard, and returns the
// zones it kept.
func drawMealieSheet(r *renderer, s mealieSheet) []mediaZone {
	r.useFont()
	fillCanvas(r.dst, walnut)
	r.mealieSheet(&s, dashboard.Theme{})
	r.zmu.Lock()
	defer r.zmu.Unlock()
	return append([]mediaZone(nil), r.mealieZones...)
}

// zonesOfKind returns the zones of one kind, in the order they were drawn.
func zonesOfKind(zones []mediaZone, kind int) []mediaZone {
	var out []mediaZone
	for _, z := range zones {
		if z.kind == kind {
			out = append(out, z)
		}
	}
	return out
}

// savePreview writes img as name in the directory SHOW_PREVIEW names, to look at, when it is set.
func savePreview(t *testing.T, name string, img image.Image) {
	t.Helper()
	dir := os.Getenv("SHOW_PREVIEW")
	if dir == "" {
		return
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// A sheet with recipes draws a tile for each that fits, in reading order and in as many columns as
// the covers of the library have, with a Done button.
func TestMealieOverviewDrawsTheRecipes(t *testing.T) {
	for _, c := range []struct {
		name string
		size image.Point
		cols int
	}{
		{"show5", image.Pt(showWide, showHigh), 4},
		{"show8", image.Pt(show8Wide, show8High), 5},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRenderer(image.NewRGBA(image.Rectangle{Max: c.size}))
			zones := drawMealieSheet(r, loadedMealieSheet(testRecipes(12)))
			if got := len(zonesOfKind(zones, mealiePartDone)); got != 1 {
				t.Errorf("%d Done buttons, want 1", got)
			}
			items := zonesOfKind(zones, mealiePartItem)
			if len(items) < c.cols {
				t.Fatalf("%d recipes drawn, want at least a row of %d", len(items), c.cols)
			}
			abreast := 0
			for _, z := range items {
				if z.r.Min.Y == items[0].r.Min.Y {
					abreast++
				}
			}
			if abreast != c.cols {
				t.Errorf("%d recipes abreast, want %d", abreast, c.cols)
			}
			for i, z := range items {
				if z.index != i {
					t.Errorf("the tile %d is recipe %d, want them in the order of the list", i, z.index)
				}
				x, y := (z.r.Min.X+z.r.Max.X)/2, (z.r.Min.Y+z.r.Max.Y)/2
				if got, on := r.mealieAt(x, y); !on || got.kind != mealiePartItem || got.index != i {
					t.Errorf("the middle of tile %d is found as %+v (on the sheet: %v)", i, got, on)
				}
			}
			done := zonesOfKind(zones, mealiePartDone)[0]
			if got, _ := r.mealieAt((done.r.Min.X+done.r.Max.X)/2, (done.r.Min.Y+done.r.Max.Y)/2); got.kind != mealiePartDone {
				t.Errorf("the middle of Done is found as %+v", got)
			}
			savePreview(t, fmt.Sprintf("mealie-overview-%s.png", c.name), r.dst)
		})
	}
}

// More recipes than fit are not all drawn: only what is in view has a tile to tap, and none reaches
// outside the card.
func TestMealieOverviewDrawsOnlyWhatIsInView(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	zones := drawMealieSheet(r, loadedMealieSheet(testRecipes(200)))
	items := zonesOfKind(zones, mealiePartItem)
	if len(items) == 0 || len(items) >= 200 {
		t.Fatalf("%d of 200 recipes drawn, want some but not all", len(items))
	}
	card := r.mealieGeometry().card
	for _, z := range items {
		if !z.r.In(card) {
			t.Errorf("recipe %d reaches to %v, outside the card %v", z.index, z.r, card)
		}
	}
	savePreview(t, "mealie-overview-many.png", r.dst)
}

// Drawing no sheet forgets the parts of the one drawn before, so that a tap finds nothing of it.
func TestMealieOverviewDrawnNoMoreHasNoParts(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	drawMealieSheet(r, loadedMealieSheet(testRecipes(3)))
	r.mealieSheet(nil, dashboard.Theme{})
	if _, on := r.mealieAt(showWide/2, showHigh/2); on {
		t.Error("a tap in the middle is still on a sheet that is gone")
	}
}

// The sheet says what is the matter in place of the recipes, for each state it can be in: Mealie not
// set up says what to do about it, being asked says so, a problem says what it was, and no recipes
// says there are none and what to do. A sheet with recipes has nothing to say, and shows them. Each
// has its Done to leave by.
func TestMealieNoticeSaysWhatIsTheMatter(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json")) // the screen's language is English
	for _, c := range []struct {
		name            string
		sheet           mealieSheet
		icon, msg, hint string
	}{
		{"not set up", mealieSheet{}, "", "Set up Mealie on the setup page to browse your recipes here.", ""},
		{"not set up, whatever it was asked before", mealieSheet{listLoaded: true, listProblem: "mealie refused the token"}, "", "Set up Mealie on the setup page to browse your recipes here.", ""},
		{"loading", mealieSheet{set: true, listLoading: true}, "", "Loading…", ""},
		{"a problem", mealieSheet{set: true, listLoaded: true, listProblem: "mealie refused the token"},
			"alert-circle-outline", "mealie refused the token", ""},
		{"none", mealieSheet{set: true, listLoaded: true}, "chef-hat", "Nothing here yet", "Add recipes in Mealie, and they show up here."},
		{"recipes", loadedMealieSheet(testRecipes(3)), "", "", ""},
		{"recipes, asked for again", mealieSheet{set: true, listLoaded: true, listLoading: true, recipes: testRecipes(3)}, "", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			icon, msg, hint := mealieNotice(&c.sheet)
			if icon != c.icon || msg != c.msg || hint != c.hint {
				t.Errorf("icon %q, message %q, hint %q; want %q, %q, %q", icon, msg, hint, c.icon, c.msg, c.hint)
			}
			r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
			if got := len(zonesOfKind(drawMealieSheet(r, c.sheet), mealiePartDone)); got != 1 {
				t.Errorf("%d Done buttons drawn, want 1", got)
			}
		})
	}
}
