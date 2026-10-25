//go:build !dot && !spot && !panel

package tessera

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// screenGrid is the grid of this Show's screen, known from its board.
func screenGrid() grid { return gridFor(layout.Show8(), layout.Skylight()) }
