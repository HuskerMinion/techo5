//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mealie"
)

// mockedRecipesEndpoint returns a handler that imitates Mealie's GET /api/recipes. It answers with
// recipes named "Recipe 000", "Recipe 001" and so on, one page at a time
func mockedRecipesEndpoint(total int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		per, _ := strconv.Atoi(r.URL.Query().Get("perPage"))
		if page < 1 || per < 1 {
			http.Error(w, "bad page", http.StatusBadRequest)
			return
		}
		var items []string
		for i := (page - 1) * per; i < min(page*per, total); i++ {
			items = append(items, fmt.Sprintf(`{"id":"id-%03d","name":"Recipe %03d","slug":"recipe-%03d","image":"v1"}`, i, i, i))
		}
		_, _ = fmt.Fprintf(w, `{"page":%d,"per_page":%d,"total":%d,"total_pages":%d,"items":[%s]}`,
			page, per, total, (total+per-1)/per, strings.Join(items, ","))
	}
}

// mockedMealie returns a handler that imitates Mealie with total recipes: the list at /api/recipes, and
// at /api/recipes/recipe-NNN a recipe of that name with two ingredients and three steps.
func mockedMealie(total int) http.HandlerFunc {
	list := mockedRecipesEndpoint(total)
	return func(w http.ResponseWriter, r *http.Request) {
		slug, isRecipe := strings.CutPrefix(r.URL.Path, "/api/recipes/")
		if !isRecipe {
			list(w, r)
			return
		}
		n := strings.TrimPrefix(slug, "recipe-")
		_, _ = fmt.Fprintf(w, `{"slug":%q,"name":"Recipe %s",
			"recipeIngredient":[{"display":"2 eggs of %s"},{"display":"1 pinch of salt"}],
			"recipeInstructions":[{"text":"First of %s."},{"title":"Then","text":"Second."},{"text":"Third."}]}`, slug, n, n, n)
	}
}

// guideSheet returns a loaded sheet with the recipe at index 0 open, its detail read: two ingredients and
// n steps, so n+1 pages.
func guideSheet(n int) mealieSheet {
	s := loadedMealieSheet(testRecipes(6))
	entry := s.recipes[0]
	recipe := &mealie.DetailedRecipe{Slug: entry.Slug, Name: entry.Name,
		Ingredients: []mealie.Ingredient{{Text: "2 eggs"}, {Text: "1 pinch of salt"}}}
	for i := range n {
		recipe.Steps = append(recipe.Steps, mealie.Step{Text: fmt.Sprintf("Step %d.", i+1)})
	}
	s.selected, s.detail = &entry, recipe
	return s
}

// tapPart draws the sheet d has up on r and taps the part of that kind. It says whether there was one.
func tapPart(r *renderer, d *Display, kind int) bool {
	zones := zonesOfKind(drawFrame(r, d.mealieSheetNow()), kind)
	if len(zones) == 0 {
		return false
	}
	x, y := middleOf(zones[0])
	d.mealieTap(x, y)
	return true
}

// stubMealieClient makes the recipe page use c as its Mealie client, and sets whether Mealie is set up
// (set).
func stubMealieClient(t *testing.T, c mealie.Client, set bool) {
	t.Helper()
	previousClient := mealieClient
	mealieClient = func() (mealie.Client, bool) { return c, set }
	t.Cleanup(func() { mealieClient = previousClient })
}

// newTestDisplay returns the smallest Display the recipe page can run on.
func newTestDisplay() *Display {
	return &Display{poke: make(chan struct{}, 1)}
}

// mealieSheetOf returns a copy of the recipe sheet that d has up, taken under the display's lock so that it
// is safe to read while a load is still running. It is empty when no sheet is up.
func mealieSheetOf(d *Display) mealieSheet {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dashMealie == nil {
		return mealieSheet{}
	}
	return *d.dashMealie
}

// Opening the recipe sheet shows the list, asks Mealie for the recipes, and ends up with all of
// them, however many pages Mealie sends them in.
func TestMealieOpensAndLoadsTheRecipes(t *testing.T) {
	srv := httptest.NewServer(mockedRecipesEndpoint(40))
	t.Cleanup(srv.Close)
	stubMealieClient(t, mealie.Client{URL: srv.URL, Token: "tok"}, true)
	d := newTestDisplay()
	d.openMealie()
	if s := mealieSheetOf(d); !s.set || s.selected != nil || s.epoch == 0 {
		t.Fatalf("opened on %+v, want a sheet that is set up and shows the list", s)
	}
	waitUntil(t, "the recipes", func() bool { s := mealieSheetOf(d); return s.listLoaded && !s.listLoading })
	s := mealieSheetOf(d)
	if s.listProblem != "" {
		t.Fatalf("problem: %s", s.listProblem)
	}
	if len(s.recipes) != 40 || s.recipes[0].Name != "Recipe 000" || s.recipes[39].Name != "Recipe 039" {
		t.Errorf("got %d recipes, from %q to %q", len(s.recipes), s.recipes[0].Name, s.recipes[len(s.recipes)-1].Name)
	}
}

// When Mealie is not set up the sheet still opens and displays a "not configured" message
func TestMealieNotSetUp(t *testing.T) {
	stubMealieClient(t, mealie.Client{}, false)
	d := newTestDisplay()
	d.openMealie()
	d.mu.Lock()
	s := d.dashMealie
	d.mu.Unlock()
	if s == nil || s.set || s.listLoading || s.listLoaded {
		t.Errorf("sheet = %+v, want one up, not set, and not asked for", s)
	}
}

// When Mealie fails, the sheet keeps the reason, and a later load of the list tries again.
func TestMealieAsksAgainAfterAProblem(t *testing.T) {
	var down atomic.Bool
	down.Store(true)
	working := mockedRecipesEndpoint(3)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		working(w, r)
	}))
	t.Cleanup(srv.Close)
	stubMealieClient(t, mealie.Client{URL: srv.URL}, true)
	d := newTestDisplay()
	d.openMealie()
	waitUntil(t, "the problem", func() bool { s := mealieSheetOf(d); return s.listLoaded && !s.listLoading })
	s := mealieSheetOf(d)
	if !strings.Contains(s.listProblem, "500") || len(s.recipes) != 0 {
		t.Fatalf("problem %q with %d recipes, want the status and none", s.listProblem, len(s.recipes))
	}
	down.Store(false) // Mealie is back
	d.mealieLoadList()
	waitUntil(t, "the recipes", func() bool { s := mealieSheetOf(d); return !s.listLoading && s.listProblem == "" })
	if got := len(mealieSheetOf(d).recipes); got != 3 {
		t.Errorf("got %d recipes, want 3", got)
	}
}

// Loading the list again while a request for it is still waiting must not send a second request. If
// the sheet is left before the answer comes, the answer is thrown away and must not bring the sheet
// back.
func TestMealieLeftWhileLoading(t *testing.T) {
	var requests atomic.Int32
	answer := make(chan struct{}) // closed to let the fake Mealie answer. Until then, it holds the request
	var answerOnce sync.Once
	letMealieAnswer := func() { answerOnce.Do(func() { close(answer) }) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		<-answer
		mockedRecipesEndpoint(3)(w, r)
	}))
	t.Cleanup(srv.Close)
	// Cleanups run last to first, so this one runs before srv.Close, which waits for held requests.
	t.Cleanup(letMealieAnswer)
	stubMealieClient(t, mealie.Client{URL: srv.URL}, true)
	d := newTestDisplay()
	d.openMealie()
	waitUntil(t, "the request", func() bool { return requests.Load() >= 1 })

	d.mealieLoadList() // the list again, while its first request is still waiting
	d.mu.Lock()
	d.dashMealie = nil // the sheet is left
	d.mu.Unlock()

	select { // forget the redraws asked for so far, so the next one is the load ending
	case <-d.poke:
	default:
	}
	letMealieAnswer()
	select {
	case <-d.poke: // the load has ended, and asked for a redraw
	case <-time.After(5 * time.Second):
		t.Fatal("the read did not end")
	}

	d.mu.Lock()
	left := d.dashMealie
	d.mu.Unlock()
	if left != nil {
		t.Error("the answer for a sheet that was left put the sheet back")
	}
	if n := requests.Load(); n != 1 {
		t.Errorf("Mealie was asked %d times, want once", n)
	}
}

// An answer to an earlier opening of the sheet is not put on a sheet opened since: the first opening's
// request is still waiting when the sheet is left and opened again, and what it brings is thrown away.
func TestMealieAnswerForAnEarlierOpeningIsThrownAway(t *testing.T) {
	answer := make(chan struct{})
	var answerOnce sync.Once
	letMealieAnswer := func() { answerOnce.Do(func() { close(answer) }) }
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			<-answer // the first opening's request waits; the second opening's is answered at once
			mockedRecipesEndpoint(5)(w, r)
			return
		}
		mockedRecipesEndpoint(2)(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(letMealieAnswer)
	stubMealieClient(t, mealie.Client{URL: srv.URL}, true)
	d := newTestDisplay()
	d.openMealie()
	waitUntil(t, "the first request", func() bool { return requests.Load() >= 1 })
	d.mu.Lock()
	d.dashMealie = nil // the sheet is left
	d.mu.Unlock()
	d.openMealie() // and opened again, which asks for the list afresh
	waitUntil(t, "the second opening's recipes", func() bool { s := mealieSheetOf(d); return s.listLoaded && !s.listLoading })

	letMealieAnswer() // now the first opening's answer arrives, five recipes
	time.Sleep(200 * time.Millisecond)
	if got := len(mealieSheetOf(d).recipes); got != 2 {
		t.Errorf("the sheet has %d recipes, want the second opening's 2", got)
	}
}

// drawFrame draws a whole frame of the drawn dashboard on r, with the recipe sheet s over it, or none
// when s is nil, and returns the zones the recipe sheet kept.
func drawFrame(r *renderer, s *mealieSheet) []mediaZone {
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn,
		drawn: fourControls(), dashMealie: s})
	r.zmu.Lock()
	defer r.zmu.Unlock()
	return append([]mediaZone(nil), r.mealieZones...)
}

// middleOf returns the point in the middle of z.
func middleOf(z mediaZone) (int, int) {
	return (z.r.Min.X + z.r.Max.X) / 2, (z.r.Min.Y + z.r.Max.Y) / 2
}

// A card that navigates to dashboard.MealiePath opens the recipe sheet.
func TestMealieCardOpensTheSheet(t *testing.T) {
	stubMealieClient(t, mealie.Client{}, false)
	page := dashboard.Drawn{Sections: []dashboard.Section{{Blocks: []dashboard.Block{{Tiles: []dashboard.Tile{
		{Name: "Recipes", Icon: "chef-hat", Tap: &dashboard.Action{View: dashboard.MealiePath}},
	}}}}}}
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	r.draw(scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: page})
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	tiles, _ := r.dash()
	if len(tiles) != 1 {
		t.Fatalf("%d tiles drawn, want 1", len(tiles))
	}
	if d.mealieUp() {
		t.Fatal("the recipe sheet is up before the card was tapped")
	}
	d.drawnTap((tiles[0].r.Min.X+tiles[0].r.Max.X)/2, (tiles[0].r.Min.Y+tiles[0].r.Max.Y)/2)
	if !d.mealieUp() {
		t.Fatal("the card did not open the recipe sheet")
	}
}

// A frame with a recipe sheet draws it over the dashboard, so that its parts are there to be tapped;
// the next frame without one has none.
func TestMealieSheetIsDrawnOverTheDashboard(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	sheet := loadedMealieSheet(testRecipes(6))
	zones := drawFrame(r, &sheet)
	if got := len(zonesOfKind(zones, mealiePartDone)); got != 1 {
		t.Errorf("%d Done buttons in a frame with the sheet, want 1", got)
	}
	if len(zonesOfKind(zones, mealiePartItem)) == 0 {
		t.Error("no recipes in a frame with the sheet")
	}
	drawFrame(r, nil)
	if _, on := r.mealieAt(showWide/2, showHigh/2); on {
		t.Error("the sheet is still there in a frame without it")
	}
}

// While the recipe sheet is up it takes every tap: a tap beside the parts changes nothing, and Done puts
// the sheet away.
func TestMealieSheetTakesTheTaps(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	if d.mealieTap(10, 10) {
		t.Fatal("a tap was taken with no recipe sheet up")
	}
	sheet := loadedMealieSheet(testRecipes(6))
	d.dashMealie = &sheet
	zones := drawFrame(r, d.mealieSheetNow())
	done := zonesOfKind(zones, mealiePartDone)[0]
	bx, by := middleOf(mediaZone{r: r.mealieGeometry().bar})
	dx, dy := middleOf(done)
	if !d.colorTap(bx, by) {
		t.Error("a tap beside the parts was not taken by the sheet")
	}
	if !d.mealieUp() {
		t.Error("a tap beside the parts put the sheet away")
	}
	if !d.colorTap(dx, dy) {
		t.Error("a tap on Done was not taken by the sheet")
	}
	if d.mealieUp() {
		t.Error("Done did not put the sheet away")
	}
}

// A finger dragged up or down on the recipes scrolls them as it goes, as far as there is to scroll and
// no further, and a wobble of a tap does not. It is the sheet's finger to where it lifts, so the sheet
// stays up when that is Done and the drag is over.
func TestMealieDragScrollsTheRecipes(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	sheet := loadedMealieSheet(testRecipes(100))
	d.dashMealie = &sheet
	zones := drawFrame(r, d.mealieSheetNow())
	most := r.mealieMaxScroll()
	if most == 0 {
		t.Fatal("100 recipes have nothing to scroll")
	}
	x, y := middleOf(zonesOfKind(zones, mealiePartItem)[0])
	scroll := func() int { return d.mealieSheetNow().scroll }

	d.drawnHold(x, y)
	d.drawnMove(x, y-3)
	if got := scroll(); got != 0 {
		t.Errorf("a wobble scrolled to %d, want 0", got)
	}
	for _, c := range []struct {
		name     string
		to, want int
	}{
		{"a drag up", y - 100, 100},
		{"a drag up past the end", y - most - 500, most},
		{"a drag down past the start", y + 500, 0},
	} {
		d.drawnMove(x, c.to)
		if got := scroll(); got != c.want {
			t.Errorf("%s scrolled to %d, want %d", c.name, got, c.want)
		}
	}
	done := zonesOfKind(zones, mealiePartDone)[0]
	dx, dy := middleOf(done)
	d.drawnMove(x, y-100)
	d.drawnMove(dx, dy)
	d.drawnRelease(dx, dy)
	if !d.mealieUp() {
		t.Error("a scroll that lifted on Done put the sheet away")
	}
	if d.mealieSheetNow().drag != (mealieDrag{}) {
		t.Error("the finger is still on the list after it lifted")
	}

	// Scrolled further than there is, the list is drawn at its end, and a finger takes it from there.
	d.editMealie(func(s *mealieSheet) { s.scroll = most + 300 })
	d.drawnHold(x, y)
	d.drawnMove(x, y+100)
	if got := scroll(); got != most-100 {
		t.Errorf("a drag down from past the end scrolled to %d, want %d", got, most-100)
	}
}

// A tap on a recipe opens its guide at the first page and asks Mealie for the recipe; Back returns to
// the list, scrolled where it was.
func TestMealieRecipeOpensItsGuide(t *testing.T) {
	srv := httptest.NewServer(mockedMealie(100))
	t.Cleanup(srv.Close)
	stubMealieClient(t, mealie.Client{URL: srv.URL}, true)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	d.openMealie()
	waitUntil(t, "the list", func() bool { return mealieSheetOf(d).listLoaded })
	d.editMealie(func(s *mealieSheet) { s.scroll = 100 })

	items := zonesOfKind(drawFrame(r, d.mealieSheetNow()), mealiePartItem)
	x, y := middleOf(items[0])
	d.mealieTap(x, y)
	slug := fmt.Sprintf("recipe-%03d", items[0].index)
	if s := mealieSheetOf(d); s.selected == nil || s.selected.Slug != slug || s.step != 0 || s.scroll != 0 {
		t.Fatalf("after the tap the sheet has selected %+v at step %d, scrolled %d; want %s at the first page", s.selected, s.step, s.scroll, slug)
	}
	waitUntil(t, "the recipe", func() bool { return mealieSheetOf(d).detail != nil })
	if got := mealieSheetOf(d).detail; got.Slug != slug || len(got.Ingredients) != 2 || len(got.Steps) != 3 {
		t.Errorf("the guide has %+v", got)
	}

	if !tapPart(r, d, mealiePartBack) {
		t.Fatal("the guide has no Back")
	}
	if s := mealieSheetOf(d); s.selected != nil || s.detail != nil || s.scroll != 100 {
		t.Errorf("after Back the sheet has selected %v, detail %v, scrolled %d; want the list scrolled to 100", s.selected, s.detail, s.scroll)
	}
}

// Next and Previous turn the pages, the ingredients and then each step: there is no Previous on the
// first page and no Next on the last, and a page starts at its top.
func TestMealieGuideTurnsItsPages(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	sheet := guideSheet(2) // three pages
	d.dashMealie = &sheet
	has := func(kind int) bool { return len(zonesOfKind(drawFrame(r, d.mealieSheetNow()), kind)) == 1 }
	for _, c := range []struct {
		step       int
		prev, next bool
	}{{0, false, true}, {1, true, true}, {2, true, false}} {
		if c.step > 0 {
			d.editMealie(func(s *mealieSheet) { s.scroll = 50 })
			if !tapPart(r, d, mealiePartNext) {
				t.Fatalf("no Next to go to step %d", c.step)
			}
			if s := mealieSheetOf(d); s.step != c.step || s.scroll != 0 {
				t.Fatalf("after Next the guide is at page %d scrolled %d, want %d at the top", s.step, s.scroll, c.step)
			}
		}
		if has(mealiePartPrev) != c.prev || has(mealiePartNext) != c.next {
			t.Errorf("page %d has Previous %v and Next %v, want %v and %v", c.step, has(mealiePartPrev), has(mealiePartNext), c.prev, c.next)
		}
	}
	for want := 1; want >= 0; want-- {
		if !tapPart(r, d, mealiePartPrev) || mealieSheetOf(d).step != want {
			t.Fatalf("Previous did not go to page %d", want)
		}
	}
	d.mealieTurn(5) // a turn that is more than there is stays on the last page
	if got := mealieSheetOf(d).step; got != 2 {
		t.Errorf("turned 5 pages on to page %d, want the last, 2", got)
	}
}

// An answer for a recipe that was left is thrown away: the first recipe's request is still waiting when
// Back is tapped and another recipe opened, and what it brings does not replace the second's.
func TestMealieAnswerForARecipeThatWasLeftIsThrownAway(t *testing.T) {
	answer := make(chan struct{})
	var answerOnce sync.Once
	letMealieAnswer := func() { answerOnce.Do(func() { close(answer) }) }
	var details atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/recipes/") && details.Add(1) == 1 {
			<-answer // the first recipe's request waits; the second's is answered at once
		}
		mockedMealie(5)(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(letMealieAnswer)
	stubMealieClient(t, mealie.Client{URL: srv.URL}, true)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	d.openMealie()
	waitUntil(t, "the list", func() bool { return mealieSheetOf(d).listLoaded })

	d.mealieOpenRecipe(0)
	waitUntil(t, "the first request", func() bool { return details.Load() >= 1 })
	d.mealieBack()
	d.mealieOpenRecipe(1)
	waitUntil(t, "the second recipe", func() bool { return mealieSheetOf(d).detail != nil })

	letMealieAnswer() // now the first recipe's answer arrives
	time.Sleep(200 * time.Millisecond)
	if got := mealieSheetOf(d).detail; got == nil || got.Name != "Recipe 001" {
		t.Errorf("the guide has %+v, want the second recipe's", got)
	}
	d.mealieBack()
	d.mu.Lock()
	d.dashMealie = nil // and the sheet closed, while a recipe is on its way, is not brought back
	d.mu.Unlock()
	d.mealieOpenRecipe(0)
	if d.mealieUp() {
		t.Error("opening a recipe with no sheet up put the sheet up")
	}
}

// A recipe that cannot be read says why in the guide, with Back to leave by, and asking for it again is
// a tap on it again.
func TestMealieGuideSaysWhyARecipeCannotBeRead(t *testing.T) {
	var details atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/recipes/") {
			details.Add(1)
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		mockedMealie(3)(w, r)
	}))
	t.Cleanup(srv.Close)
	stubMealieClient(t, mealie.Client{URL: srv.URL}, true)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	d.openMealie()
	waitUntil(t, "the list", func() bool { return mealieSheetOf(d).listLoaded })
	for want := int32(1); want <= 2; want++ {
		if !tapPart(r, d, mealiePartItem) {
			t.Fatal("the list has no recipe to tap")
		}
		waitUntil(t, "the problem", func() bool { return mealieSheetOf(d).detailProblem != "" })
		if s := mealieSheetOf(d); s.detailProblem != "mealie answered 500 Internal Server Error" || s.detailLoading || s.detail != nil {
			t.Errorf("the guide has problem %q, loading %v, detail %v", s.detailProblem, s.detailLoading, s.detail)
		}
		if details.Load() != want {
			t.Errorf("Mealie was asked for the recipe %d times, want %d", details.Load(), want)
		}
		if !tapPart(r, d, mealiePartBack) {
			t.Fatal("a guide with a problem has no Back")
		}
	}
}

// A page too long for its room scrolls under a finger, as the list does, to its end and no further; a
// finger that scrolled it and lifted on Next did that only, and the page is not turned.
func TestMealieGuidePageScrollsAndIsNotTurnedByIt(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	d := &Display{r: r, poke: make(chan struct{}, 1)}
	sheet := guideSheet(2)
	for i := range 40 {
		sheet.detail.Ingredients = append(sheet.detail.Ingredients, mealie.Ingredient{Text: fmt.Sprintf("%d spoons of something", i)})
	}
	d.dashMealie = &sheet
	zones := drawFrame(r, d.mealieSheetNow())
	most := r.mealieMaxScroll()
	if most == 0 {
		t.Fatal("40 ingredients have nothing to scroll")
	}
	g := r.mealieGeometry()
	x, y := middleOf(mediaZone{r: g.guide})
	nx, ny := middleOf(zonesOfKind(zones, mealiePartNext)[0])

	d.drawnHold(x, y)
	d.drawnMove(x, y-100)
	if got := mealieSheetOf(d).scroll; got != 100 {
		t.Errorf("a drag of 100 up scrolled the page to %d", got)
	}
	d.drawnMove(x, y-most-500)
	if got := mealieSheetOf(d).scroll; got != most {
		t.Errorf("a drag past the end scrolled the page to %d, want %d", got, most)
	}
	d.drawnMove(nx, ny)
	d.drawnRelease(nx, ny)
	if got := mealieSheetOf(d).step; got != 0 {
		t.Errorf("a scroll that lifted on Next turned the page to %d", got)
	}
}
