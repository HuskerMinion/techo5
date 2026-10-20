//go:build !dot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/picscale"
)

// A picture drawn whole: a picture-elements card's, whose elements are places on it to tap, and a
// view's only picture, which fills the screen. The picture is drawn as large as fits with its own
// proportions, nothing of it cut off, so that a page drawn as one picture shows every button on it,
// each where its element says, and the screen's color around it where the two shapes differ.

// elementSide is the size of an element whose style gives it none: Home Assistant's icon and its
// padding, a little larger to be a finger's.
const elementSide = 48

// lonePicture is the picture of a view that is nothing else: one section, one picture.
func lonePicture(v dashboard.Drawn) (dashboard.Picture, bool) {
	if len(v.Sections) != 1 || len(v.Sections[0].Blocks) != 1 || v.Sections[0].Blocks[0].Picture == nil {
		return dashboard.Picture{}, false
	}
	return *v.Sections[0].Blocks[0].Picture, true
}

// wholePage draws a view's only picture over the whole of area, and keeps where its elements went.
// The page does not scroll.
func (r *paint) wholePage(p dashboard.Picture, area image.Rectangle, pal dashPal) {
	var zones []dashTile
	r.wholePicture(area, p, pal, &zones)
	r.setDash(zones, area.Dy())
}

// fit is the largest part of the frame dst with the proportions of a picture src, centered: the
// picture shown whole, with the frame's own color at its sides or above and below it.
func fit(src, dst image.Rectangle) image.Rectangle {
	sw, sh, dw, dh := src.Dx(), src.Dy(), dst.Dx(), dst.Dy()
	if sw <= 0 || sh <= 0 || dw <= 0 || dh <= 0 {
		return dst
	}
	if sw*dh > sh*dw {
		h := max(1, dw*sh/sw)
		y := dst.Min.Y + (dh-h)/2
		return image.Rect(dst.Min.X, y, dst.Max.X, y+h)
	}
	w := max(1, dh*sw/sh)
	x := dst.Min.X + (dw-w)/2
	return image.Rect(x, dst.Min.Y, x+w, dst.Max.Y)
}

// elementBox is where an element is on a picture drawn at pic: its size in percent of the picture,
// or natural pixels square when its style gives none, moved from its point by its shift.
func elementBox(e dashboard.Element, pic image.Rectangle, natural int) image.Rectangle {
	w, h := e.W*float64(pic.Dx()), e.H*float64(pic.Dy())
	if e.W <= 0 {
		w = float64(natural)
	}
	if e.H <= 0 {
		h = float64(natural)
	}
	x := float64(pic.Min.X) + e.X*float64(pic.Dx()) + e.ShiftX*w
	y := float64(pic.Min.Y) + e.Y*float64(pic.Dy()) + e.ShiftY*h
	return image.Rect(int(math.Round(x)), int(math.Round(y)), int(math.Round(x+w)), int(math.Round(y+h)))
}

// pictureNote is what a card says in place of a picture it has not drawn, and false while there is
// nothing to say.
func pictureNote(p dashboard.Picture) (string, bool) {
	switch {
	case p.Image != nil && !p.Image.Bounds().Empty():
		return "", false
	case p.TooLarge:
		return "Too large to show here", true
	case p.Failed:
		return "The picture would not load", true
	}
	return "Loading the picture…", true
}

// shapeOf is a picture's shape: the picture's own, its size when it is known but not drawn (a
// picture too large to decode), or nothing.
func shapeOf(p dashboard.Picture) image.Rectangle {
	if p.Image != nil {
		return p.Image.Bounds()
	}
	return image.Rectangle{Max: p.Shape}
}

// wholePicture draws a picture whole in frame, its elements over it, and keeps the places to tap:
// the elements, the last one first as it is the one on top, then the picture's own tap. Until the
// picture has arrived there is nothing to place them on, and nothing is kept. One that will not be
// drawn, too large or not loading, still has them: on a blank frame of its shape when that is known,
// on the whole frame when not, so a page of buttons still works.
func (r *paint) wholePicture(frame image.Rectangle, p dashboard.Picture, pal dashPal, tiles *[]dashTile) {
	fc := r.faces()
	pic := fit(shapeOf(p), frame)
	if msg, ok := pictureNote(p); ok {
		if p.TooLarge || p.Failed {
			draw.Draw(r.dst, pic, image.NewUniform(pal.card), image.Point{}, draw.Src) // the blank frame
		}
		r.text(fc.sub, msg, pic.Min.X+(pic.Dx()-r.width(fc.sub, msg))/2, pic.Min.Y+pic.Dy()/2, pal.sub)
		if !p.TooLarge && !p.Failed {
			return
		}
	} else {
		picscale.CatmullRom.Scale(r.dst, pic, p.Image, p.Image.Bounds(), draw.Src)
	}
	boxes := make([]image.Rectangle, len(p.Elements))
	for i, e := range p.Elements {
		boxes[i] = elementBox(e, pic, r.s(elementSide))
		r.element(boxes[i], e, pic, pal)
	}
	for i := len(p.Elements) - 1; i >= 0; i-- {
		// Only what is on the picture: an element running off its edge is tapped on the part on it.
		if box := boxes[i].Intersect(pic); !box.Empty() {
			r.zone(tiles, box, dashboard.Tile{Tap: p.Elements[i].Tap, Entity: p.Elements[i].Entity})
		}
	}
	if p.Name != "" {
		band := image.Rect(pic.Min.X, pic.Max.Y-r.s(40), pic.Max.X, pic.Max.Y)
		washOver(r.dst, band, color.RGBA{0, 0, 0, 140})
		r.text(fc.label, r.fit(fc.label, p.Name, pic.Dx()-2*r.s(cardPad)), pic.Min.X+r.s(cardPad), pic.Max.Y-r.s(12), color.RGBA{255, 255, 255, 255})
	}
	r.zone(tiles, pic, dashboard.Tile{Tap: p.Tap})
}

// element draws what an element shows, centered in its box: an icon, a state icon's lit when its
// entity is on, or a state label's or a service button's words, in the style's color when it gives
// one. The words are on a see-through pill of the page's color, so that they stay readable on any
// picture. Anything else, and an element its style hides, is the picture's to show.
func (r *paint) element(box image.Rectangle, e dashboard.Element, pic image.Rectangle, pal dashPal) {
	if e.Hidden {
		return
	}
	c := pal.text
	switch {
	case e.Gone:
		c = lerp(pal.text, pal.bg, 0.5)
	case e.On:
		c = pal.active
	case e.Color.A != 0:
		c = e.Color
	}
	cx, cy := (box.Min.X+box.Max.X)/2, (box.Min.Y+box.Max.Y)/2
	switch {
	case e.Icon != "":
		const isz = 28
		r.mdiIcon(e.Icon, cx-r.s(isz)/2, cy-r.s(isz)/2, isz, c)
	case e.Label != "":
		fc := r.faces()
		text := r.fit(fc.label, e.Label, pic.Dx()-2*r.s(labelPad))
		w := r.width(fc.label, text)
		pill := image.Rect(cx-w/2-r.s(labelPad), cy-r.s(14), cx+w/2+r.s(labelPad), cy+r.s(14))
		r.roundOver(pill, float64(pill.Dy())/2, pal.bg, 0.6)
		r.text(fc.label, text, cx-w/2, cy+r.s(9), c)
	}
}

// labelPad is the room either side of an element's words on their pill.
const labelPad = 10

// roundOver lays c over a rounded rectangle at opacity a: a see-through pill.
func (r *paint) roundOver(b image.Rectangle, rad float64, c color.RGBA, a float64) {
	x0, y0, x1, y1 := rectF(b)
	for y := b.Min.Y - 1; y <= b.Max.Y; y++ {
		for x := b.Min.X - 1; x <= b.Max.X; x++ {
			r.blendAt(x, y, c, a*clamp01(0.5-rrDist(float64(x)+0.5, float64(y)+0.5, x0, y0, x1, y1, rad)))
		}
	}
}
