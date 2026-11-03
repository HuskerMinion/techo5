//go:build !dot && !spot && !panel

package tessera

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// screenGrid is the grid of this Show's screen, known from its board. A variable: a board of this
// build whose screen is none of the Shows' sets its own (gridForSize, from its panel's size).
var screenGrid = func() grid { return gridFor(layout.Show8(), layout.Skylight()) }
