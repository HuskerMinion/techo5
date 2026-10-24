//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/vector"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// calendarMonth is the month as a grid, Sunday first. An event of several days is one bar across its
// days, pointed where it runs on past the week or the month, with the day it ends; the days that are
// over are dimmed and today's number sits in the accent. In full (the month details switch) a timed
// event reads "9a Team standup", its start time in its calendar's color, with an icon on a panel
// bigger than the Show 5's, and the coming week's days carry their weather; simple keeps the timed
// events as bars of their color. A day with more than fits says how many more.
func (r *renderer) calendarMonth(v calendarView, l calLook) {
	title := locale.MonthYear(v.month, screenLang())
	r.text(r.title, title, r.margin, r.margin+r.s(34), cream)
	x := r.calButton(tr("done"), r.w-r.margin, calDone)
	if v.month.Year() != v.now.Year() || v.month.Month() != v.now.Month() {
		x = r.calButton(tr("today"), x, calToday)
	}
	x = r.calIconButton("chevron-right", "›", x, calNext)
	x = r.calIconButton("chevron-left", "‹", x, calPrev)
	if l.week {
		x = r.calSegmented(calMonthView, x, true)
	}

	top := r.margin + r.s(58)
	if len(v.order) > 1 {
		// The calendars' chips: on the header line where they fit beside the title, else a row of their own.
		// Too many for a row wrap onto a second, and those left after two rows are a "+N" chip.
		at := r.margin + r.width(r.title, title) + r.s(22)
		if at+r.chipsWidth(v, v.order, true) <= x+r.s(6) {
			r.calChips(v, v.order, at, r.margin+r.s(17), true, r.s(chipNameMost))
		} else {
			rows, more := r.chipRows(v, r.w-2*r.margin)
			end := 0
			for i, row := range rows {
				end = r.calChips(v, row, r.margin, r.margin+r.s(80)+i*r.s(46), false, r.s(chipNameMost))
			}
			if more > 0 {
				r.moreChip(more, end, r.margin+r.s(80)+(len(rows)-1)*r.s(46))
			}
			top += r.s(50) + (len(rows)-1)*r.s(46)
		}
	}

	colW := (r.w - 2*r.margin) / 7
	for i := range 7 {
		day := v.month.AddDate(0, 0, i-int(v.month.Weekday()))
		label := locale.ShortWeekday(day, screenLang())
		r.text(r.tiny, label, r.margin+i*colW+(colW-r.width(r.tiny, label))/2, top+r.s(20), dim)
	}
	gridTop := top + r.s(28)
	first := v.month
	start := first.AddDate(0, 0, -int(first.Weekday()))
	days := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, time.Local).Day()
	weeks := (int(first.Weekday()) + days + 6) / 7
	rowH := (r.h - r.s(8) - gridTop) / weeks
	today := time.Date(v.now.Year(), v.now.Month(), v.now.Day(), 0, 0, 0, 0, time.Local)

	for w := range weeks {
		row := start.AddDate(0, 0, 7*w)
		cells := make([]image.Rectangle, 7)
		for col := range 7 {
			day := row.AddDate(0, 0, col)
			cell := image.Rect(r.margin+col*colW, gridTop+w*rowH, r.margin+(col+1)*colW, gridTop+(w+1)*rowH)
			cells[col] = cell.Inset(r.s(2))
			r.monthCell(v, l, day, cells[col], day.Month() == first.Month(), day.Before(today), sameDay(day, today))
			r.calHitAdd(calHit{r: cell, kind: calDay, day: day})
		}
		r.monthWeekEvents(v, l, row, cells, today)
	}
	if !v.loaded {
		msg := tr("reading_the_calendar")
		r.text(r.tiny, msg, r.w-r.margin-r.width(r.tiny, msg), top+r.s(20), dim)
	}
}

// monthRoomy is whether the month's cells are tall enough for the bigger panel's numbers and lines: on
// a panel as tall for its width as the Show 8's. The Skylight Calendar's 1920 by 1080 is wide but
// hardly taller for its width than the Show 5, and there the bigger numbers left a day with two
// events a single line, "+2 more" and nothing else; it keeps the Show 5's spacing, scaled, and the
// rest of what a bigger panel shows.
func (r *renderer) monthRoomy(l calLook) bool { return l.wide && r.h*10 >= r.w*6 }

// monthNumber is the day's number's face, and its baseline below the cell's top: a little smaller on
// the Show 5, where the cells are short.
func (r *renderer) monthNumber(l calLook) (face numFaces, base int) {
	if r.monthRoomy(l) {
		return numFaces{r.tiny, r.textFace(true, 26)}, r.s(25)
	}
	return numFaces{r.textFace(false, 22), r.textFace(true, 22)}, r.s(20)
}

// monthCell is one day's box, its number and, in full, its weather.
func (r *renderer) monthCell(v calendarView, l calLook, day time.Time, in image.Rectangle, inMonth, past, today bool) {
	bg := calWash(10)
	switch {
	case today:
		bg = lerp(walnut, amber, 0.12)
	case past && inMonth:
		bg = calWash(5)
	case past:
		bg = calWash(3)
	case !inMonth:
		bg = calWash(4)
	}
	r.roundRect(in, r.s(8), bg)

	nf, nb := r.monthNumber(l)
	num, nx, base := strconv.Itoa(day.Day()), in.Min.X+r.s(8), in.Min.Y+nb
	if today {
		w := r.width(nf.bold, num)
		hh := r.s(15)
		if !r.monthRoomy(l) {
			hh = r.s(23) / 2
		}
		cy := base - capHeight(nf.bold)/2
		pill := image.Rect(nx-r.s(5), cy-hh, nx+w+r.s(5), cy+hh)
		r.roundRect(pill, hh, amber)
		r.text(nf.bold, num, nx, base, walnut)
	} else {
		ink := cream
		if past || !inMonth {
			ink = dim
		}
		r.text(nf.face, num, nx, base, ink)
	}
	// The coming week's weather, in full: an icon in the corner, and its high where there is room.
	if ahead := daysBetween(v.now, day); l.monthFull && len(v.forecast) > 0 && ahead >= 0 && ahead < 7 {
		if d, ok := dayForecast(v.forecast, day); ok {
			name, c := weatherGlyph(d.Condition)
			ix := in.Max.X - r.s(14)
			r.mdiAt(name, ix, base-capHeight(nf.face)/2, 20, c)
			if l.wide {
				t := strconv.FormatFloat(d.High, 'f', 0, 64) + "°"
				r.text(r.micro, t, ix-r.s(14)-r.width(r.micro, t), base-r.s(2), dim)
			}
		}
	}
}

// numFaces is a regular face and its bold.
type numFaces struct{ face, bold font.Face }

// monthWeekEvents draws a week's events over its cells, once every cell is drawn so a bar can run
// across them. Each bar keeps its lane across the week; a day's own events come under the lanes it
// has, as many as fit, and "+2 more" for the rest.
func (r *renderer) monthWeekEvents(v calendarView, l calLook, row time.Time, cells []image.Rectangle, today time.Time) {
	lh, ev0 := r.s(19), r.s(33)
	if !r.monthRoomy(l) {
		lh, ev0 = r.s(18), r.s(24)
	}
	spans := weekSpans(v.events, row, 7)
	lines := make([]int, 7)        // how many lines a day has room for
	lanes := make([]int, 7)        // how many lanes of bars cross it
	own := make([][]hass.Event, 7) // its events of its own, not in a bar
	for col := range 7 {
		lines[col] = max((cells[col].Max.Y-r.s(2)-(cells[col].Min.Y+ev0))/lh, 0)
		for _, sp := range spans {
			if sp.from <= col && col <= sp.to {
				lanes[col] = max(lanes[col], sp.lane+1)
			}
		}
		for _, e := range eventsOn(v.events, row.AddDate(0, 0, col)) {
			if !e.AllDay && !multiDay(e) {
				own[col] = append(own[col], e)
			}
		}
	}
	// cut is the lines a day shows before its "+N more": all of them when everything fits.
	cut := make([]int, 7)
	for col := range 7 {
		cut[col] = lines[col]
		if lanes[col]+len(own[col]) > lines[col] {
			cut[col] = max(lines[col]-1, 0)
		}
	}
	more := make([]int, 7)
	moreAt := slices.Clone(cut)
	// The bars: one that would cross a day without room for its lane is left out of all of them.
	for _, sp := range spans {
		room := true
		for c := sp.from; c <= sp.to; c++ {
			room = room && sp.lane < cut[c]
		}
		if !room {
			for c := sp.from; c <= sp.to; c++ {
				more[c]++
				moreAt[c] = min(moreAt[c], sp.lane)
			}
			continue
		}
		r.monthBar(v, l, sp, cells, ev0, lh, row.AddDate(0, 0, sp.to).Before(today))
	}
	// Each day's own events, under its lanes.
	for col := range 7 {
		day := row.AddDate(0, 0, col)
		past := day.Before(today)
		in := cells[col]
		line := lanes[col]
		for i, e := range own[col] {
			if line >= cut[col] {
				more[col] += len(own[col]) - i
				break
			}
			// Spare lines, after this one and the rest of the day's, let a title take a second line.
			spare := cut[col] - line - (len(own[col]) - i)
			used := r.monthTimed(v, l, e, in, in.Min.Y+ev0+line*lh, lh, past, spare > 0)
			line += used
		}
		if more[col] > 0 {
			// After the day's events shown, or in the lane of a bar left out, which is empty here.
			at := min(line, moreAt[col])
			r.monthMore(more[col], in, in.Min.Y+ev0+at*lh, lh)
		}
	}
}

// monthBar draws a bar of several days, or an all-day event's bar on its day.
func (r *renderer) monthBar(v calendarView, l calLook, sp span, cells []image.Rectangle, ev0, lh int, past bool) {
	fade := func(c color.RGBA) color.RGBA {
		if past {
			return calFade(c, 0.5)
		}
		return c
	}
	c := v.color(sp.ev.Calendar)
	y := cells[sp.from].Min.Y + ev0 + sp.lane*lh
	b := image.Rect(cells[sp.from].Min.X+r.s(4), y, cells[sp.to].Max.X-r.s(4), y+lh-r.s(2))
	if sp.before {
		b.Min.X = cells[sp.from].Min.X
	}
	fill := fade(evFill(c))
	r.roundRect(b, r.s(5), fill)
	if sp.before {
		r.fillRect(image.Rect(b.Min.X, b.Min.Y, b.Min.X+r.s(6), b.Max.Y), fill)
	}
	if sp.after {
		// Running on past the week or the month: a pointed end at the edge.
		mid := (b.Min.Y + b.Max.Y) / 2
		r.fillRect(image.Rect(b.Max.X-r.s(6), b.Min.Y, b.Max.X, b.Max.Y), fill)
		r.fillShape(image.Rect(b.Max.X, b.Min.Y, b.Max.X+r.s(10), b.Max.Y), fill, func(z *vector.Rasterizer, w, h float32) {
			z.MoveTo(0, 0)
			z.LineTo(w, h/2)
			z.LineTo(0, h)
			z.ClosePath()
		})
		r.mdiAt("chevron-right", b.Max.X-r.s(6), mid, 18, fade(cream))
	}
	if !sp.before {
		r.fillRect(image.Rect(b.Min.X, b.Min.Y+r.s(3), b.Min.X+r.s(4), b.Max.Y-r.s(3)), fade(c))
	}
	tx := b.Min.X + r.s(9)
	if icon := keywordIcon(sp.ev.Summary); icon != "" && l.icons && l.monthFull && l.wide {
		r.mdiAt(icon, tx+r.s(7), (b.Min.Y+b.Max.Y)/2, 15, fade(cream))
		tx += r.s(18)
	}
	room := b.Max.X - tx - r.s(6)
	if sp.after {
		room -= r.s(20)
	}
	label := clipText(r, r.micro, sp.ev.Summary, room)
	r.text(r.micro, label, tx, b.Max.Y-r.s(4), fade(cream))
	if sp.after && multiDay(sp.ev) {
		extra := "  until " + lastDay(sp.ev).Format("Jan 2")
		if lw := r.width(r.micro, label); label == sp.ev.Summary && lw+r.width(r.micro, extra) < room {
			r.text(r.micro, extra, tx+lw, b.Max.Y-r.s(4), fade(lerp(cream, evFill(c), 0.35)))
		}
	}
	r.calHitAdd(calHit{r: b, kind: calEvent, ev: sp.ev})
}

// monthTimed draws one of a day's own events from y, and says how many lines it took: in full its start
// time in its calendar's color and its title, on a second line when there is one to spare on a bigger
// panel; simple, a bar of its color.
func (r *renderer) monthTimed(v calendarView, l calLook, e hass.Event, in image.Rectangle, y, lh int, past, spare bool) int {
	fade := func(c color.RGBA) color.RGBA {
		if past {
			return calFade(c, 0.5)
		}
		return c
	}
	c := v.color(e.Calendar)
	if !l.monthFull {
		bar := image.Rect(in.Min.X+r.s(4), y, in.Max.X-r.s(4), y+lh-r.s(2))
		r.roundRect(bar, r.s(5), fade(evFill(c)))
		r.fillRect(image.Rect(bar.Min.X, bar.Min.Y+r.s(3), bar.Min.X+r.s(4), bar.Max.Y-r.s(3)), fade(c))
		r.text(r.micro, clipText(r, r.micro, e.Summary, bar.Dx()-r.s(12)), bar.Min.X+r.s(8), bar.Max.Y-r.s(4), fade(cream))
		r.calHitAdd(calHit{r: bar, kind: calEvent, ev: e})
		return 1
	}
	base := y + lh - r.s(5)
	tx := in.Min.X + r.s(6)
	bold := r.textFace(true, 18)
	tm := shortClock(e.Start)
	icon := ""
	if l.icons && l.wide {
		icon = keywordIcon(e.Summary)
	}
	if icon != "" {
		// The icon gives way when it would push the title's first word off the line beside the time.
		room := in.Max.X - r.s(4) - tx - r.width(bold, tm) - r.s(5)
		first := r.calWrap(r.micro, e.Summary, room-r.s(17))
		if len(first) > 0 && r.width(r.micro, first[0]) > room-r.s(17) && r.width(r.micro, first[0]) <= room {
			icon = ""
		}
	}
	if icon != "" {
		r.mdiAt(icon, tx+r.s(7), y+(lh-r.s(2))/2, 16, fade(c))
		tx += r.s(17)
	}
	left := in.Min.X + r.s(6)
	r.text(bold, tm, tx, base, fade(timeInk(c)))
	tx += r.width(bold, tm) + r.s(5)
	room := in.Max.X - r.s(4) - tx
	used := 1
	words := r.calWrap(r.micro, e.Summary, room)
	if len(words) > 1 && spare && l.wide {
		rest := e.Summary
		if r.width(r.micro, words[0]) <= room {
			r.text(r.micro, words[0], tx, base, fade(cream))
			rest = restOf(strings.Join(strings.Fields(e.Summary), " "), words[:1])
		}
		used = 2
		r.text(r.micro, clipText(r, r.micro, rest, in.Max.X-r.s(4)-left), left, base+lh, fade(cream))
	} else {
		r.text(r.micro, clipText(r, r.micro, e.Summary, room), tx, base, fade(cream))
	}
	r.calHitAdd(calHit{r: image.Rect(in.Min.X, y, in.Max.X, y+used*lh), kind: calEvent, ev: e})
	return used
}

// monthMore is a day's "+2 more", for the events there was no room for; a tap on the day lists them.
func (r *renderer) monthMore(n int, in image.Rectangle, y, lh int) {
	t := "+" + strconv.Itoa(n) + " more"
	w := r.width(r.micro, t) + r.s(14)
	b := image.Rect(in.Min.X+r.s(4), y+r.s(1), in.Min.X+r.s(4)+w, y+lh-r.s(2))
	if b.Max.X > in.Max.X {
		b.Max.X = in.Max.X
	}
	r.roundRect(b, b.Dy()/2, lerp(walnut, amber, 0.22))
	ink := amber
	if !dark() {
		ink = cream
	}
	r.text(r.micro, t, b.Min.X+r.s(7), b.Max.Y-r.s(4), ink)
}
