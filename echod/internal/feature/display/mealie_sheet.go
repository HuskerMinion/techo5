//go:build !dot && !spot

package display

import (
	"context"
	"image"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mealie"
)

// The recipes of a Mealie server. It is one unfiltered list with all recipes.
// Tapping a recipe opens the step-by-step guide for it in place of the list, and Back returns to the
// list. The guide has a page for the ingredients, if there are any, and one for each step; Previous and
// Next turn the pages.

// mealieSheet is the recipe sheet while it is up.
type mealieSheet struct {
	// epoch identifies which opening of the sheet this is.
	// A request for the recipes remembers the epoch of the sheet it was made for. If the sheet is
	// closed and opened again before the answer comes, the answer arrives at a sheet with another epoch
	// and is thrown away, since the new sheet has asked for the recipes itself.
	epoch uint64
	// set is whether Mealie is set up on the device, and base its address, for pictures.
	set  bool
	base string
	// recipes are all recipes. listLoading is whether they are still fetching, listLoaded
	// whether all requests have been answered and listProblem why they could not be read (if an error occurred).
	recipes     []mealie.RecipeEntry
	listLoading bool
	listLoaded  bool
	listProblem string
	// selected is set to a recipe when a single one is selected, it is nil while the whole list is shown.
	selected *mealie.RecipeEntry
	// detail is the selected recipe's ingredients and steps, nil until they are read. detailLoading is
	// whether they are being asked for, and detailProblem why they could not be read (if an error occurred).
	detail        *mealie.DetailedRecipe
	detailLoading bool
	detailProblem string
	// step is the page of the guide that is shown, counted from 0 (see mealiePages).
	step int
	// scroll is how far the list, or the guide's page, is scrolled up, in pixels. Drawing keeps it within
	// what there is to scroll, so it may be too much for a list that has become shorter. listScroll is
	// where the list was scrolled when a recipe was opened, to put it back. drag is a finger on it.
	scroll     int
	listScroll int
	drag       mealieDrag
}

// mealieDrag is a finger on the list as it moves: where it came down, how far the list was scrolled
// then, and whether it has gone far enough to scroll it.
type mealieDrag struct {
	at        image.Point
	base      int
	scrolling bool
}

// mealieSheetEpochs hands out the epoch numbers.
var mealieSheetEpochs atomic.Uint64

// mealieClient returns the Mealie client set up on this device, and whether there is one. Can be set by tests to overwrite the behavior.
var mealieClient = func() (mealie.Client, bool) {
	m := config.Get().Mealie
	return mealie.Client{URL: m.URL, Token: m.Token}, m.Set()
}

// openMealie puts the recipe sheet up, and asks for the recipes when Mealie is set up.
func (d *Display) openMealie() {
	c, set := mealieClient()
	slog.Info("dashboard recipes", "set", set)
	d.mu.Lock()
	d.dashMealie = &mealieSheet{epoch: mealieSheetEpochs.Add(1), set: set, base: c.URL}
	d.mu.Unlock()
	d.wake()
	d.mealieLoadList()
}

// editMealie changes the recipe sheet, if it is up, without changing the one a frame is
// still drawing. The copy shares its slices with that one: assign new values, never edit in place.
func (d *Display) editMealie(change func(*mealieSheet)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dashMealie == nil {
		return
	}
	n := *d.dashMealie
	change(&n)
	d.dashMealie = &n
}

// mealieLoadList asks Mealie for all the recipes and puts them on the sheet when they come.
// Checks the epoch field to prevent changing on a request from a sheet that is no longer showing.
func (d *Display) mealieLoadList() {
	c, set := mealieClient()
	if !set {
		return
	}
	var epoch uint64
	asking := false
	d.editMealie(func(s *mealieSheet) {
		if s.listLoading || (s.listLoaded && s.listProblem == "") {
			return
		}
		s.listLoading, s.listProblem = true, ""
		epoch, asking = s.epoch, true
	})
	if !asking {
		return
	}
	d.wake()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		recipes, err := c.Recipes(ctx)
		d.editMealie(func(s *mealieSheet) {
			if s.epoch != epoch {
				return // the sheet was left, and opened again, while the recipes were being read
			}
			s.listLoading, s.listLoaded = false, true
			if err != nil {
				s.listProblem = err.Error()
				slog.Warn("dashboard recipes", "err", err)
				return
			}
			s.recipes = recipes
		})
		d.wake()
	}()
}

// mealieSheetNow returns the recipe sheet for a frame or nil when none is up. A sheet is never changed
// once it is up (editMealie puts a new one in its place), so the frame draws this one as it is.
func (d *Display) mealieSheetNow() *mealieSheet {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dashMealie
}

// mealieUp reports whether the recipe sheet is up.
func (d *Display) mealieUp() bool { return d.mealieSheetNow() != nil }

// mealieTap is a finger lifted at x, y. Done puts the sheet away, a recipe opens its guide, Back returns
// to the list, and Previous and Next turn the guide's pages. The sheet takes every tap.
func (d *Display) mealieTap(x, y int) bool {
	if !d.mealieUp() || d.r == nil {
		return false
	}
	z, _ := d.r.mealieAt(x, y)
	switch z.kind {
	case mealiePartDone:
		d.mu.Lock()
		d.dashMealie = nil
		d.mu.Unlock()
	case mealiePartItem:
		d.mealieOpenRecipe(z.index)
	case mealiePartBack:
		d.mealieBack()
	case mealiePartPrev:
		d.mealieTurn(-1)
	case mealiePartNext:
		d.mealieTurn(1)
	}
	d.wake()
	return true
}

// mealieOpenRecipe opens the guide of the recipe at index in the list, and asks Mealie for its
// ingredients and steps. Checks the epoch and the slug when they come, so that an answer for a recipe
// that was left, or for a sheet that was closed meanwhile, is thrown away.
func (d *Display) mealieOpenRecipe(index int) {
	c, set := mealieClient()
	var epoch uint64
	var slug string
	asking := false
	d.editMealie(func(s *mealieSheet) {
		if !set || s.selected != nil || index < 0 || index >= len(s.recipes) {
			return
		}
		entry := s.recipes[index]
		s.selected = &entry
		s.detail, s.detailLoading, s.detailProblem, s.step = nil, true, "", 0
		s.listScroll, s.scroll, s.drag = s.scroll, 0, mealieDrag{}
		epoch, slug, asking = s.epoch, entry.Slug, true
	})
	if !asking {
		return
	}
	d.wake()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		recipe, err := c.Recipe(ctx, slug)
		d.editMealie(func(s *mealieSheet) {
			if s.epoch != epoch || s.selected == nil || s.selected.Slug != slug {
				return // the sheet was closed, or the recipe left, while it was being read
			}
			s.detailLoading = false
			if err != nil {
				s.detailProblem = err.Error()
				slog.Warn("dashboard recipe", "err", err)
				return
			}
			s.detail = &recipe
		})
		d.wake()
	}()
}

// mealieBack leaves the guide for the list, which is scrolled where it was.
func (d *Display) mealieBack() {
	d.editMealie(func(s *mealieSheet) {
		if s.selected == nil {
			return
		}
		s.selected, s.detail, s.detailLoading, s.detailProblem, s.step = nil, nil, false, "", 0
		s.scroll, s.listScroll, s.drag = s.listScroll, 0, mealieDrag{}
	})
}

// mealieTurn turns the guide by pages-amount pages, forward or back, to the first or last at most. A page
// starts scrolled to its top.
func (d *Display) mealieTurn(pages int) {
	d.editMealie(func(s *mealieSheet) {
		if s.detail == nil {
			return
		}
		step := min(max(s.step+pages, 0), max(mealiePages(s.detail)-1, 0))
		if step == s.step {
			return
		}
		s.step, s.scroll, s.drag = step, 0, mealieDrag{}
	})
}

// mealiePages returns how many pages the guide of r has: one for the ingredients, if there are any, and
// one for each step.
func mealiePages(r *mealie.DetailedRecipe) int {
	n := len(r.Steps)
	if len(r.Ingredients) > 0 {
		n++
	}
	return n
}

// mealieDragStart is a finger coming down on the sheet: where, and how far the list is scrolled then.
func (d *Display) mealieDragStart(x, y int) {
	if d.r == nil {
		return
	}
	most := d.r.mealieMaxScroll()
	d.editMealie(func(s *mealieSheet) {
		s.drag = mealieDrag{at: image.Pt(x, y), base: min(s.scroll, most)}
	})
}

// mealieDragMove is the finger moving: once it has gone far enough up or down, the list follows it from
// there, as far as it can go. It says whether the sheet took the finger, which it does whenever it is
// up.
func (d *Display) mealieDragMove(_, y int) bool {
	s := d.mealieSheetNow()
	if s == nil || d.r == nil {
		return false
	}
	dy := y - s.drag.at.Y
	if !s.drag.scrolling && abs(dy) < d.r.s(slideStart) {
		return true
	}
	most := d.r.mealieMaxScroll()
	d.editMealie(func(n *mealieSheet) {
		n.drag.scrolling = true
		n.scroll = min(max(n.drag.base-dy, 0), most)
	})
	d.wake()
	return true
}

// mealieDragEnd is the finger lifting. It says whether it scrolled the list, which is then all it did.
func (d *Display) mealieDragEnd() bool {
	scrolled := false
	d.editMealie(func(s *mealieSheet) {
		scrolled = s.drag.scrolling
		s.drag = mealieDrag{}
	})
	return scrolled
}
