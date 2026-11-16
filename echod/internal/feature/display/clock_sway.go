//go:build !dot

package display

// sway is a walk from 0 out to +reach, back through 0 to -reach and home again, one step at a time: at
// step m, of a round of 4*reach steps. The Show's clock (clock_drift.go) and the Spot's
// (clock_drift_spot.go) wander along it against image retention.
func sway(m, reach int) int {
	if reach <= 0 {
		return 0
	}
	p := m % (4 * reach)
	switch {
	case p <= reach:
		return p
	case p <= 3*reach:
		return 2*reach - p
	}
	return p - 4*reach
}
