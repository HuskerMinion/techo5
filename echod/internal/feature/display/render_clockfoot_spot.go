//go:build spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// The clock small at the foot of the circle (Clock position), under a photo's subject. Each style that
// goes there (footStyle) is laid out for it directly: the time, the date and one more line. What the
// face says at the top - the alert pill, the status word - stays where it always is.

// footClockFace is the clock at the foot, in style.
func (r *roundRenderer) footClockFace(s roundScene, style string) {
	defer r.alertPill(s.alerts.Here, clockPillY)
	if style == styleSun && !s.style.sunOK {
		style = styleClassic // until home's place is known, as in the middle
	}
	switch style {
	case styleFlip:
		r.statusWord(s, 118)
		r.footFlip(s)
		return
	case styleLED:
		r.statusWord(s, 118)
		r.footLED(s)
		return
	case styleGlow:
		if s.slideshow == nil {
			field := r.glow.glowField(side/glowCell+1, side/glowCell+1, s.now, colBackground,
				[3]color.RGBA{colAccent, colTimer, lerp(colDim, colAccent, 0.4)})
			r.glow.stretch(r.dst, field, glowMask())
		}
	}
	r.statusWord(s, 118)
	r.footTime(s, 364)
	dateCol := colDim
	if style == styleGlow {
		dateCol = colText
	}
	r.footDate(s, 398, dateCol)
	switch {
	case style == styleSun:
		r.footLast(s, 430, "Sun up "+clockText(s.style.rise)+"  ·  down "+clockText(s.style.set), color.RGBA{230, 170, 120, 255})
	default:
		r.footLast(s, 430, weatherLine(s.weather), colDim)
	}
}

// footTime is the time at baseline, smaller than the middle's, the AM/PM after it.
func (r *roundRenderer) footTime(s roundScene, baseline int) {
	f := r.styleFace(true, 72)
	hm := clockHM(s.now)
	r.centered(f, hm, baseline, colText)
	if ampm := clockSuffix(s.now); ampm != "" {
		r.text(r.label, ampm, center+r.width(f, hm)/2+ampmGap, baseline, colText)
	}
}

// footDate is the date at baseline: the long one when the circle is wide enough there, else the
// short one.
func (r *roundRenderer) footDate(s roundScene, baseline int, c color.RGBA) {
	date := locale.LongDate(s.now, screenLang())
	if w := footRoom(baseline); r.width(r.small, date) > w {
		date = spotClip(r, r.small, locale.ShortDate(s.now, screenLang()), w)
	}
	r.centered(r.small, date, baseline, c)
}

// footLast is the last line at the foot: a running timer, else a missed alarm, else a problem with the
// slideshow, else what the style says there (otherwise, in c).
func (r *roundRenderer) footLast(s roundScene, baseline int, otherwise string, c color.RGBA) {
	w := footRoom(baseline)
	switch {
	case len(s.timers) > 0:
		r.centered(r.small, fmt.Sprintf(tr("timer_left_s"), clockDuration(s.timers[0].Left)), baseline, colTimer)
	case s.missed != "":
		r.centered(r.small, spotClip(r, r.small, s.missed, w), baseline, colTimer)
	case s.slideshowTrouble != "":
		r.centered(r.tiny, spotClip(r, r.tiny, s.slideshowTrouble, w), baseline, colDim)
	case otherwise != "":
		r.centered(r.tiny, spotClip(r, r.tiny, otherwise, w), baseline, c)
	}
}

// footRoom is how much width a line has at baseline, inside the rim with a little to spare: the foot
// of the circle narrows fast.
func footRoom(baseline int) int {
	dy := float64(baseline - center)
	return int(2*math.Sqrt(max(float64(rimIn*rimIn)-dy*dy, 0))) - 32
}

// footFlip is the time on small flip cards, and under them the AM/PM and the day, or a timer.
func (r *roundRenderer) footFlip(s roundScene) {
	h, m := hourMinute(s.now)
	cards := []string{}
	if len(h) == 2 {
		cards = append(cards, h[:1])
	}
	cards = append(cards, h[len(h)-1:], m[:1], m[1:])
	const cw, ch, gap, apart = 48, 84, 5, 12
	total := len(cards)*cw + (len(cards)-2)*gap + apart
	x, y := center-total/2, 300
	f := r.styleFace(true, 66)
	for i, c := range cards {
		b := image.Rect(x, y, x+cw, y+ch)
		r.roundFill(b, 6, color.RGBA{36, 42, 52, 255}, color.RGBA{28, 33, 41, 255})
		r.cardFigure(f, c, image.Rect(x, y+ch*12/100, x+cw, y+ch-ch*12/100), colText)
		r.line(float64(x), float64(y+ch/2), float64(x+cw), float64(y+ch/2), 2, colBackground)
		x += cw + gap
		if i == len(cards)-3 {
			x += apart - gap
		}
	}
	line := shortDay(s)
	if ampm := clockSuffix(s.now); ampm != "" {
		line = ampm + "  ·  " + line
	}
	r.footLast(s, 414, line, colDim)
}

// footLED is the time in small seven-segment digits, the date under it and a timer under that.
func (r *roundRenderer) footLED(s roundScene) {
	h, m := hourMinute(s.now)
	digits := []int{-1, -1, -1, -1}
	if len(h) == 2 {
		digits[0] = int(h[0] - '0')
	}
	digits[1] = int(h[len(h)-1] - '0')
	if len(m) == 2 {
		digits[2], digits[3] = int(m[0]-'0'), int(m[1]-'0')
	}
	const dw, dh, t = 26.0, 54.0, 6.0
	gap, colon := dw*0.42, t*2
	ampm := clockSuffix(s.now)
	aw := 0.0
	if ampm != "" {
		aw = float64(r.width(r.label, ampm)) + 8
	}
	total := 4*dw + 3*gap + colon + aw
	x, top := float64(center)-total/2, 304.0
	lit, ghost := colAccent, lerp(colBackground, colAccent, 0.1)
	for i, d := range digits {
		r.segmentDigitAt(x, top, dw, dh, t, d, lit, ghost)
		x += dw + gap
		if i == 1 {
			cx := x - gap/2 + colon/2
			r.discAt(cx, top+dh*0.3, t*0.55, lit)
			r.discAt(cx, top+dh*0.7, t*0.55, lit)
			x += colon
		}
	}
	if ampm != "" {
		r.text(r.label, ampm, int(x-gap+8), int(top+dh), lit)
	}
	r.footDate(s, 394, colDim)
	r.footLast(s, 426, "", colDim)
}
