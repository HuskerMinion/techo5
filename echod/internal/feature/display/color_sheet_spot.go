//go:build spot

package display

import "image"

// The Spot has no color sheet: a finger held still and lifted on its dashboard is the ring menu.

func (d *Display) longPress(*dashTile) {}

func (d *Display) colorOpen() bool { return false }

func (d *Display) colorTap(int, int) bool { return false }

func (d *Display) onColorSheet(int, int) bool { return false }

func (d *Display) sliderAt(int, int) (sheetSlider, bool) { return sheetSlider{}, false }

func (d *Display) slideSheet(sheetSlider, int, bool) {}

func (d *Display) sliderBegins(sheetSlider) {}

func (s sheetSlider) slides(bool) bool { return true }

func (d *Display) pageSwipeOnSheet(image.Point, int, int) bool { return false }

func (d *Display) samePartOnSheet(image.Point, image.Point) bool { return true }
