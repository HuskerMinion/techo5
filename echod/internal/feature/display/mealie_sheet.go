//go:build !dot && !spot

package display

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mealie"
)

// The recipes of a Mealie server. It is one unfiltered list with all recipes.
// Tapping a recipe opens the step-by-step guide for it in place of the list, and Back returns to the
// list.

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

// mealieTap is a finger lifted at x, y. Done puts the sheet away.
func (d *Display) mealieTap(x, y int) bool {
	if !d.mealieUp() || d.r == nil {
		return false
	}
	if z, _ := d.r.mealieAt(x, y); z.kind == mealiePartDone {
		d.mu.Lock()
		d.dashMealie = nil
		d.mu.Unlock()
	}
	d.wake()
	return true
}
