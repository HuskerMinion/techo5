//go:build spot

package display

// The Spot has no color sheet: a finger held still and lifted on its dashboard is the ring menu.

func (d *Display) longPress(*dashTile) {}

func (d *Display) colorOpen() bool { return false }

func (d *Display) colorTap(int, int) bool { return false }

func (d *Display) onColorSheet(int, int) bool { return false }

func (d *Display) sliderAt(int, int) (sheetSlider, bool) { return sheetSlider{}, false }

func (d *Display) slideSheet(sheetSlider, int, bool) {}
