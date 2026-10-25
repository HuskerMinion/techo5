//go:build !dot && !spot

package tessera

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A grid of the owner's choosing (#268): more columns or rows than the screen's own, or fewer. The
// choices are those whose cells stay big enough to read and tap, and not much longer one way than
// the other.
const (
	gridAuto     = "Automatic"
	minCell      = 120 // pixels, either way
	maxColumns   = 8
	maxRows      = 5
	maxCellShape = 2 // a cell at most twice as wide as tall, or as tall as wide
)

// gridChoices are the grids a screen of base's size may be given, as columns by rows ("6x3").
func gridChoices(base grid) []string {
	var out []string
	for c := 1; c <= maxColumns; c++ {
		for r := 1; r <= maxRows; r++ {
			w, h := base.width/c, base.height/r
			if w < minCell || h < minCell || w > maxCellShape*h || h > maxCellShape*w {
				continue
			}
			out = append(out, fmt.Sprintf("%dx%d", c, r))
		}
	}
	return out
}

// chosen is base with the grid chosen in place of its own, when the choice is one base has room for.
func chosen(base grid, choice string) grid {
	if !offered(base, choice) {
		return base
	}
	var c, r int
	fmt.Sscanf(choice, "%dx%d", &c, &r)
	return grid{base.width, base.height, c, r}
}

// offered is whether choice is one of base's grids.
func offered(base grid, choice string) bool {
	for _, g := range gridChoices(base) {
		if g == choice {
			return true
		}
	}
	return false
}

// showGrid offers the choices for base, with the screen's own named under Automatic, and shows the
// one in force.
func (t *Tessera) showGrid(base grid, choice string) {
	auto := fmt.Sprintf("%s (%dx%d)", gridAuto, base.columns, base.rows)
	t.gridSel.Options = append([]string{auto}, gridChoices(base)...)
	if offered(base, choice) {
		t.gridSel.Set(choice)
		return
	}
	t.gridSel.Set(auto)
}

// chooseGrid is a grid picked in Home Assistant: kept, and told to the add-on at once, as a tablet's
// new screen size is.
func (t *Tessera) chooseGrid(v string) {
	base := screenGrid()
	if strings.HasPrefix(v, gridAuto) {
		v = ""
	} else if !offered(base, v) {
		t.showGrid(base, config.Get().Tessera.Grid)
		return
	}
	if err := config.Set().Tessera().Grid(v); err != nil {
		slog.Error("saving a setting failed", "setting", t.gridSel.ObjectID, "err", err)
		t.showGrid(base, config.Get().Tessera.Grid)
		return
	}
	slog.Info("setting changed", "setting", t.gridSel.ObjectID, "using", v)
	t.showGrid(base, v)
	t.regrid(chosen(base, v), "a grid chosen in Home Assistant")
}

// regrid tells the add-on a new grid while the screen is on: the tile sizes the next hello names and
// the Screen layout sensor, and the layout in force is laid on it again.
func (t *Tessera) regrid(g grid, why string) {
	s := current.Load()
	if !t.enabled.Load() || s == nil || s.grid == g {
		return
	}
	slog.Info("tessera: "+why, "grid", g.String(), "was", s.grid.String())
	t.recv.takes(capabilities(g))
	t.shape.Set(g.String())
	current.Store(newScreen(t.recv, g))
	Changed.Emit(struct{}{})
}
