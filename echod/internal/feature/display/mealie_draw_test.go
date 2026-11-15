//go:build !dot && !spot

package display

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
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
			}
			writePNG(t, os.Getenv("SHOW_PREVIEW"), fmt.Sprintf("mealie-overview-%s.png", c.name), r.dst)
		})
	}
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
	if n := len(items(100, 0)); n == 0 || n >= 100 {
		t.Errorf("%d of 100 recipes drawn at the top, want some but not all", n)
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
// or after it, all of it on the card.
func TestMealieGuideDrawsItsParts(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
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
		writePNG(t, os.Getenv("SHOW_PREVIEW"), fmt.Sprintf("mealie-guide-%d.png", step), r.dst)
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
	long := longGuide()
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

// fetchedPictures makes the pictures of recipes come from a stand-in for the server, from a new cache,
// each a plain picture in c, or an answer with status when that is not 200, and returns the addresses asked for, as a
// function so that it can be read while a fetch is still on its way.
func fetchedPictures(t *testing.T, c color.RGBA, status int) func() []string {
	t.Helper()
	var mu sync.Mutex
	var asked []string
	prevPics := libPics
	libPics = &libPictures{} // none kept from another test
	t.Cleanup(func() { libPics = prevPics })
	prev := libFetch
	libFetch = func(_ context.Context, u string) ([]byte, int, error) {
		mu.Lock()
		asked = append(asked, u)
		mu.Unlock()
		if status != http.StatusOK {
			return nil, status, nil
		}
		img := image.NewRGBA(image.Rect(0, 0, 64, 64))
		draw.Draw(img, img.Rect, image.NewUniform(c), image.Point{}, draw.Src)
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return nil, 0, err
		}
		return buf.Bytes(), http.StatusOK, nil
	}
	t.Cleanup(func() { libFetch = prev })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(asked)
	}
}

// A recipe's tile shows its picture once it has come, and a recipe with none is not asked for at all. The
// pictures are asked for at the server's address by id and version, and a picture that arrives has the
// display draw the frame again.
func TestMealieTilesShowTheirPictures(t *testing.T) {
	red := color.RGBA{200, 30, 30, 255}
	asked := fetchedPictures(t, red, http.StatusOK)
	stubMealieClient(t, mealie.Client{}, false)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := newTestDisplay(r)
	d.openMealie() // which has the display draw again when a picture arrives
	forgetWake(d)
	s := loadedMealieSheet(testRecipes(3))
	s.base = "http://mealie.test"
	s.recipes[0].ImageVersion = "v1"
	s.recipes[2].ImageVersion = "v2"

	drawMealieSheet(r, s)
	waitWake(t, d)
	libWaitCovers()
	zones := drawMealieSheet(r, s)
	for i, want := range []bool{true, false, true} {
		b := zonesOfKind(zones, mealiePartItem)[i].r
		got := r.dst.RGBAAt(b.Min.X+b.Dx()/5, b.Min.Y+b.Dy()/5)
		if drawn := got.R > 190 && got.G < 40 && got.B < 40; drawn != want {
			t.Errorf("tile %d shows its picture: %v, want %v (pixel %v)", i, drawn, want, got)
		}
	}
	want := []string{
		"http://mealie.test/api/media/recipes/id-000/images/tiny-original.webp?version=v1",
		"http://mealie.test/api/media/recipes/id-002/images/tiny-original.webp?version=v2",
	}
	got := asked()
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("asked for %v, want %v", got, want)
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
