//go:build !dot

package display

import (
	"image"
	"image/color"
	"image/draw"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// The Stack and Overlap clock styles' figures, on the Show and the Spot alike. Both draw the time from
// the figures' shapes rather than as a line of text: the Stack sets the hours over the minutes, each
// figure in a slot of its own so the two lines stand as one block; the Overlap lays each figure a
// little over the one before it, see-through where the two meet, so there the colors mix as inks
// printed one over another do; the hours and the minutes stand apart, a colon's two dots between. A face's zero with a slash or a dot through it is drawn as its O,
// narrowed to the zero's size: at this size the slash is all one sees. A figure's shape is worked out
// once for its face and kept; a frame only lays the shapes down.

// overlapInk is how strongly an Overlap figure is laid over the one before it where they meet, of 255:
// the one under it still shows through. Elsewhere each is solid, so it reads whole on any ground.
const overlapInk = 165

// overlapOver is how far an Overlap figure reaches over the one before it, overlapApart the gap
// between the hours and the minutes, where the colon goes, and overlapDot the colon's dots' radius,
// each as a share of the figures' height.
const (
	overlapOver  = 0.2
	overlapApart = 0.3
	overlapDot   = 0.052
)

// figureShapes are a renderer's figures, kept from frame to frame: each one's shape in face, about its
// origin on the baseline, as it is first wanted; a new face starts them over.
type figureShapes struct {
	face  font.Face
	masks [10]*image.Alpha
}

// inkOf is where s's ink is in face, about its origin on the baseline. Read from the face's metrics,
// so a style can measure in a face it will not draw in.
func inkOf(face font.Face, s string) image.Rectangle {
	b, _ := font.BoundString(face, s)
	return image.Rect(b.Min.X.Floor(), b.Min.Y.Floor(), b.Max.X.Ceil(), b.Max.Y.Ceil())
}

// glyphMask is s drawn in face, about its origin on the baseline.
func glyphMask(face font.Face, s string) *image.Alpha {
	m := image.NewAlpha(inkOf(face, s))
	(&font.Drawer{Dst: m, Src: image.Opaque, Face: face, Dot: fixed.P(0, 0)}).DrawString(s)
	return m
}

// mask is figure's shape in face.
func (g *figureShapes) mask(face font.Face, figure int) *image.Alpha {
	if g.face != face {
		*g = figureShapes{face: face}
	}
	if g.masks[figure] == nil {
		m := glyphMask(face, string(rune('0'+figure)))
		if mid := m.Rect.Min.Add(m.Rect.Max).Div(2); figure == 0 && m.AlphaAt(mid.X, mid.Y).A > 96 {
			// Ink in the middle of the zero: a slash or a dot. The O instead, in the zero's place.
			o := glyphMask(face, "O")
			m = image.NewAlpha(m.Rect)
			xdraw.CatmullRom.Scale(m, m.Rect, o, o.Rect, xdraw.Src, nil)
		}
		g.masks[figure] = m
	}
	return g.masks[figure]
}

// figureHeight is how tall the figures stand in face: an 8's height.
func figureHeight(face font.Face) int { return -inkOf(face, "8").Min.Y }

// overlapRow is where each of the four places' figures (clockDigits: -1 is a place left empty) starts
// across in face for the Overlap, the left of its ink, counted from the left of the first one's; and
// how wide and how tall the figures are together.
func overlapRow(face font.Face, digits [4]int) (at [4]int, w, h int) {
	h = figureHeight(face)
	over, apart := int(float64(h)*overlapOver), int(float64(h)*overlapApart)
	x, first := 0, true
	for i, d := range digits {
		if d < 0 {
			continue
		}
		switch {
		case first:
		case i == 2:
			x += apart
		default:
			x -= over
		}
		at[i], first = x, false
		x += inkOf(face, string(rune('0'+d))).Dx()
		w = x
	}
	return at, w, h
}

// overlapFit is the face, of those faceAt makes, in which the Overlap's figures are as large as fit w
// by h: it measures them in ref, then takes the size that fills the box from what that came to.
func overlapFit(faceAt func(size int) font.Face, ref int, digits [4]int, w, h int) font.Face {
	f := faceAt(ref)
	_, rw, rh := overlapRow(f, digits)
	if rw <= 0 || rh <= 0 {
		return f
	}
	return faceAt(int(float64(ref) * min(float64(w)/float64(rw), float64(h)/float64(rh))))
}

// overlapClock lays the time's figures in face with the first one's ink at x and their baseline at
// baseline, the places in turn in a and b, the colon's dots in dots, and returns where they went. Over
// a photo the first pass only notes the place, for the patch under it.
func (p *paint) overlapClock(g *figureShapes, face font.Face, digits [4]int, x, baseline int, a, b, dots color.RGBA) image.Rectangle {
	at, w, h := overlapRow(face, digits)
	box := image.Rect(x, baseline-h, x+w, baseline)
	if p.over.record {
		p.over.note(box)
		return box
	}
	var under *image.Alpha // the figure before, where it went
	underAt := image.Point{}
	for i, d := range digits {
		if d < 0 {
			continue
		}
		c := a
		if i%2 == 1 {
			c = b
		}
		m := g.mask(face, d)
		origin := image.Pt(x+at[i]-m.Rect.Min.X, baseline)
		p.overlapLay(m, origin, c, under, underAt)
		under, underAt = m, origin
		if i == 1 {
			// The colon, in the middle of the gap before the minutes.
			cx := float64(x+at[2]) - float64(h)*overlapApart/2
			for _, up := range []float64{0.3, 0.7} {
				p.fxDisc(cx, float64(baseline)-float64(h)*up, float64(h)*overlapDot, dots, 1)
			}
		}
	}
	return box
}

// overlapLay lays m, its origin at origin, in c: solid, except over under (the figure before, its
// origin at underAt), where it is laid at overlapInk so that one shows through. Pixel by pixel, by the
// two shapes' own coverage: only where both have ink do they mix.
func (p *paint) overlapLay(m *image.Alpha, origin image.Point, c color.RGBA, under *image.Alpha, underAt image.Point) {
	r := m.Rect.Add(origin).Intersect(p.dst.Rect)
	ink := [3]uint32{uint32(c.R), uint32(c.G), uint32(c.B)}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		mi, di := m.PixOffset(r.Min.X-origin.X, y-origin.Y), p.dst.PixOffset(r.Min.X, y)
		for i := range r.Dx() {
			a := uint32(m.Pix[mi+i])
			if a == 0 {
				continue
			}
			if under != nil {
				if q := image.Pt(r.Min.X+i, y).Sub(underAt); q.In(under.Rect) {
					a = a * (255 - (255-overlapInk)*uint32(under.Pix[under.PixOffset(q.X, q.Y)])/255) / 255
				}
			}
			px := p.dst.Pix[di+4*i : di+4*i+3 : di+4*i+3]
			for k, v := range ink {
				px[k] = uint8((uint32(px[k])*(255-a) + v*a + 127) / 255)
			}
		}
	}
}

// stackSlots is the width of each of the Stack's figures' slots in face, the gap between the two, and
// how tall the figures stand.
func stackSlots(face font.Face) (slot, gap, h int) {
	h = figureHeight(face)
	return inkOf(face, "0").Dx(), h / 10, h
}

// stackLine lays two of the Stack's figures in face and c, their baseline at baseline, centered on
// mid: each in a slot as wide as a 0, so the hours and the minutes make lines of one width whatever
// the figures. It returns where the line went. Over a photo the first pass only notes the place.
func (p *paint) stackLine(g *figureShapes, face font.Face, figures [2]int, mid, baseline int, c color.Color) image.Rectangle {
	slot, gap, h := stackSlots(face)
	x := mid - slot - gap/2
	line := image.Rect(x, baseline-h, x+2*slot+gap, baseline)
	if p.over.record {
		p.over.note(line)
		return line
	}
	ink := image.NewUniform(c)
	for _, f := range figures {
		m := g.mask(face, f)
		at := x + (slot-m.Rect.Dx())/2 - m.Rect.Min.X
		draw.DrawMask(p.dst, m.Rect.Add(image.Pt(at, baseline)), ink, image.Point{}, m, m.Rect.Min, draw.Over)
		x += slot + gap
	}
	return line
}
