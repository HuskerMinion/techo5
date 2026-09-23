//go:build dot || spot

package setup

import "github.com/HuskerMinion/techo5/echod/internal/lib/palette"

// pageColors is the Spot's look on the Spot, whose round screen has one, and on the Dot, which has
// no screen to match and borrows it.
func pageColors() palette.Preset { return palette.Round }
