//go:build !dot

package diag

import "github.com/HuskerMinion/techo5/echod/internal/hardware/lenscover"

// The camera's physical shutter, which only the Echo Show 8 has. On every other device these say
// there is none and the entity is left out, rather than reporting a cover that is permanently open
// and inviting somebody to trust it.

// lensCover is whether this device has a shutter and, if so, whether it is across the lens.
func lensCover() (present, covered bool) {
	c := lenscover.Get()
	return c.Present(), c.Covered()
}

// watchLensCover calls fn whenever the shutter moves, so Home Assistant hears about it when it
// happens rather than at the next refresh.
func watchLensCover(fn func(covered bool)) {
	lenscover.Get().Changed.Listen(fn)
}
