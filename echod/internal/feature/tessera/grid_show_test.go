//go:build !dot && !spot && !panel

package tessera

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// The Show's grid is its board's; a board of this build that sets its own is laid out by that.
func TestABoardSetsItsOwnGrid(t *testing.T) {
	defer func(f func() grid) { screenGrid = f }(screenGrid)
	if got, want := screenGrid(), gridFor(layout.Show8(), layout.Skylight()); got != want {
		t.Errorf("screenGrid() = %v, want the board's %v", got, want)
	}
	screenGrid = func() grid { return gridForSize(1600, 900) }
	if got, want := chosen(screenGrid(), ""), (grid{1600, 900, 5, 3}); got != want {
		t.Errorf("the grid in force is %v, want the board's %v", got, want)
	}
}
