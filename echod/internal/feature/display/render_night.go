//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"math"
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

// flipClock draws the time as a flip clock does, across the whole screen: a card for each digit, the
// hours and the minutes apart, and on a 12-hour clock a smaller card for AM or PM at the end. A 12-hour
// clock leaves the first card blank before ten. A card whose figure has just changed flips to it: the
// old top leaf folds down over the split, and its back comes down with the new figure's lower half.
func (r *renderer) flipClock(now time.Time) {
	digits := clockDigits(now)
	ampm := clockSuffix(now)
	var texts [5]string
	for i, dg := range digits {
		if dg >= 0 {
			texts[i] = string(rune('0' + dg))
		}
	}
	texts[4] = ampm
	r.flip.update(texts, now)

	gap, apart := r.s(14), r.s(44) // between the cards of a pair, and between the pairs
	avail := r.w * 94 / 100
	// Card widths: four digits, and on a 12-hour clock the smaller, skinnier AM/PM card.
	units, gaps := 4.0, 2*gap+apart
	if ampm != "" {
		units, gaps = 4.62, 2*gap+2*apart
	}
	w := int(float64(avail-gaps) / units)
	// As tall as the screen allows, up to about twice as tall as wide: taller than that, the figures
	// stop looking like a flip clock's and start looking stretched.
	h := min(w*21/10, r.h*90/100)
	x := (r.w - (int(units*float64(w)) + gaps)) / 2
	top := (r.h - h) / 2
	for i := range digits {
		r.flipCard(image.Rect(x, top, x+w, top+h), i, 12, now)
		x += w
		switch i {
		case 1:
			x += apart
		case 0, 2:
			x += gap
		}
	}
	if ampm != "" {
		// Smaller: three fifths the width of a digit card and a little over half its height, on the
		// same split line.
		aw, ah := w*62/100, h*56/100
		at := top + (h-ah)/2
		x += apart
		r.flipCard(image.Rect(x, at, x+aw, at+ah), 4, 30, now) // the letters shorter, so not pinched
	}
}

// flipFor is how long a card takes to flip.
const flipFor = 560 * time.Millisecond

// flipState is what each of the five cards shows, and the flip under way on those that just changed.
type flipState struct {
	shown, from [5]string
	at          time.Time // when the cards last changed
	drawn       time.Time // when the flip clock was last drawn
}

// update takes the cards' new texts. A card that changed flips from what it showed, unless the flip
// clock has not been on the screen a moment ago: coming back to it, the cards are simply as they are.
func (f *flipState) update(texts [5]string, now time.Time) {
	fresh := f.drawn.IsZero() || now.Sub(f.drawn) > 3*time.Second
	f.drawn = now
	if texts == f.shown {
		return
	}
	if fresh {
		f.shown, f.from, f.at = texts, texts, time.Time{}
		return
	}
	f.from, f.shown, f.at = f.shown, texts, now
}

// busy is whether a card is flipping, and the clock wants frames quickly.
func (f *flipState) busy(now time.Time) bool {
	return !f.at.IsZero() && now.Sub(f.at) < flipFor && now.Sub(f.drawn) < time.Second
}

// flipBusy is whether the flip clock is in the middle of a flip.
func (r *renderer) flipBusy(now time.Time) bool { return r.flip.busy(now) }

// flipCard draws card i: settled, or part way through its flip.
func (r *renderer) flipCard(card image.Rectangle, i, inset int, now time.Time) {
	shown, from := r.flip.shown[i], r.flip.from[i]
	p := 1.0
	if !r.flip.at.IsZero() {
		p = min(float64(now.Sub(r.flip.at))/float64(flipFor), 1)
	}
	if from == shown || p >= 1 {
		r.flipFace(card, shown, inset)
	} else {
		r.flipTurning(card, from, shown, inset, p)
	}
	mid := card.Min.Y + card.Dy()/2
	r.fillRect(image.Rect(card.Min.X, mid-r.s(2), card.Max.X, mid+r.s(2)), flipSplit)
	hw, hh := r.s(6), card.Dy()/9
	r.roundRect(image.Rect(card.Min.X-hw, mid-hh/2, card.Min.X+r.s(2), mid+hh/2), r.s(3), flipHinge)
	r.roundRect(image.Rect(card.Max.X-r.s(2), mid-hh/2, card.Max.X+hw, mid+hh/2), r.s(3), flipHinge)
}

// flipFace draws a card's two leaves and text on them, set in from the top and bottom by inset percent
// of its height. Empty text is a blank card.
func (r *renderer) flipFace(card image.Rectangle, text string, inset int) {
	mid := card.Min.Y + card.Dy()/2
	k := card.Dx() / 9
	// The lower leaf, then the upper over it; each is rounded all round, which leaves the small
	// notches at the split that a real card has.
	r.roundRect(card, k, flipLower)
	r.roundRect(image.Rect(card.Min.X, card.Min.Y, card.Max.X, mid), k, flipTop)
	if text != "" {
		in := card.Dy() * inset / 100
		r.flipDigits(text, image.Rect(card.Min.X, card.Min.Y+in, card.Max.X, card.Max.Y-in))
	}
}

// flipTurning draws a card p of the way through flipping from one text to the next. Behind the moving
// leaf, the new text's top is already there above the split and the old text's bottom still below it.
// In the first half the old top leaf folds down toward the split, darkening as it turns away; in the
// second its back, which carries the new text's lower half, comes down over the old bottom.
func (r *renderer) flipTurning(card image.Rectangle, from, to string, inset int, p float64) {
	old := r.offscreen(card, func() { r.flipFace(card, from, inset) })
	next := r.offscreen(card, func() { r.flipFace(card, to, inset) })
	mid := card.Min.Y + card.Dy()/2
	upper := image.Rect(card.Min.X, card.Min.Y, card.Max.X, mid)
	lower := image.Rect(card.Min.X, mid, card.Max.X, card.Max.Y)
	draw.Draw(r.dst, upper, next, upper.Min, draw.Over)
	draw.Draw(r.dst, lower, old, lower.Min, draw.Over)
	if p < 0.5 {
		fold := math.Cos(p * math.Pi) // 1 standing up, 0 edge on
		leaf := image.Rect(upper.Min.X, mid-int(fold*float64(upper.Dy())), upper.Max.X, mid)
		if !leaf.Empty() {
			xdraw.BiLinear.Scale(r.dst, leaf, old, upper, xdraw.Over, nil)
			r.shade(leaf, 1-fold)
		}
		return
	}
	fold := -math.Cos(p * math.Pi) // 0 edge on, 1 lying flat on the lower leaf
	leaf := image.Rect(lower.Min.X, mid, lower.Max.X, mid+int(fold*float64(lower.Dy())))
	if !leaf.Empty() {
		xdraw.BiLinear.Scale(r.dst, leaf, next, lower, xdraw.Over, nil)
		r.shade(leaf, 1-fold)
	}
}

// shade darkens b by a fraction up to three quarters: a leaf turned away from the light.
func (r *renderer) shade(b image.Rectangle, by float64) {
	a := uint8(math.Round(min(max(by, 0), 1) * 190))
	draw.Draw(r.dst, b, image.NewUniform(color.RGBA{0, 0, 0, a}), image.Point{}, draw.Over)
}

// offscreen is what paint draws, drawn on black into a picture of just b, not onto the screen.
func (r *renderer) offscreen(b image.Rectangle, paint func()) *image.RGBA {
	img := image.NewRGBA(b)
	draw.Draw(img, b, image.NewUniform(color.Black), image.Point{}, draw.Src)
	screen := r.dst
	r.dst = img
	paint()
	r.dst = screen
	return img
}

// flipDigits draws s in box the way flip-clock figures look: each character in its own equal share of
// the box, the clock face's bold figures stretched tall and narrow to fill the box's height. Zero is
// the face's round capital O, stretched to the oval a flip clock has, since the face's own zero
// carries a slash.
func (r *renderer) flipDigits(s string, box image.Rectangle) {
	n := len(s)
	slot := box.Dx() * 84 / 100 / n
	x := box.Min.X + (box.Dx()-slot*n)/2
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
	w := min(gw*h/gh*72/100, box.Dx()*92/100) // tall and narrow, as a flip clock's figures are
	if c == '1' {
		w = min(w, box.Dx()*50/100) // a one keeps its own slimness
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
