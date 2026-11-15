//go:build !dot && !spot

package display

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mdi"
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

// A scrolled sheet draws the recipes further up, the rows scrolled out are gone, and what is drawn stays
// in the view. Scrolled past the end it stops with the last row whole in view, and a list that fits has
// nothing to scroll.
func TestMealieOverviewScrolls(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	g := r.mealieGeometry()
	items := func(n, scroll int) []mediaZone {
		s := loadedMealieSheet(testRecipes(n))
		s.scroll = scroll
		return zonesOfKind(drawMealieSheet(r, s), mealiePartItem)
	}
	for _, z := range items(100, 2*g.tileH) {
		if !z.r.In(g.view) {
			t.Errorf("recipe %d reaches to %v, outside the view %v", z.index, z.r, g.view)
		}
		if z.index < g.cols {
			t.Errorf("recipe %d of the first row is still drawn two rows down", z.index)
		}
	}
	end := items(100, 1<<20)
	if len(end) == 0 {
		t.Fatal("nothing is drawn scrolled to the end")
	}
	if last := end[len(end)-1]; last.index != 99 || last.r.Dy() != g.tileH {
		t.Errorf("the last recipe is %d at %v after scrolling to the end, want 99 whole in view", last.index, last.r)
	}
	if !reflect.DeepEqual(end, items(100, r.mealieMaxScroll())) {
		t.Error("scrolling past the end draws something other than scrolling to it")
	}
	if !reflect.DeepEqual(items(3, 0), items(3, 500)) {
		t.Error("a list that fits scrolled")
	}
}

// The guide has Back where the list has Done, and Previous and Next where the page has others before
// or after it, on the Show 5 and the Show 8, and all of it is on the card.
func TestMealieGuideDrawsItsParts(t *testing.T) {
	for _, c := range []struct {
		name string
		size image.Point
	}{{"show5", image.Pt(showWide, showHigh)}, {"show8", image.Pt(show8Wide, show8High)}} {
		t.Run(c.name, func(t *testing.T) {
			r := newRenderer(image.NewRGBA(image.Rectangle{Max: c.size}))
			card := r.mealieGeometry().card
			for step := range 4 { // the ingredients, then three steps
				s := guideSheet(3)
				s.step = step
				zones := drawMealieSheet(r, s)
				want := map[int]int{mealiePartBack: 1, mealiePartDone: 0, mealiePartItem: 0, mealiePartPrev: min(step, 1), mealiePartNext: min(3-step, 1)}
				for kind, n := range want {
					if got := len(zonesOfKind(zones, kind)); got != n {
						t.Errorf("page %d has %d parts of kind %d, want %d", step, got, kind, n)
					}
				}
				for _, z := range zones {
					if !z.r.In(card) {
						t.Errorf("page %d: part %+v reaches outside the card %v", step, z, card)
					}
				}
				savePreview(t, fmt.Sprintf("mealie-guide-%s-%d.png", c.name, step), r.dst)
			}
		})
	}
}

// The footer says the ingredients, or which step of how many; with no ingredients the first step is the
// first page.
func TestMealieWhere(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json")) // the screen's language is English
	withIngredients := guideSheet(3).detail
	without := guideSheet(3).detail
	without.Ingredients = nil
	for _, c := range []struct {
		recipe *mealie.DetailedRecipe
		step   int
		want   string
	}{
		{withIngredients, 0, "Ingredients"}, {withIngredients, 1, "Step 1 of 3"}, {withIngredients, 3, "Step 3 of 3"},
		{without, 0, "Step 1 of 3"}, {without, 2, "Step 3 of 3"},
	} {
		if got := mealieWhere(c.recipe, c.step); got != c.want {
			t.Errorf("%d ingredients, page %d: %q, want %q", len(c.recipe.Ingredients), c.step, got, c.want)
		}
	}
}

// A page that is longer than its room scrolls, and keeps to the room: scrolled to the top or to the end,
// what is above and below it is the same, so that no text reaches into the bar or the footer.
func TestMealieGuidePageKeepsToItsRoom(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	g := r.mealieGeometry()
	long := guideSheet(2)
	for i := range 40 {
		long.detail.Ingredients = append(long.detail.Ingredients, mealie.Ingredient{Text: fmt.Sprintf("%d spoons of something that goes on and on", i)})
	}
	drawMealieSheet(r, long)
	most := r.mealieMaxScroll()
	if most == 0 {
		t.Fatal("40 ingredients have nothing to scroll")
	}
	top := image.NewRGBA(r.dst.Rect)
	copy(top.Pix, r.dst.Pix)
	scrolled := long
	scrolled.scroll = most
	drawMealieSheet(r, scrolled)
	if bytes.Equal(top.Pix, r.dst.Pix) {
		t.Error("the page looks the same scrolled to its end")
	}
	for name, room := range map[string]image.Rectangle{
		"above the page": image.Rect(g.card.Min.X, g.card.Min.Y, g.card.Max.X, g.guide.Min.Y),
		"below the page": image.Rect(g.card.Min.X, g.guide.Max.Y, g.card.Max.X, g.card.Max.Y),
	} {
		for y := room.Min.Y; y < room.Max.Y; y++ {
			a, b := top.PixOffset(room.Min.X, y), top.PixOffset(room.Max.X, y)
			if !bytes.Equal(top.Pix[a:b], r.dst.Pix[a:b]) {
				t.Errorf("row %d, %s, changed when the page scrolled", y, name)
				break
			}
		}
	}

	drawMealieSheet(r, guideSheet(2))
	if got := r.mealieMaxScroll(); got != 0 {
		t.Errorf("a short page can be scrolled %d pixels, want none", got)
	}
}

// A page that scrolls show a scroll bar at its right edge, with the bar the settings lists have; one that fits has
// nothing there but the card, and none is drawn where the page is not.
func TestMealieGuideShowsThatAPageScrolls(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	g := r.mealieGeometry()
	edge := image.Rect(g.card.Max.X-r.s(14), g.guide.Min.Y, g.card.Max.X-r.s(6), g.guide.Max.Y)
	colors := func(s mealieSheet) int {
		drawMealieSheet(r, s)
		seen := map[color.RGBA]bool{}
		for y := edge.Min.Y; y < edge.Max.Y; y++ {
			for x := edge.Min.X; x < edge.Max.X; x++ {
				seen[r.dst.RGBAAt(x, y)] = true
			}
		}
		return len(seen)
	}
	if got := colors(guideSheet(2)); got != 1 {
		t.Errorf("a page that fits has %d colors at its edge, want the card's alone", got)
	}
	long := guideSheet(2)
	for i := range 40 {
		long.detail.Ingredients = append(long.detail.Ingredients, mealie.Ingredient{Text: fmt.Sprintf("%d spoons of something", i)})
	}
	if got := colors(long); got < 2 {
		t.Errorf("a page that scrolls has %d colors at its edge, want a bar on the card", got)
	}
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

// guideWith returns a guide sheet, as guideSheet(2) has it, changed by change.
func guideWith(change func(*mealieSheet)) mealieSheet {
	s := guideSheet(2)
	change(&s)
	return s
}

// The sheet says what is the matter in place of the recipes, for each state it can be in: Mealie not
// set up says what to do about it, being asked says so, a problem says what it was, and no recipes
// says there are none and what to do. A sheet with recipes has nothing to say, and shows them. Each
// has its Done to leave by, and the guide of a recipe its Back.
func TestMealieNoticeSaysWhatIsTheMatter(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json")) // the screen's language is English
	for _, c := range []struct {
		name            string
		sheet           mealieSheet
		icon, msg, hint string
	}{
		{"not set up", mealieSheet{}, "cog-outline", "Set up Mealie on the setup page to browse your recipes here.", ""},
		{"not set up, whatever it was asked before", mealieSheet{listLoaded: true, listProblem: "mealie refused the token"}, "cog-outline", "Set up Mealie on the setup page to browse your recipes here.", ""},
		{"loading", mealieSheet{set: true, listLoading: true}, "", "Loading…", ""},
		{"a problem", mealieSheet{set: true, listLoaded: true, listProblem: "mealie refused the token"},
			"alert-circle-outline", "mealie refused the token", ""},
		{"none", mealieSheet{set: true, listLoaded: true}, "chef-hat", "Nothing here yet", "Add recipes in Mealie, and they show up here."},
		{"a recipe being read", guideWith(func(s *mealieSheet) { s.detail, s.detailLoading = nil, true }), "", "Loading…", ""},
		{"a recipe that could not be read", guideWith(func(s *mealieSheet) { s.detail, s.detailProblem = nil, "mealie answered 404 Not Found" }),
			"alert-circle-outline", "mealie answered 404 Not Found", ""},
		{"a recipe with nothing in it", guideWith(func(s *mealieSheet) { s.detail.Ingredients = nil; s.detail.Steps = nil }),
			"chef-hat", "Nothing here yet", "This recipe has no ingredients or steps."},
		{"a recipe's page", guideSheet(2), "", "", ""},
		{"recipes", loadedMealieSheet(testRecipes(3)), "", "", ""},
		{"recipes, asked for again", mealieSheet{set: true, listLoaded: true, listLoading: true, recipes: testRecipes(3)}, "", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			icon, msg, hint := mealieNotice(&c.sheet)
			if icon != c.icon || msg != c.msg || hint != c.hint {
				t.Errorf("icon %q, message %q, hint %q; want %q, %q, %q", icon, msg, hint, c.icon, c.msg, c.hint)
			}
			if _, ok := mdi.Rune(icon); icon != "" && !ok {
				t.Errorf("icon %q is not in the icon font, so nothing would be drawn", icon)
			}
			r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
			zones := drawMealieSheet(r, c.sheet)
			if got := len(zonesOfKind(zones, mealiePartDone)) + len(zonesOfKind(zones, mealiePartBack)); got != 1 {
				t.Errorf("%d Done or Back buttons drawn, want 1", got)
			}
		})
	}
}
