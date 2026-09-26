//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// The night clock: the time alone on black, in one of three looks - the ordinary clock face in dim
// red, an LED clock's seven red segments, or a flip clock's charcoal cards and off-white digits.

var (
	// nightRed is the lit red: dim, so with the backlight at the night light's level it reads across
	// a dark room without lighting it.
	nightRed = color.RGBA{150, 14, 0, 255}
	// nightGhost is an LED segment that is not lit, faint, as on a real LED clock.
	nightGhost = color.RGBA{26, 3, 0, 255}
	// The flip clock is not red: charcoal cards, a shade lighter above the split than below it, the
	// hinges at their sides, and dim off-white digits.
	flipTop   = color.RGBA{36, 36, 35, 255}
	flipLower = color.RGBA{27, 27, 26, 255}
	flipHinge = color.RGBA{52, 52, 50, 255}
	flipInk   = color.RGBA{158, 152, 140, 255}
	flipSplit = color.RGBA{0, 0, 0, 255}
)

// redClockPage is the night as a clock alone.
func (r *renderer) redClockPage(s scene) {
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(color.Black), image.Point{}, draw.Src)
	switch s.redStyle {
	case nightStyleLED:
		r.ledClock(s.now)
	case nightStyleFlip:
		r.flipClock(s.now)
	default:
		r.plainRedClock(s.now)
	}
}

func (r *renderer) plainRedClock(now time.Time) {
	hour, ampm := clockHM(now), clockSuffix(now)
	hw, aw := r.width(r.clock, hour), r.width(r.ampm, ampm)
	gap := r.s(18)
	if ampm == "" {
		gap = 0
	}
	x := (r.w - hw - gap - aw) / 2
	base := r.h/2 + r.digitHeight()/2 // the digits centered top to bottom
	r.text(r.clock, hour, x, base, nightRed)
	r.text(r.ampm, ampm, x+hw+gap, base, nightRed)
}

// clockDigits is the time as four digit places, hours then minutes; a 12-hour clock leaves the first
// place empty (-1) before ten, as an LED or a flip clock does.
func clockDigits(now time.Time) [4]int {
	hm := strings.SplitN(clockHM(now), ":", 2)
	h := hm[0]
	if len(h) == 1 {
		h = " " + h
	}
	d := [4]int{-1, int(h[1] - '0'), int(hm[1][0] - '0'), int(hm[1][1] - '0')}
	if h[0] != ' ' {
		d[0] = int(h[0] - '0')
	}
	return d
}

// segments are the seven segments each digit lights: a top, b top right, c bottom right, d bottom,
// e bottom left, f top left, g middle.
var segments = [10]string{"abcdef", "bc", "abdeg", "abcdg", "bcfg", "acdfg", "acdefg", "abc", "abcdefg", "abcdfg"}

// ledClock draws the time as seven-segment digits, the unlit segments faintly behind.
func (r *renderer) ledClock(now time.Time) {
	ampm := clockSuffix(now)
	h := r.h * 52 / 100
	w := h * 54 / 100
	t := h * 12 / 100
	space := w * 30 / 100 // between digits
	colon := t * 3        // the colon's width
	aw := 0
	if ampm != "" {
		aw = r.width(r.ampm, ampm) + space
	}
	total := 4*w + 3*space + colon + aw
	if limit := r.w * 90 / 100; total > limit {
		// A narrower screen: everything shrinks together.
		scale := float64(limit-aw) / float64(total-aw)
		h, w, t = int(float64(h)*scale), int(float64(w)*scale), int(float64(t)*scale)
		space, colon = int(float64(space)*scale), t*3
		total = 4*w + 3*space + colon + aw
	}
	x := (r.w - total) / 2
	top := (r.h - h) / 2
	for i, dg := range clockDigits(now) {
		r.segmentDigit(x, top, w, h, t, dg)
		x += w + space
		if i == 1 {
			// The colon between hours and minutes.
			cx := x - space + (colon+space)/2 - t/2
			r.fillRect(image.Rect(cx, top+h*30/100, cx+t, top+h*30/100+t), nightRed)
			r.fillRect(image.Rect(cx, top+h*62/100, cx+t, top+h*62/100+t), nightRed)
			x += colon
		}
	}
	if ampm != "" {
		r.text(r.ampm, ampm, x, top+h, nightRed)
	}
}

// segmentDigit draws one digit's seven segments at (x, top), w by h, each t thick: those the digit
// lights in red, the others as ghosts. A digit of -1 is all ghosts.
func (r *renderer) segmentDigit(x, top, w, h, t, digit int) {
	lit := ""
	if digit >= 0 && digit <= 9 {
		lit = segments[digit]
	}
	g := max(t*2/5, 2) // the gap where two segments meet: wide, so each segment stands apart as on an old LED
	mid := top + h/2
	type seg struct {
		name           byte
		x0, y0, x1, y1 int // the segment's centerline
	}
	for _, s := range []seg{
		{'a', x + g, top + t/2, x + w - g, top + t/2},
		{'d', x + g, top + h - t/2, x + w - g, top + h - t/2},
		{'g', x + g, mid, x + w - g, mid},
		{'f', x + t/2, top + g, x + t/2, mid - g},
		{'b', x + w - t/2, top + g, x + w - t/2, mid - g},
		{'e', x + t/2, mid + g, x + t/2, top + h - g},
		{'c', x + w - t/2, mid + g, x + w - t/2, top + h - g},
	} {
		c := nightGhost
		if strings.IndexByte(lit, s.name) >= 0 {
			c = nightRed
		}
		r.hexSegment(s.x0, s.y0, s.x1, s.y1, t, c)
	}
}

// hexSegment fills a segment with pointed ends along a horizontal or vertical centerline.
func (r *renderer) hexSegment(x0, y0, x1, y1, t int, c color.Color) {
	k := t / 2
	var box image.Rectangle
	if y0 == y1 {
		box = image.Rect(x0, y0-k, x1, y0+k)
	} else {
		box = image.Rect(x0-k, y0, x0+k, y1)
	}
	r.fillShape(box, c, func(z *vector.Rasterizer, w, h float32) {
		if y0 == y1 { // horizontal: pointed at the left and right ends
			z.MoveTo(0, h/2)
			z.LineTo(h/2, 0)
			z.LineTo(w-h/2, 0)
			z.LineTo(w, h/2)
			z.LineTo(w-h/2, h)
			z.LineTo(h/2, h)
		} else { // vertical: pointed at the top and bottom
			z.MoveTo(w/2, 0)
			z.LineTo(w, w/2)
			z.LineTo(w, h-w/2)
			z.LineTo(w/2, h)
			z.LineTo(0, h-w/2)
			z.LineTo(0, w/2)
		}
	})
}

// flipClock draws the time as a flip clock does: hours and minutes on two cards, each split across its
// middle, with tall narrow digits.
func (r *renderer) flipClock(now time.Time) {
	hm := strings.SplitN(clockHM(now), ":", 2)
	ampm := clockSuffix(now)
	ch := r.h * 66 / 100
	cw := ch * 88 / 100
	space := r.s(30)
	x := (r.w - 2*cw - space) / 2
	top := (r.h-ch)/2 - r.s(10)
	mid := top + ch/2
	k := r.s(18)
	for i, part := range hm {
		card := image.Rect(x, top, x+cw, top+ch)
		// The lower leaf, then the upper over it; each is rounded all round, which leaves the small
		// notches at the split that a real card has.
		r.roundRect(card, k, flipLower)
		r.roundRect(image.Rect(card.Min.X, card.Min.Y, card.Max.X, mid), k, flipTop)
		r.flipDigits(part, image.Rect(card.Min.X, card.Min.Y+ch*12/100, card.Max.X, card.Max.Y-ch*12/100))
		// The split, and the hinges that stand out from the card's sides.
		r.fillRect(image.Rect(card.Min.X, mid-r.s(2), card.Max.X, mid+r.s(2)), flipSplit)
		hw, hh := r.s(8), r.s(26)
		r.roundRect(image.Rect(card.Min.X-hw, mid-hh/2, card.Min.X+r.s(2), mid+hh/2), r.s(3), flipHinge)
		r.roundRect(image.Rect(card.Max.X-r.s(2), mid-hh/2, card.Max.X+hw, mid+hh/2), r.s(3), flipHinge)
		if i == 0 && ampm != "" {
			aw := r.width(r.small, ampm)
			r.text(r.small, ampm, card.Min.X+(cw-aw)/2, card.Max.Y+r.s(40), flipInk)
		}
		x += cw + space
	}
}

// flipDigits draws s in box the way flip-clock numbers look: each digit on its own, in equal slots as
// on a flip clock's leaves, the clock face's bold figures stretched tall and narrow to fill the box's
// height. Zero is the face's round capital O, stretched to the oval a flip clock has, since the face's
// own zero carries a slash.
func (r *renderer) flipDigits(s string, box image.Rectangle) {
	slot := box.Dx() * 42 / 100
	x := box.Min.X + (box.Dx()-slot*len(s))/2
	for _, c := range s {
		if c == '0' {
			c = 'O'
		}
		r.flipGlyph(c, image.Rect(x, box.Min.Y, x+slot, box.Max.Y))
		x += slot
	}
}

// flipGlyph draws one figure stretched into box, centered across it.
func (r *renderer) flipGlyph(c rune, box image.Rectangle) {
	b, _ := font.BoundString(r.clock, string(c))
	gw, gh := (b.Max.X - b.Min.X).Ceil(), (b.Max.Y - b.Min.Y).Ceil()
	if gw <= 0 || gh <= 0 {
		return
	}
	glyph := image.NewRGBA(image.Rect(0, 0, gw, gh))
	(&font.Drawer{Dst: glyph, Src: image.NewUniform(flipInk), Face: r.clock,
		Dot: fixed.Point26_6{X: -b.Min.X, Y: -b.Min.Y}}).DrawString(string(c))
	h := box.Dy()
	w := min(gw*h/gh*72/100, box.Dx()*88/100) // tall and narrow, as a flip clock's figures are
	if c == '1' {
		w = min(w, box.Dx()*46/100) // a one keeps its own slimness
	}
	x := box.Min.X + (box.Dx()-w)/2
	xdraw.BiLinear.Scale(r.dst, image.Rect(x, box.Min.Y, x+w, box.Max.Y), glyph, glyph.Bounds(), xdraw.Over, nil)
}

// digitHeight is how tall the clock face's digits stand above the baseline.
func (r *renderer) digitHeight() int {
	if b, _, ok := r.clock.GlyphBounds('8'); ok {
		return (-b.Min.Y).Ceil()
	}
	return r.clock.Metrics().Ascent.Ceil() * 7 / 10
}

func (r *renderer) fillRect(b image.Rectangle, c color.Color) {
	draw.Draw(r.dst, b, image.NewUniform(c), image.Point{}, draw.Over)
}

// roundRect fills b with its corners rounded to radius k.
func (r *renderer) roundRect(b image.Rectangle, k int, c color.Color) {
	r.fillShape(b, c, func(z *vector.Rasterizer, w, h float32) {
		q := float32(k)
		z.MoveTo(q, 0)
		z.LineTo(w-q, 0)
		z.QuadTo(w, 0, w, q)
		z.LineTo(w, h-q)
		z.QuadTo(w, h, w-q, h)
		z.LineTo(q, h)
		z.QuadTo(0, h, 0, h-q)
		z.LineTo(0, q)
		z.QuadTo(0, 0, q, 0)
	})
}

// fillShape fills the path drawn by path, in coordinates within box (w by h), in c, antialiased. The
// rasterizer is only as big as the box.
func (r *renderer) fillShape(box image.Rectangle, c color.Color, path func(z *vector.Rasterizer, w, h float32)) {
	if box.Empty() {
		return
	}
	z := vector.NewRasterizer(box.Dx(), box.Dy())
	path(z, float32(box.Dx()), float32(box.Dy()))
	z.ClosePath()
	z.Draw(r.dst, box, image.NewUniform(c), image.Point{})
}
