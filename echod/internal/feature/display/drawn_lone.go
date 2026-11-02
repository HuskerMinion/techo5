//go:build !dot

package display

import (
	"image"
	"image/color"
	"image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// A drawn view of one heading, one markdown card or one tile, with Dashboard tiles on Fill the screen:
// its words drawn as large as the screen takes them, so a warning page can be read across a room. A
// color on the card (red, amber, #c00000) is the whole page's color, with the words in white or black,
// whichever reads better on it. A view of anything more is laid out as usual.

// lone is the one card of a view.
type lone struct {
	icon   string
	main   []string // the large words: a heading, a markdown card's paragraphs, a tile's state
	title  string   // the smaller words over (markdown) or under (heading, tile) them
	titleA bool     // title is over the main words
	tile   *dashboard.Tile
	color  color.RGBA
}

// loneCard is the view's one card, when it has exactly one and it is a heading, markdown or tile.
func loneCard(v dashboard.Drawn) (lone, bool) {
	if len(v.Sections) != 1 || len(v.Sections[0].Blocks) != 1 {
		return lone{}, false
	}
	b := v.Sections[0].Blocks[0]
	switch {
	case b.Heading != "" && len(b.Tiles) == 0 && len(b.Text) == 0 && b.Graph == nil && b.Gauge == nil && b.Picture == nil:
		return lone{main: []string{b.Heading}, title: b.Right, color: b.Color}, true
	case len(b.Text) > 0 && b.Heading == "" && len(b.Tiles) == 0 && len(b.Rows) == 0 && b.Graph == nil && b.Gauge == nil && b.Picture == nil:
		return lone{main: b.Text, title: b.Title, titleA: true, color: b.Color}, true
	case len(b.Tiles) == 1 && b.Tiles[0].Adjust == nil && !b.Tiles[0].Switch && b.Heading == "" && b.Title == "" && len(b.Rows) == 0 && len(b.Text) == 0 && b.Graph == nil && b.Gauge == nil && b.Picture == nil:
		t := b.Tiles[0]
		l := lone{icon: t.Icon, title: t.Name, tile: &t, color: t.Color}
		if t.Value != "" {
			l.main = []string{t.Value}
		} else {
			l.main, l.title = []string{t.Name}, ""
		}
		return l, true
	}
	return lone{}, false
}

// onColor is white or near black, whichever reads better on c.
func onColor(c color.RGBA) color.RGBA {
	if (299*int(c.R)+587*int(c.G)+114*int(c.B))/1000 > 150 {
		return color.RGBA{20, 20, 20, 255}
	}
	return color.RGBA{255, 255, 255, 255}
}

// lonePage draws the lone card over the whole of area. Its tile, if it is one, keeps its tap.
func (r *paint) lonePage(l lone, area image.Rectangle, pal dashPal) {
	fg, dim := pal.text, pal.sub
	if l.color.A != 0 {
		draw.Draw(r.dst, area, image.NewUniform(l.color), image.Point{}, draw.Src)
		fg = onColor(l.color)
		dim = fg
	} else {
		draw.Draw(r.dst, area, image.NewUniform(pal.bg), image.Point{}, draw.Src)
	}
	in := area.Inset(r.s(dashSide) * 2)
	if r.round {
		in = in.Inset(in.Dx() * 14 / 100) // the square a circle holds
	}

	// The regions, as shares of the height: an icon, the main words, the smaller words.
	iconH, titleH := 0, 0
	if l.icon != "" {
		iconH = in.Dy() * 18 / 100
	}
	if l.title != "" {
		titleH = in.Dy() * 20 / 100
	}
	y := in.Min.Y
	if iconH > 0 {
		sz := iconH * 100 / max(1, r.s(100))
		r.mdiIcon(l.icon, in.Min.X+(in.Dx()-r.s(sz))/2, y, sz, fg)
		y += iconH
	}
	var top, bottom *image.Rectangle
	mainBox := image.Rect(in.Min.X, y, in.Max.X, in.Max.Y-titleH)
	if l.title != "" {
		box := image.Rect(in.Min.X, in.Max.Y-titleH, in.Max.X, in.Max.Y)
		bottom = &box
		if l.titleA {
			t := image.Rect(in.Min.X, y, in.Max.X, y+titleH)
			top, bottom = &t, nil
			mainBox = image.Rect(in.Min.X, y+titleH, in.Max.X, in.Max.Y)
		}
	}
	r.centeredWords(mainBox, l.main, true, stepsDown(480, 24, 8), fg)
	for _, box := range []*image.Rectangle{top, bottom} {
		if box != nil {
			r.centeredWords(*box, []string{l.title}, false, stepsDown(96, 20, 4), dim)
		}
	}

	var zones []dashTile
	if l.tile != nil {
		r.zone(&zones, area, *l.tile)
	}
	r.setDash(zones, area.Dy())
}

// centeredWords draws paras as large as box takes them, each line centered across it and the lines
// centered down it.
func (r *paint) centeredWords(box image.Rectangle, paras []string, bold bool, sizes []int, c color.RGBA) {
	lines, face, lineH := r.fitParagraphs(bold, paras, box.Dx(), box.Dy(), sizes)
	if face == nil {
		return
	}
	y := box.Min.Y + (box.Dy()-len(lines)*lineH)/2
	for _, line := range lines {
		r.text(face, line, box.Min.X+(box.Dx()-r.width(face, line))/2, y+face.Metrics().Ascent.Ceil(), c)
		y += lineH
	}
}
