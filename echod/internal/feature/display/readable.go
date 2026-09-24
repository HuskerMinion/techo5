//go:build !dot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"slices"
	"strings"

	"golang.org/x/image/font"
)

// Text over a photo. The wash laid over a slideshow picture calms it, but a wash light enough to
// leave a photo worth looking at leaves snow, sky or a white wall still bright, and the clock's pale
// text on it all but disappears. So on a photo the text looks after itself: behind each line, a soft
// patch of the ground color as dark as the picture there needs and no darker, and around the letters a
// thin dark edge for a picture that is light and dark in the same place. A dark photo is left almost
// as it was; a bright one gets dark just where the words are.
//
// Only small words get a patch: the weather, the date, the timers. The time and its AM/PM are large
// and bold enough to stand on the edge alone, and a patch the size of the clock is a smudge across
// the picture rather than a shadow under some words. Each line gets its own, hugging it.
//
// It is done in two passes. The first draws the page to a spare canvas, only to learn where its words
// go; the patches are laid under all of them; the second draws the page for real on top. One pass
// cannot do it, because a patch laid down for the date would darken the time drawn just above it.
// A page with no photo never comes here, so it draws exactly as it always has.

const (
	// scrimTarget is how bright the picture may be behind words, in 8-bit luma after the wash. Well
	// above where plain text would be comfortable: the dark edge round the letters carries the rest,
	// and on a real Show anything darker read as a dark blot rather than a shade.
	scrimTarget = 115.0

	// scrimMost is the darkest a patch gets, so even a white photo shows through it.
	scrimMost = 0.45

	// scrimTallest is the tallest line that gets a patch, as the face's height in the Show 5's pixels:
	// the title size (59) and below do, the AM/PM (70) and the time do not.
	scrimTallest = 64

	// A patch reaches scrimPad of its line's height past the words at full strength, then fades out
	// over scrimFeather of it. Its ends are rounded by scrimRound of its height, so it is a small
	// soft pill under the line and nothing more.
	scrimPad     = 0.35
	scrimFeather = 0.45
	scrimRound   = 0.5

	// haloAlpha is the dark edge around the letters: the ground color at this opacity, reaching a
	// haloReach-th of their height out (at least a pixel). Thin: the patch does the work, and the edge
	// is only for a picture light and dark in the same place.
	haloAlpha = 190
	haloReach = 110
)

// photoGold is the small lines' color over a photo, whatever it was: the theme's dim text is made for
// a plain dark ground, and on a picture it sinks into it. A warm gold stands out from nearly any photo
// and still sits with the cream time and the amber AM/PM.
var photoGold = color.RGBA{0xE8, 0xC0, 0x4E, 0xff}

// overPhoto is the state of drawing over a photo, kept on the paint between frames.
type overPhoto struct {
	photo  *image.RGBA // the picture behind the words being drawn, or nil
	ground color.RGBA  // the wash's color, which the patches and the halo are made of

	record bool              // the first pass: note where words go, draw none
	boxes  []image.Rectangle // where they went

	spare *image.RGBA // the first pass's canvas, kept between frames

	// shapes are the patches worked out for this picture, by where the words were: the clock redraws
	// every second and its words move once a minute, so working a patch out is rare and laying one
	// down is one blend. A new picture starts them over.
	shapes map[string]*patchShape
	shaped *image.RGBA

	// halos and falloffs are the parts that do not depend on the picture at all: each line's edge,
	// and how each patch fades. A fade between two pictures, a new picture every frame, reuses them.
	halos    map[haloKey]*image.Alpha
	falloffs map[falloffKey]*image.Alpha
}

// patchShape is how dark to make each pixel of area: 0 untouched, 255 the ground color.
type patchShape struct {
	area  image.Rectangle
	alpha []uint8
}

// readableOver draws page over photo, which has already been drawn and washed with ground at wash,
// with its words kept readable.
func (p *paint) readableOver(photo *image.RGBA, ground color.RGBA, wash uint8, page func()) {
	o := &p.over
	if photo == nil {
		page()
		return
	}
	// Pass one, on the spare canvas: where do the words go?
	if o.spare == nil || o.spare.Rect != p.dst.Rect {
		o.spare = image.NewRGBA(p.dst.Rect)
	}
	dst, zones := p.dst, len(p.pending)
	p.dst, o.record, o.boxes = o.spare, true, o.boxes[:0]
	page()
	p.dst, o.record = dst, false
	p.pending = p.pending[:zones] // the real pass adds its own tap zones

	o.photo, o.ground = photo, ground
	p.lay(p.shape(wash))
	page()
	o.photo = nil
}

// noteText is text for pass one: where it would be drawn.
func (o *overPhoto) noteText(face font.Face, s string, x, baseline int) {
	b, _ := font.BoundString(face, s)
	r := image.Rect(b.Min.X.Floor(), b.Min.Y.Floor(), b.Max.X.Ceil(), b.Max.Y.Ceil()).Add(image.Pt(x, baseline))
	if !r.Empty() {
		o.boxes = append(o.boxes, r)
	}
}

// note is a drawing that is not text but wants a patch under it too, like the weather's icon.
func (o *overPhoto) note(r image.Rectangle) {
	if o.record && !r.Empty() {
		o.boxes = append(o.boxes, r)
	}
}

// halo draws the dark edge around s, under where the text itself will go.
func (o *overPhoto) halo(dst *image.RGBA, face font.Face, s string, x, baseline int) {
	m := o.haloFor(face, s)
	shade := image.NewUniform(color.NRGBA{o.ground.R, o.ground.G, o.ground.B, haloAlpha})
	draw.DrawMask(dst, m.Rect.Add(image.Pt(x, baseline)), shade, image.Point{}, m, m.Rect.Min, draw.Over)
}

// haloKey is one line's edge: the same words in the same face have the same edge wherever they go.
type haloKey struct {
	face font.Face
	s    string
}

// haloFor is the edge around s: its letters grown by a haloReach-th of the face's height (at least a
// pixel) in every direction, as a mask placed from the text's origin. Kept, since the same few lines
// are drawn every second and change once a minute.
func (o *overPhoto) haloFor(face font.Face, s string) *image.Alpha {
	k := haloKey{face, s}
	if m, ok := o.halos[k]; ok {
		return m
	}
	if len(o.halos) > 32 {
		clear(o.halos)
	}
	if o.halos == nil {
		o.halos = map[haloKey]*image.Alpha{}
	}
	reach := max(1, face.Metrics().Height.Round()/haloReach)
	b, _ := font.BoundString(face, s)
	r := image.Rect(b.Min.X.Floor(), b.Min.Y.Floor(), b.Max.X.Ceil(), b.Max.Y.Ceil()).Inset(-reach)
	glyphs := image.NewAlpha(r)
	(&font.Drawer{Dst: glyphs, Src: image.Opaque, Face: face}).DrawString(s)
	grown := image.NewAlpha(r)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			var most uint8
			for dy := -reach; dy <= reach; dy += reach {
				for dx := -reach; dx <= reach; dx += reach {
					if v := glyphs.AlphaAt(x+dx, y+dy).A; v > most {
						most = v
					}
				}
			}
			grown.SetAlpha(x, y, color.Alpha{most})
		}
	}
	o.halos[k] = grown
	return grown
}

// shape is the patch for the words pass one found: under each group of them, as dark as the picture
// there needs.
func (p *paint) shape(wash uint8) *patchShape {
	o := &p.over
	groups := merged(o.boxes)
	var key strings.Builder
	fmt.Fprint(&key, wash, o.ground, p.dst.Rect, groups)
	if o.shaped != o.photo || len(o.shapes) > 16 {
		o.shapes, o.shaped = map[string]*patchShape{}, o.photo
	}
	if sh, ok := o.shapes[key.String()]; ok {
		return sh
	}

	w := float64(wash) / 255
	ground := luma(o.ground.R, o.ground.G, o.ground.B)
	sh := &patchShape{}
	type patch struct {
		core    image.Rectangle
		feather int
		a       float64
	}
	var patches []patch
	for _, b := range groups {
		core := b.Inset(-int(scrimPad*float64(b.Dy()) + 0.5))
		feather := max(2, int(scrimFeather*float64(b.Dy())+0.5))
		// The picture was drawn with its corner at the panel's; how bright it is under the words, as
		// the wash already left it, and how much more ground brings it down to scrimTarget.
		bright := brightness(o.photo, core.Add(o.photo.Rect.Min.Sub(p.dst.Rect.Min)))
		washed := bright*(1-w) + ground*w
		if washed <= scrimTarget {
			continue
		}
		patches = append(patches, patch{core, feather, min((washed-scrimTarget)/(washed-ground), scrimMost)})
		sh.area = sh.area.Union(core.Inset(-feather))
	}
	sh.area = sh.area.Intersect(p.dst.Rect)
	sh.alpha = make([]uint8, sh.area.Dx()*sh.area.Dy())
	for _, pt := range patches {
		k := int(pt.a*255 + 0.5)
		fall := o.falloffFor(pt.core, pt.feather)
		area := fall.Rect.Intersect(sh.area)
		for y := area.Min.Y; y < area.Max.Y; y++ {
			row := sh.alpha[(y-sh.area.Min.Y)*sh.area.Dx():]
			for x := area.Min.X; x < area.Max.X; x++ {
				f := int(fall.Pix[fall.PixOffset(x, y)])
				if v := uint8((f*k + 127) / 255); v > row[x-sh.area.Min.X] {
					row[x-sh.area.Min.X] = v
				}
			}
		}
	}
	o.shapes[key.String()] = sh
	return sh
}

// falloffKey is a patch's geometry, which is all its fade depends on.
type falloffKey struct {
	core    image.Rectangle
	feather int
}

// falloffFor is how a patch fades: 255 inside core with its corners rounded off, falling smoothly to
// nothing feather pixels beyond that. It depends only on where the words are, so a new picture, or each frame of a fade between two,
// reuses it and only its strength is worked out again.
func (o *overPhoto) falloffFor(core image.Rectangle, feather int) *image.Alpha {
	k := falloffKey{core, feather}
	if m, ok := o.falloffs[k]; ok {
		return m
	}
	if len(o.falloffs) > 16 {
		clear(o.falloffs)
	}
	if o.falloffs == nil {
		o.falloffs = map[falloffKey]*image.Alpha{}
	}
	// The distance from a rounded rectangle is the distance from the rectangle shrunk by the radius,
	// less the radius.
	round := int(scrimRound * float64(min(core.Dx(), core.Dy())))
	inner := image.Rect(core.Min.X+round, core.Min.Y+round, core.Max.X-1-round, core.Max.Y-1-round)
	m := image.NewAlpha(core.Inset(-feather))
	for y := m.Rect.Min.Y; y < m.Rect.Max.Y; y++ {
		dy := max(inner.Min.Y-y, y-inner.Max.Y, 0)
		for x := m.Rect.Min.X; x < m.Rect.Max.X; x++ {
			dx := max(inner.Min.X-x, x-inner.Max.X, 0)
			d := math.Hypot(float64(dx), float64(dy)) - float64(round)
			f := 1.0
			if d > 0 {
				t := 1 - d/float64(feather)
				if t <= 0 {
					continue
				}
				f = t * t * t * (t*(6*t-15) + 10) // smootherstep: no visible edge anywhere along it
			}
			m.Pix[m.PixOffset(x, y)] = uint8(f*255 + 0.5)
		}
	}
	o.falloffs[k] = m
	return m
}

// lay darkens the canvas toward the ground color by the shape.
func (p *paint) lay(sh *patchShape) {
	g := p.over.ground
	gr, gg, gb := int(g.R), int(g.G), int(g.B)
	for y := sh.area.Min.Y; y < sh.area.Max.Y; y++ {
		row := sh.alpha[(y-sh.area.Min.Y)*sh.area.Dx():][:sh.area.Dx()]
		i := p.dst.PixOffset(sh.area.Min.X, y)
		for _, a := range row {
			if a != 0 {
				px := p.dst.Pix[i : i+3 : i+3]
				k := int(a)
				px[0] = uint8((int(px[0])*(255-k) + gr*k + 127) / 255)
				px[1] = uint8((int(px[1])*(255-k) + gg*k + 127) / 255)
				px[2] = uint8((int(px[2])*(255-k) + gb*k + 127) / 255)
			}
			i += 4
		}
	}
}

// merged joins the pieces of one line into one box, until none are left to join: the weather's icon
// and its words, say. Two lines, one above the other, stay apart.
func merged(boxes []image.Rectangle) []image.Rectangle {
	out := slices.Clone(boxes)
	sameLine := func(a, b image.Rectangle) bool {
		shared := min(a.Max.Y, b.Max.Y) - max(a.Min.Y, b.Min.Y)
		gap := max(a.Min.X, b.Min.X) - min(a.Max.X, b.Max.X)
		return 2*shared >= min(a.Dy(), b.Dy()) && gap <= max(a.Dy(), b.Dy())
	}
	for joined := true; joined; {
		joined = false
		for i := 0; i < len(out) && !joined; i++ {
			for j := i + 1; j < len(out); j++ {
				if sameLine(out[i], out[j]) {
					out[i] = out[i].Union(out[j])
					out = slices.Delete(out, j, j+1)
					joined = true
					break
				}
			}
		}
	}
	return out
}

// brightness is how bright the picture is in r, as the brighter parts of it: the 85th percentile of
// its luma, sampled every few pixels. Not the average, which a white cloud in a blue sky pulls down
// while the words over the cloud still vanish.
func brightness(img *image.RGBA, r image.Rectangle) float64 {
	r = r.Intersect(img.Rect)
	if r.Empty() {
		return 0
	}
	step := max(2, min(r.Dx(), r.Dy())/24)
	var seen []float64
	for y := r.Min.Y; y < r.Max.Y; y += step {
		for x := r.Min.X; x < r.Max.X; x += step {
			i := img.PixOffset(x, y)
			seen = append(seen, luma(img.Pix[i], img.Pix[i+1], img.Pix[i+2]))
		}
	}
	slices.Sort(seen)
	return seen[len(seen)*85/100]
}

func luma(r, g, b uint8) float64 {
	return 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
}
