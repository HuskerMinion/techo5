//go:build !dot && !spot

package tessera

import "fmt"

// A screen says what it looks like in its Screen layout sensor: "<width>x<height> <columns>x<rows>", then
// optionally its density and "standard" or "compact". The add-on's editor draws a screen it has no board for
// from this alone, which is what every TECHO5 device is to it. The grid is what a page holds; cells near a
// third of the screen's height keep a tile a size a finger finds from across a kitchen.

// gridFor is the grid of a screen: the Show 8's 1280 by 800, the Skylight's 1920 by 1080, and the Show 5's
// 960 by 480, which no Tessera board has, for the rest.
func gridFor(show8, skylight bool) grid {
	switch {
	case show8:
		return grid{1280, 800, 4, 3}
	case skylight:
		return grid{1920, 1080, 5, 3}
	}
	return grid{960, 480, 4, 2}
}

// sizes are the sizes of tile a grid has room for, as a hello names them: one cell, two side by side,
// two stacked, two by two and the whole page by name, where they fit, and every other rectangle smaller
// than the page as columns by rows ("3x2"). The add-on names a rectangle a name covers by the name, and
// the whole page "full", so those two are left out of the spans.
func sizes(g grid) []string {
	out := []string{"single", "wide", "full"}
	if g.rows > 1 {
		out = append(out, "tall")
		if g.columns > 1 {
			out = append(out, "square")
		}
	}
	for c := 1; c <= g.columns; c++ {
		for r := 1; r <= g.rows; r++ {
			if (c > 2 || r > 2) && (c != g.columns || r != g.rows) {
				out = append(out, fmt.Sprintf("%dx%d", c, r))
			}
		}
	}
	return out
}

// gridForSize is the grid of a screen of any size, a panel's: the short side holds two cells below 600
// pixels and three from there, and the long side as many again as its length allows, at most eight. It
// gives the Shows' and the Skylight's grids for their sizes.
func gridForSize(w, h int) grid {
	short, long := h, w
	if w < h {
		short, long = w, h
	}
	across := 2
	if short >= 600 {
		across = 3
	}
	along := min(max(long*across/short, across), 8)
	if w < h {
		return grid{w, h, across, along}
	}
	return grid{w, h, along, across}
}
