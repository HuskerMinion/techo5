package rainbow

import (
	"image"
	"image/color"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// The card: frosted glass in the middle of the screen, a little above it, with the words on it. It
// comes in from a little smaller and lower with a bounce, and finds its height on a spring as the
// words change. While the device listens and thinks it says what was heard, with three dots pulsing
// while it thinks; once it answers, what was heard is its title and the answer is under it, the one
// going up and out as the other comes up in. It goes away shrinking a little, upward. The glass is the
// page under it blurred, brightened and tinted, lit a little along its top, with a fine rim.

// The card's size and timing, in design units and seconds.
const (
	cardW      = 440.0 // wide enough for most questions on a line or two
	cardTop    = 58.0  // its top above the screen's middle
	cardRadius = 22.0
	cardPad    = 20.0 // the words' margin each side
	cardDots   = 56.0 // room kept at the right for the thinking dots
	cardMin    = 64.0 // the height of a card of one line
	cardLines  = 4    // the most lines an answer takes
	cardHeard  = 3    // and what was heard, over it or alone: the whole question, not one line of it
	frostSigma = 14.0
	cardIn     = 0.55
	cardOut    = 0.32
)

// The words' colors: what was heard, the title over an answer, and the answer.
var (
	heardInk = color.RGBA{217, 217, 217, 217} // white at 0.85, premultiplied
	titleInk = color.RGBA{255, 214, 10, 255}
	replyInk = color.RGBA{255, 255, 255, 255}
	glassInk = [3]float32{40, 46, 80}
)

// cardWords is one set of words on the card, drawn once at full size: what it says (to tell when it
// changes), whether it is an answer, and how tall the card wants to be for it, in design units.
type cardWords struct {
	key    string
	reply  bool
	img    *image.RGBA
	height float64
}

// card is the card's state between frames.
type card struct {
	shown        bool
	inAt, outAt  time.Time
	h, vh        float64 // its height, in design units, and how fast that is changing
	cur, prev    *cardWords
	swapAt       time.Time
	thinking     bool
	at           image.Rectangle // where it was last drawn
	alpha, scale float64         // for the frame in hand
	oy           float64

	frostAt   image.Rectangle // what of the screen frost was made from, in pixels
	frostSum  uint32          // a sparse sum over it, to tell when the page under it changes
	frostSame bool            // the page under it is as it was last frame
	frostImg  []uint8         // the frost, a quarter of that each way, RGBA
	fw, fh    int

	glass []uint32 // the frost spread to the screen's pixels, as the glass is, over frostAt

	look cardLook // what img was painted for
	img  []uint32 // the card's picture, premultiplied, a word to a pixel, the size of at on the screen
}

// up is whether the card is on the screen, coming or going.
func (c *card) up() bool { return c.shown || c.outAt.After(c.inAt) && c.alpha > 0 }

// moving is whether the card is still coming, going, or finding its height.
func (c *card) moving() bool {
	if !c.up() {
		return false
	}
	if c.cur != nil && (math.Abs(c.cur.height-c.h) > 0.3 || math.Abs(c.vh) > 1) {
		return true
	}
	return !c.shown || c.alpha < 1 || c.scale != 1 || c.prev != nil || c.thinking
}

// update moves the card on to now for turn t, the light being far enough on for it when want, and
// says what of the screen its glass needs frosted; empty when it is not up.
func (c *card) update(g *geometry, now time.Time, dt float64, t Turn, want bool, f Faces) image.Rectangle {
	words, reply := t.Heard, false
	if (t.Phase == "replying" || t.Phase == "lingering") && t.Reply != "" {
		reply = true
	}
	has := t.Heard != "" || t.Phase == "thinking" || reply
	wanted := has && (want || (t.Phase == "lingering" && c.shown))
	key := words
	if reply {
		key = t.Heard + "\x00" + t.Reply
	}
	c.thinking = t.Phase == "thinking" && !reply

	switch {
	case wanted && !c.shown:
		c.shown, c.inAt, c.h, c.vh, c.prev = true, now, 58, 0, nil
		c.cur = newCardWords(g, f, t.Heard, t.Reply, reply, key)
	case !wanted && c.shown:
		c.shown, c.outAt = false, now
	case c.shown && c.cur.key != key:
		w := newCardWords(g, f, t.Heard, t.Reply, reply, key)
		if w.reply != c.cur.reply {
			c.prev, c.swapAt = c.cur, now
		}
		c.cur = w
	}
	if c.cur == nil {
		c.at = image.Rectangle{}
		return image.Rectangle{}
	}

	// The height on a spring, in steps short enough for it to hold at any frame rate.
	for left := dt; left > 0; left -= 1.0 / 120 {
		st := min(left, 1.0/120)
		acc := 260*(c.cur.height-c.h) - 24*c.vh
		c.vh += acc * st
		c.h += c.vh * st
	}
	if c.shown {
		p := clamp01(now.Sub(c.inAt).Seconds() / cardIn)
		c.alpha, c.scale, c.oy = smooth(0, 0.5, p), 0.86+0.14*backOut(p), 16*(1-smooth(0, 1, p))
		if p >= 1 {
			c.scale = 1
		}
	} else {
		p := clamp01(now.Sub(c.outAt).Seconds() / cardOut)
		if p >= 1 {
			c.alpha, c.cur, c.prev, c.at = 0, nil, nil, image.Rectangle{}
			return image.Rectangle{}
		}
		c.alpha, c.scale, c.oy = 1-smooth(0, 1, p), 1-0.06*smooth(0, 1, p), -6*smooth(0, 1, p)
	}
	x0, y0, x1, y1 := c.box(g)
	m := frostSigma * 2.5 * g.k
	return image.Rect(int(x0-m), int(y0-m), int(x1+m)+1, int(y1+m)+1).Intersect(image.Rect(0, 0, g.w, g.h))
}

// box is the card's rectangle on the screen for the frame in hand, in pixels, scaled.
func (c *card) box(g *geometry) (x0, y0, x1, y1 float64) {
	k := g.k
	w, h := cardW*k, c.h*k
	top := (g.hd/2 - cardTop + c.oy) * k
	cx, cy := float64(g.w)/2, top+h/2
	return cx - w/2*c.scale, cy - h/2*c.scale, cx + w/2*c.scale, cy + h/2*c.scale
}

// newCardWords lays out the words for the card: what was heard, up to two lines; or for an answer,
// what was heard on one line as the title and the answer under it.
func newCardWords(g *geometry, f Faces, heard, reply string, isReply bool, key string) *cardWords {
	k := g.k
	w := int(cardW * k)
	pad := cardPad * k
	lineOf := func(face font.Face) float64 { return float64(face.Metrics().Height.Ceil()) }
	type line struct {
		face font.Face
		text string
		ink  color.RGBA
	}
	heard = capital(heard) // what speech recognition hears comes in lower case
	var lines []line
	title := 0 // the title's lines, over an answer
	if !isReply {
		for _, l := range wrapLines(f.Heard, heard, int(float64(w)-2*pad-cardDots*k), cardHeard) {
			lines = append(lines, line{f.Heard, l, heardInk})
		}
	} else {
		if heard != "" {
			for _, l := range wrapLines(f.Heard, heard, int(float64(w)-2*pad), cardHeard) {
				lines = append(lines, line{f.Heard, l, titleInk})
			}
			title = len(lines)
		}
		for _, l := range wrapLines(f.Reply, reply, int(float64(w)-2*pad), cardLines) {
			lines = append(lines, line{f.Reply, l, replyInk})
		}
	}
	text := 0.0
	for _, l := range lines {
		text += lineOf(l.face)
	}
	// One line sits in the middle of a card cardMin tall; more take more.
	edge := max(14*k, (cardMin*k-lineOf(f.Heard))/2)
	gap := 0.0
	if isReply && heard != "" {
		gap = 6 * k
	}
	hgt := max(cardMin*k, 2*edge+text+gap)
	img := image.NewRGBA(image.Rect(0, 0, w, int(math.Ceil(hgt))))
	y := edge
	for i, l := range lines {
		y += float64(l.face.Metrics().Ascent.Ceil())
		d := font.Drawer{Dst: img, Src: image.NewUniform(l.ink), Face: l.face, Dot: fixed.P(int(pad), int(y))}
		d.DrawString(l.text)
		y += lineOf(l.face) - float64(l.face.Metrics().Ascent.Ceil())
		if i == title-1 {
			y += gap
		}
	}
	return &cardWords{key: key, reply: isReply, img: img, height: hgt / k}
}

// capital is s with its first letter a capital, accented or not.
func capital(s string) string {
	for i, r := range s {
		if unicode.IsLetter(r) {
			return s[:i] + string(unicode.ToUpper(r)) + s[i+utf8.RuneLen(r):]
		}
		if !unicode.IsSpace(r) && !unicode.IsPunct(r) {
			return s // a number first: nothing to make a capital
		}
	}
	return s
}

// wrapLines breaks text into at most n lines no wider than w, on spaces, the last cut short with an
// ellipsis when there is more.
func wrapLines(face font.Face, text string, w, n int) []string {
	var out []string
	line := ""
	words := strings.Fields(text)
	for i, word := range words {
		try := word
		if line != "" {
			try = line + " " + word
		}
		if line != "" && font.MeasureString(face, try).Ceil() > w {
			out = append(out, line)
			if len(out) == n {
				out[n-1] = clipLine(face, out[n-1]+" "+strings.Join(words[i:], " "), w)
				return out
			}
			line = word
			continue
		}
		line = try
	}
	if line != "" {
		out = append(out, clipLine(face, line, w))
	}
	return out
}

// clipLine is text cut to fit w, ending in an ellipsis when it was cut.
func clipLine(face font.Face, text string, w int) string {
	if font.MeasureString(face, text).Ceil() <= w {
		return text
	}
	r := []rune(text)
	for len(r) > 0 && font.MeasureString(face, string(r)+"…").Ceil() > w {
		r = r[:len(r)-1]
	}
	return strings.TrimRight(string(r), " ") + "…"
}

// frost makes the glass from the page on dst in at: shrunk to a quarter, blurred, kept for draw. A
// page that has not changed under the card since the last frame, as most do not, keeps the glass it
// had: a sparse look over it tells.
func (c *card) frost(dst *image.RGBA, g *geometry, at image.Rectangle) {
	var sum uint32
	for y := at.Min.Y + 1; y < at.Max.Y; y += 4 {
		row := dst.Pix[y*dst.Stride:]
		for x := at.Min.X + 1; x < at.Max.X; x += 4 {
			sum = sum*31 + uint32(row[x*4]) + uint32(row[x*4+1])<<8 + uint32(row[x*4+2])<<16
		}
	}
	c.frostSame = at == c.frostAt && sum == c.frostSum && len(c.frostImg) > 0
	if c.frostSame {
		return
	}
	c.frostAt, c.frostSum = at, sum
	c.fw, c.fh = (at.Dx()+3)/4, (at.Dy()+3)/4
	if cap(c.frostImg) < c.fw*c.fh*4 {
		c.frostImg = make([]uint8, c.fw*c.fh*4)
	}
	c.frostImg = c.frostImg[:c.fw*c.fh*4]
	img := c.frostImg
	par(c.fh, func(lo, hi int) {
		for j := lo; j < hi; j++ {
			for i := range c.fw {
				var s [3]uint32
				n := uint32(0)
				for y := at.Min.Y + 4*j; y < min(at.Min.Y+4*j+4, at.Max.Y); y++ {
					row := dst.Pix[y*dst.Stride:]
					for x := at.Min.X + 4*i; x < min(at.Min.X+4*i+4, at.Max.X); x++ {
						s[0], s[1], s[2] = s[0]+uint32(row[x*4]), s[1]+uint32(row[x*4+1]), s[2]+uint32(row[x*4+2])
						n++
					}
				}
				o := (j*c.fw + i) * 4
				if n == 16 { // a whole block: no division, which is a call on the Show's processor
					img[o], img[o+1], img[o+2], img[o+3] = uint8(s[0]>>4), uint8(s[1]>>4), uint8(s[2]>>4), 255
				} else {
					img[o], img[o+1], img[o+2], img[o+3] = uint8(s[0]/n), uint8(s[1]/n), uint8(s[2]/n), 255
				}
			}
		}
	})
	// Three passes of a box 2r+1 wide make a blur of about frostSigma, a quarter of it at this size.
	r := max(1, int(math.Round(math.Sqrt(4*math.Pow(frostSigma*g.k/4, 2)+1)/2-0.5)))
	boxBlur(img, c.fw, c.fh, r, 3)
	c.makeGlass()
}

// makeGlass spreads the frost out to the screen's pixels, brightened and tinted as the glass is: what
// every pixel of the card starts from, made again only with the frost. The tint goes on the frost's
// own cells, which are then doubled twice (double.go), as the light is.
func (c *card) makeGlass() {
	at := c.frostAt
	w, h := at.Dx(), at.Dy()
	if cap(c.glass) < w*h {
		c.glass = make([]uint32, w*h)
	}
	c.glass = c.glass[:w*h]
	var ink [3]uint32
	for k := range 3 {
		ink[k] = uint32(glassInk[k]*0.42 + 0.5)
	}
	cells := make([]uint32, c.fw*c.fh)
	for i, v := range wordsOf(c.frostImg) {
		var l [3]uint32
		for k := range 3 {
			ch := min(255, (v>>(8*k)&0xff)*294>>8) // brighter by 0.15
			l[k] = ch*148>>8 + ink[k]              // under the tint, at 0.42
		}
		cells[i] = l[0] | l[1]<<10 | l[2]<<20
	}
	hw, hh := 2*c.fw, 2*c.fh
	half := make([]uint32, hw*hh)
	none, all := make([][2]int32, hh), make([][2]int32, hh)
	for j := range hh {
		none[j], all[j] = [2]int32{int32(hw / 2), int32(hw / 2)}, [2]int32{0, int32(hw)}
	}
	up2(cells, c.fw, c.fh, half, hw, hh, none, all)
	par(h, func(lo, hi int) {
		for y := lo; y < hi; y++ {
			row := c.glass[y*w : (y+1)*w]
			spread(row, half[min(y/2, hh-1)*hw:(min(y/2, hh-1)+1)*hw], 0, hw)
			for x := range row {
				row[x] |= 0xff000000
			}
		}
	})
}

// scale4 is each byte of x times f of 256.
func scale4(x, f uint32) uint32 {
	return (x&0x00ff00ff*f)>>8&0x00ff00ff | (x>>8&0x00ff00ff*f)&0xff00ff00
}

// cardLook is what the card's picture depends on, beside the glass: drawn again only when it changes.
type cardLook struct {
	at          image.Rectangle
	alpha, size int
	cur, prev   *cardWords
}

// draw lays the card over dst for the frame in hand.
func (c *card) draw(dst *image.RGBA, g *geometry, now time.Time) {
	k := g.k
	x0, y0, x1, y1 := c.box(g)
	c.at = image.Rect(int(x0), int(y0), int(math.Ceil(x1)), int(math.Ceil(y1)))
	area := c.at.Intersect(dst.Rect)
	ccx, ccy := float32(x0+x1)/2, float32(y0+y1)/2
	look := cardLook{c.at, int(c.alpha*255 + 0.5), int(c.scale*1000 + 0.5), c.cur, c.prev}
	if look != c.look || !c.frostSame || c.prev != nil {
		c.look = look
		c.paint(area, g, now, x0, y0, x1, y1)
	}
	// The card over the page: its picture is premultiplied, and most of it covers the page outright.
	img, pix, stride := c.img, words(dst), dst.Stride/4
	par(area.Dy(), func(lo, hi int) {
		w := area.Dx()
		for y := lo; y < hi; y++ {
			src := img[y*w : (y+1)*w]
			out := pix[(area.Min.Y+y)*stride+area.Min.X : (area.Min.Y+y)*stride+area.Min.X+w]
			for x, v := range src {
				switch a := v >> 24; a {
				case 0:
				case 255:
					out[x] = v
				default:
					out[x] = addSat(v, scale4(out[x], 256-a)) | 0xff000000
				}
			}
		}
	})

	if c.thinking && c.cur != nil && !c.cur.reply {
		// Three dots pulsing one after another at the right of the first line.
		t := float64(now.UnixMilli()%(1<<30)) / 1000
		for i := range 3 {
			ph := 0.5 + 0.5*math.Sin(t*6-float64(i)*0.9)
			lx := (cardW - cardDots + 18 + 11*float64(i)) * k
			ly := 32 * k
			cx := float64(ccx) + (lx-cardW*k/2)*c.scale
			cy := float64(ccy) + (ly-c.h*k/2)*c.scale
			dot(dst, cx, cy, (3+ph)*k*c.scale, float32((0.25+0.6*ph)*c.alpha))
		}
	}
}

// paint draws the card's picture for area, premultiplied: the glass, the words, the rim, at the card's
// strength. Only the pixels near its rounded edge need the edge worked out.
func (c *card) paint(area image.Rectangle, g *geometry, now time.Time, x0, y0, x1, y1 float64) {
	k := g.k
	if n := area.Dx() * area.Dy(); cap(c.img) < n {
		c.img = make([]uint32, n)
	} else {
		c.img = c.img[:n]
	}
	ccx, ccy := float32(x0+x1)/2, float32(y0+y1)/2
	hw, hh := float32(x1-x0)/2, float32(y1-y0)/2
	scale := float32(c.scale)
	rad := float32(cardRadius*k) * scale
	rim := float32(k) * scale // the rim's width
	alpha := uint32(c.alpha*256 + 0.5)
	top, half := float32(y0), float32(y1-y0)/2

	// The words: the old going up and out, the new coming up in after it.
	type layer struct {
		img   []uint32
		w, h  int
		alpha uint32  // of 256
		dy    float32 // in pixels
	}
	var layers []layer
	add := func(cw *cardWords, a float32, dy float32) {
		layers = append(layers, layer{words(cw.img), cw.img.Rect.Dx(), cw.img.Rect.Dy(), uint32(a*256 + 0.5), dy})
	}
	if c.prev != nil {
		so := float32(smooth(0, 1, clamp01(now.Sub(c.swapAt).Seconds()/0.18)))
		sw := float32(smooth(0, 1, clamp01((now.Sub(c.swapAt).Seconds()-0.1)/0.3)))
		add(c.prev, 1-so, -8*so*float32(k))
		add(c.cur, sw, 8*(1-sw)*float32(k))
		if sw >= 1 {
			c.prev = nil
		}
	} else if c.cur != nil {
		add(c.cur, 1, 0)
	}
	exact := c.scale == 1
	inv := 1 / scale
	fa, gw := c.frostAt, c.frostAt.Dx()

	par(area.Dy(), func(lo, hi int) {
		for y := area.Min.Y + lo; y < area.Min.Y+hi; y++ {
			py := float32(y) + 0.5
			ay := abs32(py-ccy) - (hh - rad)
			// The light along the top: white at a tenth, gone by the middle.
			shine := uint32(max(0, 0.10*(1-(py-top)/half)) * 256)
			row := c.img[(y-area.Min.Y)*area.Dx() : (y-area.Min.Y+1)*area.Dx()]
			glass := c.glass[min(max(y-fa.Min.Y, 0), fa.Dy()-1)*gw:]
			// Where the row is in each layer of words, and whether it is in it at all.
			var ly [2]int32
			for n, l := range layers {
				ly[n] = int32(((py-ccy)*inv + float32(c.h*k)/2 - l.dy - 0.5) * 256)
			}
			for x := area.Min.X; x < area.Max.X; x++ {
				px := float32(x) + 0.5
				ax := abs32(px-ccx) - (hw - rad)
				var sd float32
				edge := ax > -rim-2 || ay > -rim-2
				if edge {
					if ax > -1 && ay > -1 {
						sd = sdRound(ax, ay, rad)
					} else {
						sd = maxf(ax, ay) - rad
					}
					if sd > 0.5 {
						row[x-area.Min.X] = 0
						continue
					}
				}
				p := glass[min(max(x-fa.Min.X, 0), gw-1)]
				if shine > 0 {
					p = blend(p, 0xffffffff, shine)
				}
				for n, l := range layers {
					lx := ((px-ccx)*inv + float32(l.w)/2 - 0.5) * 256
					X, Y := int32(lx), ly[n]
					if X < 0 || Y < 0 || int(X>>8) >= l.w || int(Y>>8) >= l.h {
						continue
					}
					var v uint32
					if exact {
						v = l.img[int(Y>>8)*l.w+int(X>>8)]
					} else {
						v = bilinear(l.img, l.w, l.h, X, Y)
					}
					if v == 0 {
						continue
					}
					v = scale4(v, l.alpha)
					p = addSat(v, scale4(p, 256-v>>24))
				}
				a := alpha
				if edge {
					// The rim: a fine line just inside the edge, white at 0.35; and the edge smoothed.
					if ring := clampf(minf(sd+rim+0.5, 0.5-sd)); ring > 0 {
						p = blend(p, 0xffffffff, uint32(ring*0.35*256))
					}
					a = uint32(float32(a) * minf(1, 0.5-sd))
				}
				row[x-area.Min.X] = scale4(p|0xff000000, min(a, 256))
			}
		}
	})
}

// dot lays a soft white dot of radius rad at cx, cy over dst, at alpha.
func dot(dst *image.RGBA, cx, cy, rad float64, alpha float32) {
	for y := int(cy - rad - 1); y <= int(cy+rad+1); y++ {
		for x := int(cx - rad - 1); x <= int(cx+rad+1); x++ {
			if !(image.Point{x, y}).In(dst.Rect) {
				continue
			}
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) - rad
			a := float32(clamp01(0.5-d)) * alpha
			if a <= 0 {
				continue
			}
			p := dst.Pix[y*dst.Stride+x*4 : y*dst.Stride+x*4+3]
			for c := range 3 {
				p[c] = clamp8(float32(p[c])*(1-a) + 255*a)
			}
		}
	}
}
