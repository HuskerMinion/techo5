//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"

	"golang.org/x/image/font"
)

// eqLine is one line of words under the bars.
type eqLine struct {
	face  font.Face
	text  string
	c     color.RGBA
	lineH int
}

// eqWords lays out what goes under the bars, maxW wide: what it is doing, what was heard, and the
// answer, the answer a size smaller when it is long.
func (r *renderer) eqWords(s scene, room, maxW int) []eqLine {
	accent, heardCol, replyCol := color.RGBA{120, 220, 140, 255}, color.RGBA{170, 178, 190, 255}, color.RGBA{240, 244, 250, 255}
	if skin() != skinNone {
		// On the skin's ground (turnGround), in its colors: a light skin's ground would lose these.
		accent, heardCol, replyCol = amber, dim, cream
	}
	if s.eq.night {
		accent, heardCol, replyCol = color.RGBA{255, 90, 70, 255}, color.RGBA{150, 110, 105, 255}, color.RGBA{255, 140, 120, 255}
	}
	var out []eqLine
	switch s.phase {
	case "listening":
		out = append(out, eqLine{r.small, tr("ui_listening_turn"), accent, r.s(42)})
	case "thinking":
		out = append(out, eqLine{r.small, tr("ui_thinking_turn"), accent, r.s(42)})
	}
	if s.heard != "" && s.phase != "listening" {
		for _, l := range r.wrap(r.small, "“"+s.heard+"”", maxW) {
			out = append(out, eqLine{r.small, l, heardCol, r.s(40)})
		}
		out[len(out)-1].lineH += r.s(10) // a breath between the question and the answer
	}
	if s.reply == "" || (s.phase != "replying" && s.phase != "lingering") {
		return out
	}
	used := 0
	for _, l := range out {
		used += l.lineH
	}
	face, lineH := r.body, r.s(52)
	lines := r.wrap(face, s.reply, maxW)
	if used+len(lines)*lineH > room {
		face, lineH = r.small, r.s(42)
		lines = r.wrap(face, s.reply, maxW)
	}
	for _, l := range lines {
		out = append(out, eqLine{face, l, replyCol, lineH})
	}
	return out
}

// eqGround is the turn page's background on the Show.
var eqGround = color.RGBA{8, 10, 16, 255}

// turnGround is the turn page's background in force: eqGround, or under a skin the skin's own ground,
// so that a light skin's turn is light too. At night it stays dark, under the night's red words.
func turnGround(s scene) color.RGBA {
	if skin() != skinNone && !s.eq.night {
		return walnut
	}
	return eqGround
}

// equalizer draws the whole turn page in the bars style.
func (r *renderer) equalizer(s scene) {
	ground := turnGround(s)
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(ground), image.Point{}, draw.Src)
	top, bot, under, bottom, lines := r.eqLayout(s, 54)
	drawBars(r.dst, image.Rect(r.s(40), top, r.w-r.s(40), bot), s.eq, ground, max(1, r.s(2)), r.s(4))
	r.eqText(lines, bot+under, bottom)
}

// wave draws the whole turn page in the wave style.
func (r *renderer) wave(s scene) {
	ground := turnGround(s)
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(ground), image.Point{}, draw.Src)
	top, bot, under, bottom, lines := r.eqLayout(s, 58)
	if r.wb == nil {
		r.wb = &waveBuf{}
	}
	pad := r.s(20)
	if luma(ground.R, ground.G, ground.B) <= 128 {
		drawWave(r.dst, r.wb, image.Rect(0, top, r.w, bot), pad, s.eq, ground, s.now, max(2, r.s(5)/2), 0)
	} else {
		// A light skin's ground: the wave is light added to the ground and would vanish into it, so it
		// is drawn on the ground turned over and the band turned back, dark lines on the light.
		area := image.Rect(0, top-pad, r.w, bot+pad).Intersect(r.dst.Rect)
		invertRGB(r.dst, area)
		over := color.RGBA{255 - ground.R, 255 - ground.G, 255 - ground.B, 255}
		drawWave(r.dst, r.wb, image.Rect(0, top, r.w, bot), pad, s.eq, over, s.now, max(2, r.s(5)/2), 0)
		invertRGB(r.dst, area)
	}
	r.eqText(lines, bot+under, bottom)
}

// invertRGB turns the colors in area of dst over, leaving their alpha.
func invertRGB(dst *image.RGBA, area image.Rectangle) {
	for y := area.Min.Y; y < area.Max.Y; y++ {
		row := dst.Pix[dst.PixOffset(area.Min.X, y):dst.PixOffset(area.Max.X, y)]
		for i := 0; i+3 < len(row); i += 4 {
			row[i], row[i+1], row[i+2] = 255-row[i], 255-row[i+1], 255-row[i+2]
		}
	}
}

// turnVisual draws the whole turn page in the VU, Orb or HAL 9000 style (turn_visual.go).
func (r *renderer) turnVisual(s scene) {
	ground := turnGround(s)
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(ground), image.Point{}, draw.Src)
	top, bot, under, bottom, lines := r.eqLayout(s, 58)
	band := image.Rect(r.s(40), top, r.w-r.s(40), bot)
	switch s.eq.style {
	case "vu":
		drawVU(r.dst, band, s.eq, ground)
	case "orb":
		drawOrb(r.dst, band, s.eq, s.eq.phase, s.now)
	default:
		drawHAL(r.dst, band, s.eq, s.eq.phase, s.now)
	}
	r.eqText(lines, bot+under, bottom)
}

// eqLayout splits the page between the picture and the words: the picture takes the top part (pct of
// the height) and gives way to a long answer down to a quarter of the screen. It returns the picture's
// top and bottom, the gap under it, the words' last baseline, and the words.
func (r *renderer) eqLayout(s scene, pct int) (top, bot, under, bottom int, lines []eqLine) {
	top, bottom = r.s(headerH)+r.s(8), r.h-r.s(14)
	under = r.s(54)
	lines = r.eqWords(s, bottom-(r.h*28/100)-under, r.w-2*r.margin)
	words := 0
	for _, l := range lines {
		words += l.lineH
	}
	bot = min(r.h*pct/100, bottom-under-words)
	bot = max(bot, r.h*28/100)
	return top, bot, under, bottom, lines
}

// eqText draws the words centered from y down; what still does not fit ends in an ellipsis.
func (r *renderer) eqText(lines []eqLine, y, bottom int) { r.eqTextAt(lines, y, bottom, -1) }

// eqTextAt is eqText with the words against left, or centered for a left below 0.
func (r *renderer) eqTextAt(lines []eqLine, y, bottom, left int) {
	for i, l := range lines {
		base := y + l.lineH - r.s(14)
		last := i+1 < len(lines) && base+lines[i+1].lineH > bottom+r.s(10)
		text := l.text
		if last {
			text += " …"
		}
		x := left
		if x < 0 {
			x = (r.w - r.width(l.face, text)) / 2
		}
		r.text(l.face, text, x, base, l.c)
		if last {
			return
		}
		y += l.lineH
	}
}
