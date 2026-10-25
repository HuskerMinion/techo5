//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The pieces the calendar's views share: the theme's washes and an event's fill, an icon set by its
// middle, the countdown chip, the Day, Week and Month switch, the calendars' chips, and a day's weather.

// calWash is the ground with a little light laid over it, a little dark on a light theme: the shade of
// a box on the page, a for how much (as white at that alpha would be over a dark ground).
func calWash(a int) color.RGBA {
	if dark() {
		return lerp(walnut, color.RGBA{255, 255, 255, 255}, float64(a)/255)
	}
	return lerp(walnut, color.RGBA{60, 40, 20, 255}, float64(a)/255*0.9)
}

// evFill is an event's box in its calendar's color c: the color at half on a dark theme, a pale tint
// of it on a light one, so the words over it read in the text color either way.
func evFill(c color.RGBA) color.RGBA {
	if dark() {
		return color.RGBA{c.R / 2, c.G / 2, c.B / 2, 255}
	}
	return lerp(c, color.RGBA{255, 255, 255, 255}, 0.62)
}

// calFade is c faded toward the ground by t: a day that is over, a calendar turned off.
func calFade(c color.RGBA, t float64) color.RGBA { return lerp(c, walnut, t) }

// evSub is the second line's color on an event's box: the text color, a little toward the box.
func evSub(c color.RGBA) color.RGBA { return lerp(cream, evFill(c), 0.3) }

// timeInk is the start time of an event in the month, in its calendar's color: deeper on a light theme.
func timeInk(c color.RGBA) color.RGBA {
	if dark() {
		return c
	}
	return lerp(c, color.RGBA{0, 0, 0, 255}, 0.3)
}

// mdiAt draws an icon with its middle at cx, cy, size tall in the Show 5's pixels as mdiIcon takes it.
func (r *renderer) mdiAt(name string, cx, cy, size int, c color.RGBA) {
	px := r.s(size)
	r.mdiIcon(name, cx-px/2, cy-px/2, size, c)
}

// countChip draws a countdown chip with its right edge at right, around cy, in f: filled in the accent
// for the next event, a box of the page's shade for the rest. It says where its left edge is.
func (r *renderer) countChip(text string, right, cy int, filled bool, f font.Face) int {
	w := r.width(f, text) + r.s(20)
	h := r.s(26)
	if f != r.micro {
		h = r.s(30)
	}
	b := image.Rect(right-w, cy-h/2, right, cy-h/2+h)
	fill, ink := calWash(22), cream
	if filled {
		fill, ink = amber, walnut
	}
	r.roundRect(b, h/2, fill)
	r.text(f, text, b.Min.X+r.s(10), cy+capHeight(f)/2, ink)
	return b.Min.X
}

// calSegmented is the Day, Week and Month switch, with its right edge at right on the header line, sel
// the view shown; Week is there only while the week view is on. It says where the next button goes.
func (r *renderer) calSegmented(sel string, right int, week bool) int {
	labels := []string{"Day", "Week", "Month"}
	kinds := []calHitKind{calViewDay, calViewWeek, calMonthView}
	if !week {
		labels, kinds = []string{"Day", "Month"}, []calHitKind{calViewDay, calMonthView}
	}
	f, pad := r.tiny, r.s(16)
	total := 0
	for _, l := range labels {
		total += r.width(f, l) + 2*pad
	}
	b := image.Rect(right-total, r.margin-r.s(8), right, r.margin+r.s(40))
	r.roundRect(b, b.Dy()/2, calWash(18))
	x := b.Min.X
	for i, l := range labels {
		w := r.width(f, l) + 2*pad
		seg := image.Rect(x, b.Min.Y, x+w, b.Max.Y)
		ink := dim
		if l == sel {
			in := seg.Inset(r.s(4))
			ink = r.buttonFace(in, float64(in.Dy())/2, btnPrimary)
		} else {
			r.calHitAdd(calHit{r: seg.Inset(-r.s(2)), kind: kinds[i]})
		}
		r.text(f, l, seg.Min.X+(seg.Dx()-r.width(f, l))/2, seg.Min.Y+seg.Dy()/2+r.s(9), ink)
		x += w
	}
	return b.Min.X - r.s(14)
}

// chipFace is the calendars' chips' words and height: small in the header line, larger on a row of
// their own.
func (r *renderer) chipFace(inLine bool) (font.Face, int) {
	if inLine {
		return r.micro, r.s(32)
	}
	return r.tiny, r.s(38)
}

// chipNameMost is how wide a name on a chip is at most, in the Show 5's pixels: a long one is cut
// short, so one chip never takes a row.
const chipNameMost = 200

// chipName is a calendar's name on its chip, cut short to most wide.
func (r *renderer) chipName(v calendarView, cal string, inLine bool, most int) string {
	f, _ := r.chipFace(inLine)
	return clipText(r, f, v.name(cal), most)
}

// chipPad is what a chip adds around its name: the dot or the eye, and the room at its ends.
func (r *renderer) chipPad(inLine bool) int {
	if inLine {
		return r.s(42)
	}
	return r.s(60)
}

// chipWidth is how wide one calendar's chip is, its name at most most wide.
func (r *renderer) chipWidth(v calendarView, cal string, inLine bool, most int) int {
	f, _ := r.chipFace(inLine)
	return r.width(f, r.chipName(v, cal, inLine, most)) + r.chipPad(inLine)
}

// chipsWidth is how wide the chips of cals are together.
func (r *renderer) chipsWidth(v calendarView, cals []string, inLine bool) int {
	w := 0
	for i, c := range cals {
		if i > 0 {
			w += r.chipGap(inLine)
		}
		w += r.chipWidth(v, c, inLine, r.s(chipNameMost))
	}
	return w
}

// chipRowsMost is how many rows of chips the month gives up to them.
const chipRowsMost = 2

// moreChipWidth is how wide the "+N" chip is.
func (r *renderer) moreChipWidth(n int) int {
	f, _ := r.chipFace(false)
	return r.width(f, "+"+strconv.Itoa(n)) + r.s(36)
}

// chipRows lays the calendars' chips out in rows no wider than room, at most chipRowsMost of them, and
// says how many are left over for a "+N" chip at the end of the last, which opens the list of them all.
// Where they do not all fit, the last row keeps room for that chip.
func (r *renderer) chipRows(v calendarView, room int) (rows [][]string, more int) {
	lay := func(reserve int) ([][]string, int) {
		rows := [][]string{nil}
		x := 0
		for i, cal := range v.order {
			w := r.chipWidth(v, cal, false, r.s(chipNameMost))
			if len(rows[len(rows)-1]) > 0 {
				w += r.chipGap(false)
			}
			end := room
			if len(rows) == chipRowsMost {
				end -= reserve
			}
			if x+w > end && len(rows[len(rows)-1]) > 0 {
				if len(rows) == chipRowsMost {
					return rows, len(v.order) - i
				}
				rows, x = append(rows, nil), 0
				w = r.chipWidth(v, cal, false, r.s(chipNameMost))
			}
			rows[len(rows)-1] = append(rows[len(rows)-1], cal)
			x += w
		}
		return rows, 0
	}
	if rows, more := lay(0); more == 0 {
		return rows, 0
	}
	return lay(r.chipGap(false) + r.moreChipWidth(len(v.order)))
}

// chipGap is the room between two chips.
func (r *renderer) chipGap(inLine bool) int {
	if inLine {
		return r.s(6)
	}
	return r.s(8)
}

// calWrap breaks a title into lines no wider than w, as wrap does, and after a hyphen too, so
// "Parent-teacher night" can be "Parent-" and "teacher night".
func (r *renderer) calWrap(f font.Face, s string, w int) []string {
	var words []string
	for _, word := range strings.Fields(s) {
		for {
			i := strings.Index(word, "-")
			if i <= 0 || i == len(word)-1 {
				break
			}
			words = append(words, word[:i+1])
			word = word[i+1:]
		}
		words = append(words, word)
	}
	var lines []string
	line := ""
	for _, word := range words {
		try := word
		if line != "" && !strings.HasSuffix(line, "-") {
			try = line + " " + word
		} else if line != "" {
			try = line + word
		}
		if line != "" && r.width(f, try) > w {
			lines = append(lines, line)
			line = word
			continue
		}
		line = try
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// calChips draws a chip for each of cals from x, around cy: in its color while it is shown, an outline
// with a crossed-out eye while it is hidden, each name at most most wide. A tap on one turns it over.
// It says where it ended.
func (r *renderer) calChips(v calendarView, cals []string, x, cy int, inLine bool, most int) int {
	f, h := r.chipFace(inLine)
	dot, eye, gap := r.s(6), 16, r.s(30)
	if !inLine {
		dot, eye, gap = r.s(8), 20, r.s(36)
	}
	for _, cal := range cals {
		name := r.chipName(v, cal, inLine, most)
		w := r.chipWidth(v, cal, inLine, most)
		b := image.Rect(x, cy-h/2, x+w, cy-h/2+h)
		base := cy + capHeight(f)/2
		if slices.Contains(v.hidden, cal) {
			r.roundStroke(b, float64(h)/2, r.sf(3)/2, calFade(dim, 0.3))
			r.mdiAt("eye-off", b.Min.X+r.s(16), cy, eye, calFade(dim, 0.2))
			r.text(f, name, b.Min.X+gap, base, calFade(dim, 0.15))
		} else {
			c := v.color(cal)
			t := 0.28
			if !dark() {
				t = 0.35
			}
			r.roundRect(b, h/2, lerp(walnut, c, t))
			r.aaDisc(float64(b.Min.X+r.s(16)), float64(cy), float64(dot), c)
			r.text(f, name, b.Min.X+gap, base, cream)
		}
		r.calHitAdd(calHit{r: b.Inset(-r.s(3)), kind: calChip, cal: cal})
		x += w + r.chipGap(inLine)
	}
	return x
}

// moreChip is the "+N" chip after the chips that fit, for the calendars that did not: a tap opens the
// list of every calendar, where each can be turned off or on, so a hidden one is never out of reach.
func (r *renderer) moreChip(n, x, cy int) {
	_, h := r.chipFace(false)
	b := image.Rect(x, cy-h/2, x+r.moreChipWidth(n), cy-h/2+h)
	r.labelButton(b, "+"+strconv.Itoa(n), btnSecondary)
	r.calHitAdd(calHit{r: b.Inset(-r.s(3)), kind: calChipsMore})
}

// calChipsList is the list of every calendar over the page, from the "+N" chip: a chip each, in as many
// columns as they need, and Done. A tap on a chip turns it over and leaves the list up; Done, or a tap
// outside it, puts it away.
func (r *renderer) calChipsList(v calendarView) {
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(color.NRGBA{0, 0, 0, 150}), image.Point{}, draw.Over)
	r.calHitAdd(calHit{r: r.dst.Rect, kind: calChipsDone})
	w, h := r.w*80/100, r.h*84/100
	card := image.Rect((r.w-w)/2, (r.h-h)/2, (r.w+w)/2, (r.h+h)/2)
	fill := color.RGBA{walnut.R/2 + 20, walnut.G/2 + 20, walnut.B/2 + 20, 255}
	if !dark() {
		fill = shift(walnut, -10)
	}
	r.roundRect(card, r.s(18), fill)
	r.calHitAdd(calHit{r: card, kind: calInside})
	r.text(r.small, "Calendars", card.Min.X+r.s(28), card.Min.Y+r.s(48), cream)
	done := image.Rect(card.Max.X-r.s(130), card.Min.Y+r.s(14), card.Max.X-r.s(22), card.Min.Y+r.s(62))
	r.labelButton(done, "Done", btnSecondary)
	r.calHitAdd(calHit{r: done.Inset(-r.s(6)), kind: calChipsDone})
	// As many columns as the calendars need, each as wide as the widest chip with a gap after it; where
	// that is more than the card has room for, the names are cut to the columns that fit, so no chip
	// or its tap area runs into the next.
	top, rowH, gap := card.Min.Y+r.s(84), r.s(48), r.s(16)
	room := card.Dx() - r.s(56)
	perCol := max((card.Max.Y-r.s(16)-top)/rowH, 1)
	cols := (len(v.order) + perCol - 1) / perCol
	widest := 0
	for _, cal := range v.order {
		widest = max(widest, r.chipWidth(v, cal, false, r.s(chipNameMost)))
	}
	colW := widest + gap
	most := r.s(chipNameMost)
	if cols*colW-gap > room {
		colW = (room + gap) / cols
		most = max(colW-gap-r.chipPad(false), r.s(40))
	}
	for i, cal := range v.order {
		x := card.Min.X + r.s(28) + (i/perCol)*colW
		cy := top + (i%perCol)*rowH + rowH/2
		r.calChips(v, []string{cal}, x, cy, false, most)
	}
}

// dayWeather draws a day's forecast with its right edge at right, its words' baseline at base: the
// icon, size tall, then the words. It says where its left edge is, or right when there is none.
func (r *renderer) dayWeather(days []hass.Day, day time.Time, right, base int, f font.Face, size int, words bool) int {
	d, ok := dayForecast(days, day)
	if !ok {
		return right
	}
	text := highLow(d)
	if words {
		if c := conditionWords(d.Condition); c != "" {
			text += "  " + c
		}
	}
	x := right - r.width(f, text)
	r.text(f, text, x, base, dim)
	name, c := weatherGlyph(d.Condition)
	cx := x - r.s(size)/2 - r.s(6)
	r.mdiAt(name, cx, base-capHeight(f)/2, size, c)
	return cx - r.s(size)/2
}
