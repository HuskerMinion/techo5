//go:build !dot

package display

import (
	"image"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// The drawn dashboard filling the screen: a view of only a few tiles, as a panel by a door often is,
// laid out as a grid over the whole page instead of in the usual columns, each tile as big as its
// share. The Dashboard tiles setting's Fill the screen; a view with anything else on it, or too many
// tiles to read across the room, is drawn large instead.

// fillMost is the most tiles a filled page lays out: three rows of three on a Show 5 still leaves
// each tile wider than a usual one.
const fillMost = 9

// fewTiles is every tile of a view that is only tiles, under headings or not, when there are few
// enough to fill the page with.
func fewTiles(v dashboard.Drawn) ([]dashboard.Tile, bool) {
	var out []dashboard.Tile
	for _, sec := range v.Sections {
		for _, b := range sec.Blocks {
			switch {
			case b.Heading != "":
			case len(b.Tiles) > 0:
				out = append(out, b.Tiles...)
			default:
				return nil, false
			}
		}
	}
	return out, len(out) > 0 && len(out) <= fillMost
}

// fillColumns is how many tiles go across a filled page: one, two side by side, three in a row,
// two by two, and three across for five to nine.
func fillColumns(n int) int {
	switch {
	case n <= 1:
		return 1
	case n == 2, n == 4:
		return 2
	}
	return 3
}

// fillGrid lays tiles out over area as fillColumns says, the rows sharing its height, and keeps where
// each went for a tap. The page does not scroll.
func (r *paint) fillGrid(tiles []dashboard.Tile, area image.Rectangle, pal dashPal, adj dashAdjusting) {
	side, gap := r.s(dashSide), r.s(dashGap)
	cols := fillColumns(len(tiles))
	rows := (len(tiles) + cols - 1) / cols
	inner := area.Inset(side)
	tw := (inner.Dx() - gap*(cols-1)) / cols
	th := (inner.Dy() - gap*(rows-1)) / rows
	var zones []dashTile
	for i, t := range tiles {
		x := inner.Min.X + (i%cols)*(tw+gap)
		y := inner.Min.Y + (i/cols)*(th+gap)
		box := image.Rect(x, y, x+tw, y+th)
		r.tile(box, t, pal, adj)
		r.zone(&zones, box, t)
	}
	r.setDash(zones, area.Dy())
}
