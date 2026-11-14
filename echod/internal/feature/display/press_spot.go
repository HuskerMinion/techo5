//go:build spot

package display

// The Spot shows no press on its dashboard tiles (press.go): there is nothing to drop.
func (d *Display) dropPressLocked() {}
