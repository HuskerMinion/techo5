package rainbow

import (
	"image"
	"image/color"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// The card: frosted glass in the middle of the screen, a little above it, with the words on it. It
// comes in from a little smaller and lower with a bounce, and finds its height on a spring as the
// words change. While the device listens and thinks it says what was heard, with three dots pulsing
// while it thinks; once it answers, what was heard is its title and the answer is under it, the one
// going up and out as the other comes up in. It goes away shrinking a little, upward. The glass is the
// page under it blurred, brightened and tinted, lit a little along its top, with a fine rim.
//
// An answer longer than the card's cardLines is laid out whole and seen through a window of that many
// lines, which moves down it as the voice says it, the line being said kept near the top, and rests on
// the last lines once it is all said. The window's edges fade a little where there is more past them.
// An answer past cardMost lines is cut there with an ellipsis: the whole of it is one image, and a reply
// of a few thousand characters would otherwise make one of many megabytes.

// The card's size and timing, in design units and seconds.
const (
	cardW      = 440.0 // wide enough for most questions on a line or two
	cardTop    = 58.0  // its top above the screen's middle
	cardRadius = 22.0
	cardPad    = 20.0 // the words' margin each side
	cardDots   = 56.0 // room kept at the right for the thinking dots
	cardMin    = 64.0 // the height of a card of one line
	cardLines  = 4    // the most lines an answer takes on the card: a longer one scrolls
	cardMost   = 40   // the most lines of an answer it scrolls through: the last is cut with an ellipsis
	scrollLead = 1.0  // the lines kept over the one being said, while there are lines to scroll
	fadeDepth  = 0.7  // how far the window's edge fades where there is more past it
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
//
// An answer longer than cardLines is all in img, and the card shows a window of it: where the window is
// in img and how tall, in pixels, how much of the answer is below it before it scrolls, a line's height,
// and each line's length in characters, the way the voice gets through them. scroll is how many lines
// down the window is, and scrollTo where it is going.
type cardWords struct {
	key    string
	reply  bool
	img    *image.RGBA
	height float64

	winTop, winH, more, lineH float64
	chars                     []int
	scroll, scrollTo          float64
	// whole is how many times longer the answer is than what is laid out of it, 1 when none of it is
	// cut: said is a share of the whole answer, and the window goes over the lines that are shown.
	whole float64
}

// scrolls is whether the words are more than the card shows at once.
func (w *cardWords) scrolls() bool { return w != nil && w.more > 0 }

// offset is how far the window is scrolled, in whole pixels.
func (w *cardWords) offset() int {
	if !w.scrolls() {
		return 0
	}
	return int(w.scroll*w.lineH + 0.5)
}

// scrollFor is how many lines down a window n lines tall is on an answer whose lines are chars long,
// once said of it has been said: the line being said kept scrollLead lines under the window's top,
// from the first lines before it starts to the last once it is all said. It goes by characters, as
// the Classic pages do (replyPage), within a line as well as from one to the next, so it moves on
// smoothly.
func scrollFor(chars []int, n int, said float64) float64 {
	if len(chars) <= n {
		return 0
	}
	total := 0
	for _, c := range chars {
		total += c
	}
	at := clamp01(said) * float64(total)
	pos := float64(len(chars))
	for i, c := range chars {
		if c > 0 && at < float64(c) {
			pos = float64(i) + at/float64(c)
			break
		}
		at -= float64(c)
	}
	return min(max(pos-scrollLead, 0), float64(len(chars)-n))
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

	look   cardLook // what img was painted for
	img    []uint32 // the card's picture, premultiplied, a word to a pixel, the size of at on the screen
	offset int      // the answer's scroll img was painted at, in pixels
	said   float64  // how much of the answer has been said, for the frame in hand

	// The rows of an answer's window as img has them without the words, the glass and its light, so
	// a scroll lays the words on them again and nothing else: kept by the last whole painting when the
	// card was still, whole, and its answer scrolled; rows lo to hi of img.
	under            []uint32
	underLo, underHi int
	underOK          bool
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
	if w := c.cur; w.scrolls() && (w.scroll != w.scrollTo ||
		c.said > 0 && c.said < 1 && w.scrollTo < float64(len(w.chars)-cardLines)) {
		return true // an answer scrolling, or the voice still on its way to the end of it
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
	was := c.cur

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
	c.said = t.Said
	if w := c.cur; w.scrolls() {
		// The window follows the voice, a little behind it so a jump in the reckoning is no jump on the
		// screen, and only ever down: the reckoning can go back a little as the audio comes in. New
		// words start where the voice already is.
		to := scrollFor(w.chars, cardLines, t.Said*w.whole)
		if w != was {
			w.scroll, w.scrollTo = to, to
		}
		w.scrollTo = max(w.scrollTo, to)
		w.scroll += (w.scrollTo - w.scroll) * rate(0.2, dt)
		if math.Abs(w.scrollTo-w.scroll) < 0.002 {
			w.scroll = w.scrollTo
		}
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

// newCardWords lays out the words for the card: what was heard, up to cardHeard lines; or for an
// answer, what was heard as the title and the answer under it, up to cardMost lines of it, the card as
// tall as cardLines of it.
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
		for _, l := range wrapLines(f.Reply, reply, int(float64(w)-2*pad), cardMost) {
			lines = append(lines, line{f.Reply, l, replyInk})
		}
	}
	text := 0.0
	for _, l := range lines {
		text += lineOf(l.face)
	}
	// Past cardLines the answer is below the card's window, and the card no taller for it.
	over := max(0, len(lines)-title-cardLines)
	if !isReply {
		over = 0
	}
	hidden := float64(over) * lineOf(f.Reply)
	// One line sits in the middle of a card cardMin tall; more take more.
	edge := max(14*k, (cardMin*k-lineOf(f.Heard))/2)
	gap := 0.0
	if isReply && heard != "" {
		gap = 6 * k
	}
	hgt := max(cardMin*k, 2*edge+text+gap)
	img := image.NewRGBA(image.Rect(0, 0, w, int(math.Ceil(hgt))))
	cw := &cardWords{key: key, reply: isReply, img: img, whole: 1, height: max(cardMin*k, 2*edge+text+gap-hidden) / k}
	y := edge
	for i, l := range lines {
		if over > 0 && i >= title {
			if i == title {
				cw.winTop = y
			}
			cw.chars = append(cw.chars, len([]rune(l.text)))
		}
		y += float64(l.face.Metrics().Ascent.Ceil())
		fonts.DrawString(img, image.NewUniform(l.ink), l.face, fixed.P(int(pad), int(y)), l.text)
		y += lineOf(l.face) - float64(l.face.Metrics().Ascent.Ceil())
		if i == title-1 {
			y += gap
		}
	}
	if over > 0 {
		cw.lineH = lineOf(f.Reply)
		cw.winH, cw.more = cardLines*cw.lineH, hidden
		cw.whole = wholeOf(reply, cw.chars)
	}
	return cw
}

// wholeOf is how many times longer reply is than the lines laid out of it, chars long, 1 when they are
// all of it. The voice says the whole reply, and an answer cut at cardMost lines would otherwise have
// the window fall further and further behind it, still on the first lines when the voice is done.
func wholeOf(reply string, chars []int) float64 {
	shown := max(len(chars)-1, 0) // the spaces the lines were broken at
	for _, c := range chars {
		shown += c
	}
	all := utf8.RuneCountInString(strings.Join(strings.Fields(reply), " "))
	if shown <= 0 || all <= shown {
		return 1
	}
	return float64(all) / float64(shown)
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
// ellipsis when there is more; with n 0, into as many as it takes.
func wrapLines(face font.Face, text string, w, n int) []string {
	var out []string
	line := ""
	for _, word := range strings.Fields(text) {
		try := word
		if line != "" {
			try = line + " " + word
		}
		if line != "" && fonts.MeasureString(face, try).Ceil() > w {
			out = append(out, line)
			if len(out) == n {
				// One word past the line is enough to cut it with the ellipsis: the whole rest of a long
				// answer would be measured again for every rune clipLine takes off its end.
				out[n-1] = clipLine(face, out[n-1]+" "+word, w)
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
	if fonts.MeasureString(face, text).Ceil() <= w {
		return text
	}
	// The longest start of it that fits with the ellipsis, found by halves: one long word, a link
	// say, taken off a rune at a time would be measured once for each of its runes.
	r := []rune(text)
	lo, hi := 0, len(r) // r[:lo] fits, r[:hi+1] does not
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if fonts.MeasureString(face, string(r[:mid])+"…").Ceil() <= w {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return strings.TrimRight(string(r[:lo]), " ") + "…"
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
	off := c.cur.offset()
	switch {
	case look != c.look || !c.frostSame || c.prev != nil || off != c.offset && c.scale != 1:
		c.look = look
		c.paint(area, 0, area.Dy(), g, now, x0, y0, x1, y1)
	case off != c.offset && c.underOK:
		// Only the answer has scrolled: its words are laid again on its window's rows as they were
		// without them, the glass, the title and the rim being as they were.
		c.scroll(area, g, x0, y0, x1, y1)
	case off != c.offset:
		lo, hi := c.window(area, y0)
		c.paint(area, lo, hi, g, now, x0, y0, x1, y1)
	}
	c.offset = off
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

// window is the rows of area the answer's window covers on a card whose top is at y0, whole size, and a
// row more each side.
func (c *card) window(area image.Rectangle, y0 float64) (lo, hi int) {
	lo = int(math.Floor(y0+c.cur.winTop)) - 1 - area.Min.Y
	hi = int(math.Ceil(y0+c.cur.winTop+c.cur.winH)) + 1 - area.Min.Y
	return max(lo, 0), max(min(hi, area.Dy()), 0)
}

// wordsLayer is one set of words as the card lays them: their picture, how strong, how far moved up or
// down, in pixels, and for an answer that scrolls its window: where it is and how tall, how much is
// below it, how far it is scrolled, and how much its top and bottom edges fade, by how much there is
// past each, up to half a line.
type wordsLayer struct {
	img   []uint32
	w, h  int
	alpha uint32 // of 256
	dy    float32

	winTop, winH, more, off float32
	fade, fadeTop, fadeBot  float32
}

func newWordsLayer(cw *cardWords, a, dy float32) wordsLayer {
	l := wordsLayer{img: words(cw.img), w: cw.img.Rect.Dx(), h: cw.img.Rect.Dy(), alpha: uint32(a*256 + 0.5), dy: dy}
	if cw.scrolls() {
		off := float32(cw.offset())
		l.winTop, l.winH, l.more, l.off = float32(cw.winTop), float32(cw.winH), float32(cw.more), off
		l.fade = float32(cw.lineH) / 2
		l.fadeTop, l.fadeBot = fadeDepth*clampf(off/l.fade), fadeDepth*clampf((l.more-off)/l.fade)
	}
	return l
}

// row is where the card's row ry is in the words, in 256ths of a pixel, and how strong the words are
// on it: in an answer's window, that far down the answer, less at the window's edges, and under the
// window the answer's end.
func (l *wordsLayer) row(ry float32) (int32, uint32) {
	a := l.alpha
	if l.more > 0 && ry >= l.winTop {
		if in := ry - l.winTop; in < l.winH {
			ry += l.off
			f := (1 - l.fadeTop*(1-clampf(in/l.fade))) * (1 - l.fadeBot*(1-clampf((l.winH-in)/l.fade)))
			a = uint32(float32(l.alpha) * f)
		} else {
			ry += l.more
		}
	}
	return int32(ry * 256), a
}

// scroll paints the answer's window again for a new scroll, from the rows kept without its words:
// only the words' columns, well inside the card, where the picture is the glass and the words alone.
// It gives what paint does, at a fraction of the cost, on every frame of a long answer being said.
func (c *card) scroll(area image.Rectangle, g *geometry, x0, y0, x1, y1 float64) {
	k := g.k
	ccx, ccy := float32(x0+x1)/2, float32(y0+y1)/2
	l := newWordsLayer(c.cur, 1, 0)
	hk := float32(c.h * k)
	m := int32(cardPad * k / 2 * 256) // half the words' margin: clear of the rim and the rounded corners
	w := area.Dx()
	par(c.underHi-c.underLo, func(a, b int) {
		for r := c.underLo + a; r < c.underLo+b; r++ {
			// As paint has it, at the card's whole size and with nothing moving it.
			Y, la := l.row(float32(area.Min.Y+r) + 0.5 - ccy + hk/2 - 0.5)
			in := Y >= 0 && int(Y>>8) < l.h
			under, row := c.under[(r-c.underLo)*w:(r-c.underLo+1)*w], c.img[r*w:(r+1)*w]
			for x := area.Min.X; x < area.Max.X; x++ {
				X := int32((float32(x) + 0.5 - ccx + float32(l.w)/2 - 0.5) * 256)
				if X < m || X >= int32(l.w)<<8-m {
					continue
				}
				p := under[x-area.Min.X]
				if in {
					if v := l.img[int(Y>>8)*l.w+int(X>>8)]; v != 0 {
						v = scale4(v, la)
						p = addSat(v, scale4(p, 256-v>>24))
					}
				}
				row[x-area.Min.X] = p | 0xff000000
			}
		}
	})
}

// paint draws the card's picture for area, premultiplied: the glass, the words, the rim, at the card's
// strength. Only the pixels near its rounded edge need the edge worked out. Only area's rows lo to hi
// are painted: the rest are kept from the picture before, which is the size of area.
func (c *card) paint(area image.Rectangle, lo, hi int, g *geometry, now time.Time, x0, y0, x1, y1 float64) {
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
	var layers []wordsLayer
	add := func(cw *cardWords, a float32, dy float32) {
		layers = append(layers, newWordsLayer(cw, a, dy))
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

	// A whole painting of a still card whose answer scrolls keeps its window's rows without the words,
	// for scroll; any other leaves nothing kept.
	if lo == 0 && hi == area.Dy() {
		c.underOK = c.prev == nil && c.cur.scrolls() && exact && alpha == 256
		if c.underOK {
			c.underLo, c.underHi = c.window(area, y0)
			if n := (c.underHi - c.underLo) * area.Dx(); cap(c.under) < n {
				c.under = make([]uint32, n)
			} else {
				c.under = c.under[:n]
			}
		}
	}
	keep := c.underOK && lo == 0 && hi == area.Dy()

	par(hi-lo, func(a, b int) {
		for y := area.Min.Y + lo + a; y < area.Min.Y+lo+b; y++ {
			py := float32(y) + 0.5
			ay := abs32(py-ccy) - (hh - rad)
			// The light along the top: white at a tenth, gone by the middle.
			shine := uint32(max(0, 0.10*(1-(py-top)/half)) * 256)
			row := c.img[(y-area.Min.Y)*area.Dx() : (y-area.Min.Y+1)*area.Dx()]
			glass := c.glass[min(max(y-fa.Min.Y, 0), fa.Dy()-1)*gw:]
			// Where the row is in each layer of words, and whether it is in it at all, and how strong
			// the layer is on it.
			var ly [2]int32
			var la [2]uint32
			for n := range layers {
				ly[n], la[n] = layers[n].row((py-ccy)*inv + float32(c.h*k)/2 - layers[n].dy - 0.5)
			}
			var under []uint32
			if r := y - area.Min.Y; keep && r >= c.underLo && r < c.underHi {
				under = c.under[(r-c.underLo)*area.Dx() : (r-c.underLo+1)*area.Dx()]
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
				if under != nil {
					under[x-area.Min.X] = p
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
					v = scale4(v, la[n])
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
