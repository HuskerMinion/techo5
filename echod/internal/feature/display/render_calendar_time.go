//go:build !dot && !spot

package display

import (
	"image"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// The day and the week on their hours: hours down the left, each event a box as long as it lasts,
// those at the same time side by side, and the all-day events in a strip under the header. Today has
// the now line across it and its hours gone by shaded.

// timeline is the day's hours as last drawn, for a swipe or the Later pill to move them: the hour the
// page opened at, how many whole hours show, and how far from that hour they can be moved either way.
type timeline struct {
	auto      float64
	shown     int
	low, high int
}

// atHour is day at hour h.
func atHour(day time.Time, h int) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), h, 0, 0, 0, time.Local)
}

// hoursOf is how far into its day t is, in hours.
func hoursOf(t time.Time) float64 {
	return float64(t.Hour()) + float64(t.Minute())/60 + float64(t.Second())/3600
}

// dayHourHeight is an hour's height on the day's hours: more on a panel as wide as the Show 5's for its
// height, where fewer hours have to fit.
func (r *renderer) dayHourHeight() int {
	if float64(r.h)/float64(r.w) < 0.55 {
		return r.s(42)
	}
	return r.s(34)
}

// autoStart is the hour a day opens at: an hour before now on today, an hour before its first timed
// event on another day, eight in the morning on a day with none.
func autoStart(evs []hass.Event, day, now time.Time) float64 {
	if sameDay(day, now) {
		return math.Max(float64(now.Hour()-1), 0)
	}
	for _, p := range layoutDay(evs, day, 0) {
		return math.Max(math.Floor(p.s0)-1, 0)
	}
	return 8
}

// timelineFor is the hours a page of shown hours shows, opened at auto and moved by scroll, kept within
// the day, and how far it can move. The opening hour is first kept to the latest a page can start at,
// so late in the evening the page opens on the day's last hours and a swipe moves it a full step: the
// limits are worked out from where the page really is.
func timelineFor(auto, shown float64, scroll int) (start float64, t timeline) {
	most := math.Max(24-shown, 0)
	auto = math.Min(math.Max(auto, 0), most)
	t = timeline{auto: auto, shown: max(int(shown), 1)}
	t.low, t.high = -int(math.Ceil(auto)), int(math.Ceil(most-auto))
	t.high = max(t.high, t.low)
	start = math.Min(math.Max(auto+float64(min(max(scroll, t.low), t.high)), 0), most)
	return start, t
}

// calendarTimeline is a day on its hours.
func (r *renderer) calendarTimeline(v calendarView, l calLook) {
	today := sameDay(v.day, v.now)
	x := r.calButton(tr("done"), r.w-r.margin, calBack)
	x = r.calIconButton("chevron-right", "›", x, calNext)
	x = r.calIconButton("chevron-left", "‹", x, calPrev)
	title := clipText(r, r.title, locale.LongDate(v.day, screenLang()), x-r.margin-r.s(10))
	r.text(r.title, title, r.margin, r.margin+r.s(34), cream)
	if today {
		tx := r.margin + r.width(r.title, title) + r.s(16)
		tw := r.width(r.tiny, tr("today")) + r.s(24)
		if tx+tw < x {
			cy := r.margin + r.s(17)
			r.roundRect(image.Rect(tx, cy-r.s(16), tx+tw, cy+r.s(16)), r.s(16), amber)
			r.text(r.tiny, tr("today"), tx+r.s(12), cy+r.s(9), walnut)
		}
	}

	gx := r.margin + r.s(74) // the hours' left edge, after their labels
	right := r.w - r.margin
	evs := eventsOn(v.events, v.day)

	// The all-day strip, with the day's weather at its right end.
	sy0, sy1 := r.margin+r.s(58), r.margin+r.s(92)
	r.text(r.micro, tr("all_day"), r.margin, sy0+r.s(23), dim)
	wx := r.dayWeather(v.forecast, v.day, right, sy0+r.s(25), r.tiny, 28, true)
	ax := gx + r.s(6)
	var allDay []hass.Event
	for _, e := range evs {
		if e.AllDay {
			allDay = append(allDay, e)
		}
	}
	if len(allDay) == 0 {
		r.text(r.micro, "—", ax, sy0+r.s(23), calFade(dim, 0.4))
	}
	for i, e := range allDay {
		c := v.color(e.Calendar)
		label := e.Summary
		w := r.width(r.tiny, label) + r.s(28)
		room := wx - r.s(16) - ax
		if i < len(allDay)-1 {
			room -= r.s(60) // room left for the "+N" of those after it
		}
		if w > room {
			if room < r.s(90) {
				more := "+" + strconv.Itoa(len(allDay)-i)
				r.text(r.tiny, more, ax, sy1-r.s(9), amber)
				r.calHitAdd(calHit{r: image.Rect(ax, sy0, ax+r.width(r.tiny, more), sy1), kind: calEvent, ev: e})
				break
			}
			w, label = room, clipText(r, r.tiny, label, room-r.s(28))
		}
		b := image.Rect(ax, sy0, ax+w, sy1)
		r.roundRect(b, r.s(8), evFill(c))
		r.roundRect(image.Rect(b.Min.X, b.Min.Y+r.s(5), b.Min.X+r.s(5), b.Max.Y-r.s(5)), r.s(2), c)
		r.text(r.tiny, label, b.Min.X+r.s(16), b.Max.Y-r.s(9), cream)
		r.calHitAdd(calHit{r: b, kind: calEvent, ev: e})
		ax += w + r.s(8)
	}

	top, bottom := sy1+r.s(12), r.h-r.s(8)
	hourH := r.dayHourHeight()
	start, tl := timelineFor(autoStart(evs, v.day, v.now), float64(bottom-top)/float64(hourH), v.scroll)
	end := start + float64(bottom-top)/float64(hourH)
	r.calMu.Lock()
	r.calTimeline = tl
	r.calMu.Unlock()
	ytime := func(h float64) int { return top + int(math.Round((h-start)*float64(hourH))) }

	nowH := hoursOf(v.now)
	nowY := ytime(nowH)
	if today && nowY > top {
		r.fillRect(image.Rect(gx, top, right, min(nowY, bottom)), calWash(5))
	}
	half := lerp(walnut, ember, 0.45)
	for h := int(math.Ceil(start)); float64(h) <= end && h <= 24; h++ {
		y := ytime(float64(h))
		if float64(h)-0.5 >= start {
			yh := ytime(float64(h) - 0.5)
			r.fillRect(image.Rect(gx, yh, right, yh+max(r.s(1), 1)), half)
		}
		r.fillRect(image.Rect(gx, y, right, y+max(r.s(1), 1)), ember)
		if h < 24 && !(today && abs(nowY-y) < r.s(16)) {
			lab := hourLabel(h)
			r.text(r.micro, lab, gx-r.s(10)-r.width(r.micro, lab), y+r.s(6), dim)
		}
	}

	var later []placed
	inner := right - r.s(14) - (gx + r.s(6))
	// Laid out as drawn: a box is at least a line tall, with a little room under it.
	least := float64(r.s(minBlockPx)+r.s(3)) / float64(hourH)
	for _, p := range layoutDay(evs, v.day, least) {
		s0, s1 := p.s0, p.s1
		if s0 >= end {
			later = append(later, p)
			continue
		}
		if s1 <= start {
			continue
		}
		colW := inner / p.cols
		b := image.Rect(gx+r.s(6)+p.col*colW, max(ytime(s0)+r.s(1), top), gx+r.s(6)+(p.col+1)*colW, min(ytime(s1)-r.s(2), bottom))
		if p.cols > 1 && p.col < p.cols-1 {
			b.Max.X -= r.s(4)
		}
		r.timeBlock(v, l, p.ev, minBlockRect(b, r.s(minBlockPx), top, bottom), false)
	}

	if today && nowH >= start && nowH <= end {
		r.fxLine(float64(gx), float64(nowY), float64(right), float64(nowY), r.sf(5)/2, amber, 1)
		r.aaDisc(float64(gx), float64(nowY), r.sf(6), amber)
		lab := nowLabel(v.now)
		r.text(r.micro, lab, gx-r.s(12)-r.width(r.micro, lab), nowY+r.s(6), amber)
	}
	r.scrollBar(top, bottom, start/24, (end-start)/24)
	if len(later) > 0 {
		e := later[0].ev
		t := "▼  Later: " + e.Summary + " · " + clockText(e.Start)
		maxW := right - gx - r.s(60)
		t = clipText(r, r.micro, t, maxW)
		w := r.width(r.micro, t) + r.s(28)
		b := image.Rect(right-r.s(14)-w, bottom-r.s(36), right-r.s(14), bottom-r.s(6))
		r.calMicroButton(b, t)
		r.calHitAdd(calHit{r: b.Inset(-r.s(6)), kind: calLater, ev: e})
	}
	if len(evs) == 0 && !v.loaded {
		msg := tr("reading_the_calendar")
		r.text(r.tiny, msg, gx+r.s(20), top+r.s(40), dim)
	}
}

// minBlockPx is the least an event is drawn on the hours, in the Show 5's pixels: a line's height, so
// a short event, or one in a week of many hours, is still there to read and to tap.
const minBlockPx = 18

// minBlockRect is b at least least tall, kept between top and bottom.
func minBlockRect(b image.Rectangle, least, top, bottom int) image.Rectangle {
	if b.Dy() >= least {
		return b
	}
	b.Max.Y = b.Min.Y + least
	if b.Max.Y > bottom {
		b.Min.Y, b.Max.Y = max(bottom-least, top), bottom
	}
	return b
}

// blockHours is where an event's part of a day starts and ends on its hours. By the clock, except that
// one running to the day's end ends at 24, and one the clocks going back made shorter on the clock than
// it lasts (1:30 to 1:30 again, an hour later) is as long as it lasts.
func blockHours(p placed, day time.Time) (s0, s1 float64) {
	s0, s1 = hoursOf(p.from), hoursOf(p.till)
	if !sameDay(p.till, day) {
		s1 = 24
	}
	if lasts := p.till.Sub(p.from).Hours(); s1-s0 < lasts {
		s1 = math.Min(s0+lasts, 24)
	}
	return s0, s1
}

// weekHours is the week's hours: 7 AM to 9 PM, and an hour more either way when an event needs it.
func weekHours(events []hass.Event, first time.Time) (startH, endH int) {
	startH, endH = 7, 21
	for i := range 7 {
		day := first.AddDate(0, 0, i)
		for _, p := range layoutDay(events, day, 0) {
			startH = min(startH, max(int(math.Floor(p.s0)), 6))
			endH = max(endH, min(int(math.Ceil(p.s1)), 22))
		}
	}
	return startH, endH
}

// weekMarker is a day's events before or after the week's hours, at the top or the foot of its column:
// the one's title, or how many; a tap opens the day, which has all its hours.
func (r *renderer) weekMarker(evs []hass.Event, day time.Time, b image.Rectangle, arrow string) {
	if len(evs) == 0 {
		return
	}
	text := evs[0].Summary
	if len(evs) > 1 {
		text = strconv.Itoa(len(evs)) + " more"
	}
	lead := arrow + " "
	room := b.Dx() - r.s(12) - r.width(r.micro, lead)
	r.calMicroButton(b, lead+clipText(r, r.micro, text, room))
	r.calHitAdd(calHit{r: b, kind: calDay, day: day})
}

// calMicroButton draws one of the calendar's compact controls in b — the day's Later pill, a week's
// off-hours marker — as the program's button, its words centered. They stay in the page's micro face
// because the box is measured from them, and the sheet's button face is wider than that box.
func (r *renderer) calMicroButton(b image.Rectangle, label string) {
	r.buttonFace(b, float64(b.Dy())/2, btnSecondary)
	r.text(r.micro, label, b.Min.X+(b.Dx()-r.width(r.micro, label))/2,
		b.Min.Y+(b.Dy()+r.micro.Metrics().Ascent.Ceil()-r.micro.Metrics().Descent.Ceil())/2, amber)
}

// laterScroll is the scroll that shows an event on the day's hours as last drawn: its hour a little
// below the top.
func (t timeline) laterScroll(e hass.Event) int {
	return min(max(int(math.Floor(math.Floor(hoursOf(e.Start))-1-t.auto)), t.low), t.high)
}

// timeBlock draws an event's box on the hours: its title and, where there is room, when and where. week
// says it is in the week's narrow columns, where the title wraps rather than sharing its line.
func (r *renderer) timeBlock(v calendarView, l calLook, e hass.Event, b image.Rectangle, week bool) {
	if b.Dx() < r.s(12) {
		return
	}
	c := v.color(e.Calendar)
	r.roundRect(b, r.s(min(8, b.Dy()/2)), evFill(c))
	sub := evSub(c)
	if week {
		r.roundRect(image.Rect(b.Min.X+r.s(4), b.Min.Y+r.s(4), b.Min.X+r.s(8), b.Max.Y-r.s(4)), r.s(2), c)
	} else {
		r.roundRect(image.Rect(b.Min.X+r.s(6), b.Min.Y+r.s(6), b.Min.X+r.s(11), b.Max.Y-r.s(6)), r.s(2), c)
	}
	r.calHitAdd(calHit{r: b, kind: calEvent, ev: e})
	when := clockText(e.Start) + " – " + clockText(e.End)
	if !e.End.After(e.Start) {
		when = clockText(e.Start)
	}
	icon := ""
	if l.icons {
		icon = keywordIcon(e.Summary)
	}
	if week {
		tx := b.Min.X + r.s(13)
		tw := b.Max.X - tx - r.s(4)
		lh := r.s(19)
		if b.Dy() < r.s(30) {
			base := (b.Min.Y+b.Max.Y)/2 + capHeight(r.micro)/2
			if icon != "" && tw > r.s(60) {
				r.mdiAt(icon, tx+r.s(7), (b.Min.Y+b.Max.Y)/2, 15, cream)
				tx += r.s(18)
				tw -= r.s(18)
			}
			r.text(r.micro, clipText(r, r.micro, e.Summary, tw), tx, base, cream)
			return
		}
		room := max((b.Dy()-r.s(6))/lh, 1)
		title := strings.Join(strings.Fields(e.Summary), " ")
		firstW := tw
		if icon != "" && tw > r.s(60) {
			r.mdiAt(icon, tx+r.s(7), b.Min.Y+r.s(13), 15, cream)
			firstW -= r.s(18)
		}
		lines := r.calWrap(r.micro, title, firstW)
		shown := lines[:min(len(lines), room)]
		if len(lines) > room {
			w := tw
			if room == 1 {
				w = firstW
			}
			shown[room-1] = clipText(r, r.micro, restOf(title, shown[:room-1]), w)
		}
		y := b.Min.Y + r.s(19)
		for i, ln := range shown {
			lx := tx
			w := tw
			if i == 0 && icon != "" && tw > r.s(60) {
				lx, w = tx+r.s(18), firstW
			}
			r.text(r.micro, clipText(r, r.micro, ln, w), lx, y, cream)
			y += lh
		}
		extra := []string{shortClock(e.Start) + " – " + shortClock(e.End)}
		if e.Location != "" {
			extra = append(extra, e.Location)
		}
		for _, ln := range extra[:min(len(extra), max(room-len(shown), 0))] {
			r.text(r.micro, clipText(r, r.micro, ln, tw), tx, y, sub)
			y += lh
		}
		return
	}
	if e.Location != "" {
		when += "  ·  " + e.Location
	}
	tx := b.Min.X + r.s(20)
	tw := b.Max.X - tx - r.s(8)
	if b.Dy() >= r.s(46) {
		if icon != "" {
			r.mdiAt(icon, tx+r.s(9), b.Min.Y+r.s(19), 20, cream)
			tx += r.s(24)
			tw -= r.s(24)
		}
		r.text(r.tiny, clipText(r, r.tiny, e.Summary, tw), tx, b.Min.Y+r.s(27), cream)
		r.text(r.micro, clipText(r, r.micro, when, b.Max.X-r.s(28)-b.Min.X), b.Min.X+r.s(20), b.Min.Y+r.s(48), sub)
		return
	}
	base := (b.Min.Y+b.Max.Y)/2 + capHeight(r.micro)/2
	tx = b.Min.X + r.s(18)
	if icon != "" {
		r.mdiAt(icon, tx+r.s(7), (b.Min.Y+b.Max.Y)/2, 15, cream)
		tx += r.s(18)
		tw -= r.s(16)
	}
	title := clipText(r, r.micro, e.Summary, tw)
	r.text(r.micro, title, tx, base, cream)
	if rest := tw - r.width(r.micro, title) - r.s(12); rest > r.s(40) {
		r.text(r.micro, clipText(r, r.micro, when, rest), tx+r.width(r.micro, title)+r.s(12), base, sub)
	}
}

// restOf is what is left of title after its first lines, as calWrap broke them: each line is the
// title's next words, with the space between two lines left out.
func restOf(title string, lines []string) string {
	for _, ln := range lines {
		title = strings.TrimPrefix(title, ln)
		title = strings.TrimPrefix(title, " ")
	}
	return title
}

// weekStart is the Sunday of the week holding day.
func weekStart(day time.Time) time.Time {
	d := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.Local)
	return d.AddDate(0, 0, -int(d.Weekday()))
}

// weekTitle is the week's title: "October 18 – 24", or shorter, "Oct 18 – 24"; across two months,
// "October 25 – November 1".
func weekTitle(first time.Time, short bool) string {
	last := first.AddDate(0, 0, 6)
	layout := "January 2"
	if short {
		layout = "Jan 2"
	}
	if first.Month() == last.Month() {
		return first.Format(layout) + " – " + last.Format("2")
	}
	return first.Format(layout) + " – " + last.Format(layout)
}

// calendarWeek is the week on its hours: a column a day, Sunday first, under its name, its number and
// its weather; today's column tinted and the now line across it.
func (r *renderer) calendarWeek(v calendarView, l calLook) {
	first := weekStart(v.day)
	x := r.calButton(tr("done"), r.w-r.margin, calDone)
	x = r.calIconButton("chevron-right", "›", x, calNext)
	x = r.calIconButton("chevron-left", "‹", x, calPrev)
	x = r.calSegmented(calViewWeek, x, true)
	title := weekTitle(first, false)
	if r.margin+r.width(r.title, title) > x-r.s(10) {
		title = clipText(r, r.title, weekTitle(first, true), x-r.margin-r.s(10))
	}
	r.text(r.title, title, r.margin, r.margin+r.s(34), cream)

	gx := r.margin + r.s(64)
	right := r.w - r.margin
	colW := (right - gx) / 7
	hy := r.margin + r.s(54)
	hdrH := r.s(58)
	allTop := hy + hdrH + r.s(4)
	allH := r.s(28)
	// The strip has the all-day events; a timed one running past midnight is on the hours of each of
	// its days, as the day's timeline has it, and not in the strip as well.
	var allDay []hass.Event
	for _, e := range v.events {
		if e.AllDay {
			allDay = append(allDay, e)
		}
	}
	spans := weekSpans(allDay, first, 7)
	lanes, most := 0, 2
	if l.wide {
		most = 3
	}
	for _, sp := range spans {
		lanes = max(lanes, sp.lane+1)
	}
	strip := max(min(lanes, most), 1)
	top := allTop + strip*allH + r.s(10)
	bottom := r.h - r.s(10)

	// The hours: seven in the morning to nine at night, an hour wider either way for an event outside
	// them, and no wider, so an hour stays tall enough to read; an event further out is a marker at the
	// top or the foot of its day, which opens that day.
	startH, endH := weekHours(v.events, first)
	// The markers have bands of their own, above the hours and under them, so none covers an event's box.
	band := r.s(minBlockPx) + r.s(4)
	hoursTop, hoursBottom := top, bottom
	for i := range 7 {
		for _, p := range layoutDay(v.events, first.AddDate(0, 0, i), 0) {
			if p.s1 <= float64(startH) && hoursTop == top {
				hoursTop += band
			}
			if p.s0 >= float64(endH) && hoursBottom == bottom {
				hoursBottom -= band
			}
		}
	}
	hourH := float64(hoursBottom-hoursTop) / float64(endH-startH)
	ytime := func(h float64) int { return hoursTop + int(math.Round((h-float64(startH))*hourH)) }

	bold := r.textFace(true, 26)
	for i := range 7 {
		day := first.AddDate(0, 0, i)
		cx0 := gx + i*colW
		mid := cx0 + colW/2
		today := sameDay(day, v.now)
		if today {
			r.roundRect(image.Rect(cx0+r.s(2), hy, cx0+colW-r.s(2), bottom), r.s(10), lerp(walnut, amber, 0.09))
		}
		wd, num := day.Format("Mon"), day.Format("2")
		nf := r.tiny
		if today {
			nf = bold
		}
		w1 := r.width(r.tiny, wd) + r.s(10) + r.width(nf, num)
		lx := mid - w1/2
		ink := dim
		if today {
			ink = amber
		}
		r.text(r.tiny, wd, lx, hy+r.s(26), ink)
		nx := lx + r.width(r.tiny, wd) + r.s(10)
		if today {
			nw := r.width(bold, num)
			r.roundRect(image.Rect(nx-r.s(6), hy+r.s(4), nx+nw+r.s(6), hy+r.s(34)), r.s(15), amber)
			r.text(bold, num, nx, hy+r.s(28), walnut)
		} else {
			r.text(r.tiny, num, nx, hy+r.s(26), cream)
		}
		if d, ok := dayForecast(v.forecast, day); ok {
			t := highLow(d)
			w2 := r.s(24) + r.width(r.micro, t)
			lx := mid - w2/2
			name, c := weatherGlyph(d.Condition)
			r.mdiAt(name, lx+r.s(9), hy+r.s(46), 20, c)
			r.text(r.micro, t, lx+r.s(24), hy+r.s(52), dim)
		}
		r.calHitAdd(calHit{r: image.Rect(cx0, hy-r.s(4), cx0+colW, hy+hdrH), kind: calDay, day: day})
	}

	r.text(r.micro, tr("all_day"), r.margin, allTop+r.s(19), dim)
	half := lerp(walnut, ember, 0.4)
	line := max(r.s(1), 1)
	for h := startH; h <= endH; h++ {
		y := ytime(float64(h))
		r.fillRect(image.Rect(gx, y, right, y+line), ember)
		if h < endH {
			yh := ytime(float64(h) + 0.5)
			r.fillRect(image.Rect(gx, yh, right, yh+line), half)
		}
		// Every hour's name where an hour is taller than a line of them, else every other hour's.
		if hourH >= float64(r.s(22)) || (h-startH)%2 == 0 {
			lab := hourLabel(h)
			r.text(r.micro, lab, gx-r.s(8)-r.width(r.micro, lab), y+r.s(6), dim)
		}
	}
	for i := range 8 {
		xx := gx + i*colW
		r.fillRect(image.Rect(xx, hy+hdrH, xx+line, bottom), ember)
	}

	// The all-day strip: a bar for each event, across its days.
	hiddenLanes := 0
	for _, sp := range spans {
		if sp.lane >= strip {
			hiddenLanes++
			continue
		}
		c := v.color(sp.ev.Calendar)
		y := allTop + sp.lane*allH
		b := image.Rect(gx+sp.from*colW+r.s(4), y, gx+(sp.to+1)*colW-r.s(4), y+allH-r.s(2))
		r.roundRect(b, r.s(6), evFill(c))
		r.roundRect(image.Rect(b.Min.X+r.s(4), b.Min.Y+r.s(5), b.Min.X+r.s(8), b.Max.Y-r.s(5)), r.s(2), c)
		tx := b.Min.X + r.s(13)
		if icon := keywordIcon(sp.ev.Summary); icon != "" && l.icons && b.Dx() > r.s(90) {
			r.mdiAt(icon, tx+r.s(7), (b.Min.Y+b.Max.Y)/2, 15, cream)
			tx += r.s(18)
		}
		label := sp.ev.Summary
		if sp.after {
			label += "  →"
		}
		r.text(r.micro, clipText(r, r.micro, label, b.Max.X-tx-r.s(6)), tx, b.Max.Y-r.s(7), cream)
		r.calHitAdd(calHit{r: b, kind: calEvent, ev: sp.ev})
	}
	if hiddenLanes > 0 {
		r.text(r.micro, "+"+strconv.Itoa(hiddenLanes), r.margin, allTop+r.s(19)+allH, amber)
	}

	for i := range 7 {
		day := first.AddDate(0, 0, i)
		cx0 := gx + i*colW
		var earlier, later []hass.Event
		least := float64(r.s(minBlockPx)+r.s(3)) / hourH
		for _, p := range layoutDay(v.events, day, least) {
			s0, s1 := p.s0, p.s1
			switch {
			case s1 <= float64(startH):
				earlier = append(earlier, p.ev)
				continue
			case s0 >= float64(endH):
				later = append(later, p.ev)
				continue
			}
			inner := colW - r.s(8)
			w := inner / p.cols
			b := image.Rect(cx0+r.s(4)+p.col*w, max(ytime(s0)+r.s(1), hoursTop), cx0+r.s(4)+(p.col+1)*w, min(ytime(s1)-r.s(2), hoursBottom))
			if p.cols > 1 && p.col < p.cols-1 {
				b.Max.X -= r.s(2)
			}
			r.timeBlock(v, l, p.ev, minBlockRect(b, r.s(minBlockPx), hoursTop, hoursBottom), true)
		}
		r.weekMarker(earlier, day, image.Rect(cx0+r.s(4), top+r.s(1), cx0+colW-r.s(4), top+r.s(1)+r.s(minBlockPx)), "▲")
		r.weekMarker(later, day, image.Rect(cx0+r.s(4), bottom-r.s(1)-r.s(minBlockPx), cx0+colW-r.s(4), bottom-r.s(1)), "▼")
	}

	// The now line: faint across the week, strong across today.
	if ti := daysBetween(first, v.now); ti >= 0 && ti < 7 {
		nh := hoursOf(v.now)
		if nh >= float64(startH) && nh <= float64(endH) {
			ny := ytime(nh)
			r.fxLine(float64(gx), float64(ny), float64(right), float64(ny), r.sf(1), calFade(amber, 0.6), 1)
			r.fxLine(float64(gx+ti*colW), float64(ny), float64(gx+(ti+1)*colW), float64(ny), r.sf(5)/2, amber, 1)
			r.aaDisc(float64(gx+ti*colW+r.s(2)), float64(ny), r.sf(6), amber)
		}
	}
	if !v.loaded {
		msg := tr("reading_the_calendar")
		r.text(r.tiny, msg, gx+r.s(20), top+r.s(40), dim)
	}
}
