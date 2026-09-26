//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"strings"
	"time"

	"golang.org/x/image/vector"
)

// The red night clock: the time alone, dim red on black, in one of three looks - the ordinary clock
// face, an LED clock's seven segments, or a flip clock's split cards.

var (
	// nightRed is the lit red: dim, so with the backlight at the night light's level it reads across
	// a dark room without lighting it.
	nightRed = color.RGBA{150, 14, 0, 255}
	// nightGhost is an LED segment that is not lit, faint, as on a real LED clock.
	nightGhost = color.RGBA{26, 3, 0, 255}
	// nightCard is a flip card, and nightSplit the gap across its middle.
	nightCard  = color.RGBA{30, 8, 5, 255}
	nightSplit = color.RGBA{0, 0, 0, 255}
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
	g := max(t/5, 1) // the gap where two segments meet
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

// flipClock draws the time on two flip cards, hours and minutes, each split across its middle.
func (r *renderer) flipClock(now time.Time) {
	hm := strings.SplitN(clockHM(now), ":", 2)
	ampm := clockSuffix(now)
	ch := r.h * 62 / 100
	cw := max(r.width(r.clock, "88"), r.width(r.clock, hm[0]), r.width(r.clock, hm[1])) + r.s(44)
	space := r.s(28)
	x := (r.w - 2*cw - space) / 2
	top := (r.h - ch) / 2
	for i, part := range hm {
		card := image.Rect(x, top, x+cw, top+ch)
		r.roundRect(card, r.s(22), nightCard)
		// The digits sit in the middle of the card, the split running through them.
		tw := r.width(r.clock, part)
		r.text(r.clock, part, card.Min.X+(cw-tw)/2, card.Min.Y+ch/2+r.digitHeight()/2, nightRed)
		r.fillRect(image.Rect(card.Min.X, card.Min.Y+ch/2-r.s(2), card.Max.X, card.Min.Y+ch/2+r.s(2)), nightSplit)
		if i == 0 && ampm != "" {
			aw := r.width(r.small, ampm)
			r.text(r.small, ampm, card.Min.X+(cw-aw)/2, card.Max.Y+r.s(40), nightRed)
		}
		x += cw + space
	}
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
