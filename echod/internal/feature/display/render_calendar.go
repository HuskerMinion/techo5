//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strconv"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The calendar page (docs/calendar-and-night-plan.md): the month as a grid with each day's events on
// it, a day's events as a list, and one event's details in a window over either. The events are Home
// Assistant's, from the calendars this device shows, each calendar in its own color.

// calendarShow is how long the calendar stays up untouched.
const calendarShow = 2 * time.Minute

// calendarView is what the page shows.
type calendarView struct {
	month  time.Time    // the first of the month shown
	day    time.Time    // the day opened as a list; zero for the month
	detail *hass.Event  // the event opened in its window, over the month or the day
	scroll int          // how far down the day's list is scrolled, in rows
	events []hass.Event // the month's
	loaded bool         // whether the month's events have been read yet
	names  map[string]string
	order  []string // the calendars shown, in order, which picks their colors
	now    time.Time
}

func (v calendarView) color(cal string) color.RGBA { return calendarColor(v.order, cal) }

func (v calendarView) name(cal string) string {
	if n := v.names[cal]; n != "" {
		return n
	}
	return cal
}

// calHitKind is what a tap on the page landed on.
type calHitKind int

const (
	calNone    calHitKind = iota
	calPrev               // the month before
	calNext               // the month after
	calToday              // back to this month
	calDone               // put the calendar away
	calBack               // from a day back to its month
	calDay                // a day of the month: its list
	calEvent              // an event: its window
	calClose              // the window's Close
	calOutside            // anywhere outside the window: closes it
	calInside             // the window itself: nothing
)

type calHit struct {
	r    image.Rectangle
	kind calHitKind
	day  time.Time
	ev   hass.Event
}

func (r *renderer) calHitAdd(h calHit) {
	r.calMu.Lock()
	r.calHits = append(r.calHits, h)
	r.calMu.Unlock()
}

// calendarHit is what a tap at p landed on, as last drawn: the topmost thing there.
func (r *renderer) calendarHit(p image.Point) (calHit, bool) {
	r.calMu.Lock()
	defer r.calMu.Unlock()
	for i := len(r.calHits) - 1; i >= 0; i-- {
		if p.In(r.calHits[i].r) {
			return r.calHits[i], true
		}
	}
	return calHit{}, false
}

// calendarPage draws the calendar: the day's list or the month, and the event window over either.
func (r *renderer) calendarPage(s scene) {
	r.calMu.Lock()
	r.calHits = r.calHits[:0]
	r.calMu.Unlock()
	v := s.cal
	if v.day.IsZero() {
		r.calendarMonth(v)
	} else {
		r.calendarDay(v)
	}
	if v.detail != nil {
		r.calendarDetail(v, *v.detail)
	}
}

// calButton draws a button with its right edge at right, on the header line, and says where it is.
func (r *renderer) calButton(label string, right int, kind calHitKind) int {
	w := r.width(r.small, label) + r.s(36)
	b := image.Rect(right-w, r.margin-r.s(8), right, r.margin+r.s(40))
	r.roundRect(b, b.Dy()/2, color.NRGBA{255, 255, 255, 26})
	r.text(r.small, label, b.Min.X+r.s(18), b.Min.Y+r.s(36), cream)
	r.calHitAdd(calHit{r: b.Inset(-r.s(6)), kind: kind})
	return b.Min.X - r.s(14)
}

// calendarMonth is the month as a grid, Sunday first, each day with as much of its events as fits.
func (r *renderer) calendarMonth(v calendarView) {
	r.text(r.title, v.month.Format("January 2006"), r.margin, r.margin+r.s(34), cream)
	x := r.calButton("Done", r.w-r.margin, calDone)
	if v.month.Year() != v.now.Year() || v.month.Month() != v.now.Month() {
		x = r.calButton("Today", x, calToday)
	}
	x = r.calButton("›", x, calNext)
	r.calButton("‹", x, calPrev)

	top := r.margin + r.s(62)
	colW := (r.w - 2*r.margin) / 7
	for i := range 7 {
		label := time.Weekday(i).String()[:3]
		r.text(r.tiny, label, r.margin+i*colW+(colW-r.width(r.tiny, label))/2, top+r.s(22), dim)
	}
	gridTop := top + r.s(32)
	first := v.month
	start := first.AddDate(0, 0, -int(first.Weekday()))
	days := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, time.Local).Day()
	weeks := (int(first.Weekday()) + days + 6) / 7
	rowH := (r.h - r.s(10) - gridTop) / weeks
	barH, gap := r.s(21), r.s(3)
	for i := range weeks * 7 {
		day := start.AddDate(0, 0, i)
		cell := image.Rect(r.margin+(i%7)*colW, gridTop+(i/7)*rowH, r.margin+(i%7+1)*colW, gridTop+(i/7+1)*rowH)
		inner := cell.Inset(r.s(2))
		inMonth := day.Month() == first.Month()
		var bg color.Color = color.NRGBA{255, 255, 255, 10}
		if !inMonth {
			bg = color.NRGBA{255, 255, 255, 4}
		}
		today := sameDay(day, v.now)
		if today {
			bg = color.NRGBA{amber.R, amber.G, amber.B, 46}
		}
		r.roundRect(inner, r.s(8), bg)
		r.calHitAdd(calHit{r: cell, kind: calDay, day: day})

		num, numColor := strconv.Itoa(day.Day()), cream
		switch {
		case today:
			numColor = amber
		case !inMonth:
			numColor = dim
		}
		r.text(r.tiny, num, inner.Min.X+r.s(8), inner.Min.Y+r.s(24), numColor)

		evs := eventsOn(v.events, day)
		if len(evs) == 0 {
			continue
		}
		y := inner.Min.Y + r.s(31)
		fit := (inner.Max.Y - r.s(3) - y + gap) / (barH + gap)
		if fit < 1 {
			// No room for a bar: a mark for each, up to four, beside the day's number.
			for j, e := range evs[:min(len(evs), 4)] {
				c := v.color(e.Calendar)
				cx := inner.Max.X - r.s(10) - j*r.s(12)
				r.roundRect(image.Rect(cx-r.s(4), inner.Min.Y+r.s(12), cx+r.s(4), inner.Min.Y+r.s(20)), r.s(4), c)
			}
			continue
		}
		// As many as fit, and how many more beside the day's number.
		shown := evs[:min(len(evs), fit)]
		if more := len(evs) - len(shown); more > 0 {
			label := "+" + strconv.Itoa(more)
			r.text(r.micro, label, inner.Max.X-r.s(8)-r.width(r.micro, label), inner.Min.Y+r.s(22), amber)
		}
		for _, e := range shown {
			bar := image.Rect(inner.Min.X+r.s(4), y, inner.Max.X-r.s(4), y+barH)
			c := v.color(e.Calendar)
			r.roundRect(bar, r.s(5), color.RGBA{c.R / 2, c.G / 2, c.B / 2, 255})
			r.fillRect(image.Rect(bar.Min.X, bar.Min.Y+r.s(3), bar.Min.X+r.s(4), bar.Max.Y-r.s(3)), c)
			label := clipText(r, r.micro, e.Summary, bar.Dx()-r.s(12))
			r.text(r.micro, label, bar.Min.X+r.s(8), bar.Max.Y-r.s(5), cream)
			r.calHitAdd(calHit{r: bar, kind: calEvent, ev: e})
			y += barH + gap
		}
	}
	if !v.loaded {
		msg := "Reading the calendar…"
		r.text(r.tiny, msg, r.w-r.margin-r.width(r.tiny, msg), top+r.s(22), dim)
	}
}

// calendarDay is a day's events as a list: the time, the title and the calendar, all-day first.
func (r *renderer) calendarDay(v calendarView) {
	// Done here goes back to the month; the month's own Done puts the calendar away.
	x := r.calButton("Done", r.w-r.margin, calBack)
	r.text(r.title, clipText(r, r.title, v.day.Format("Monday, January 2"), x-r.margin-r.s(10)), r.margin, r.margin+r.s(34), cream)

	evs := eventsOn(v.events, v.day)
	top := r.margin + r.s(74)
	if len(evs) == 0 {
		msg := "Nothing on this day"
		if !v.loaded {
			msg = "Reading the calendar…"
		}
		r.text(r.body, msg, r.margin, top+r.s(60), dim)
		return
	}
	rowH := r.s(66)
	fit := max((r.h-r.s(10)-top)/rowH, 1)
	first := min(max(v.scroll, 0), max(len(evs)-fit, 0))
	shown := evs[first:min(len(evs), first+fit)]
	for i, e := range shown {
		row := image.Rect(r.margin, top+i*rowH, r.w-r.margin, top+(i+1)*rowH-r.s(6))
		c := v.color(e.Calendar)
		r.roundRect(row, r.s(10), color.NRGBA{255, 255, 255, 12})
		r.roundRect(image.Rect(row.Min.X+r.s(10), row.Min.Y+r.s(10), row.Min.X+r.s(18), row.Max.Y-r.s(10)), r.s(4), c)
		textX := row.Min.X + r.s(34)
		r.text(r.small, clipText(r, r.small, e.Summary, row.Dx()-r.s(60)), textX, row.Min.Y+r.s(30), cream)
		r.text(r.tiny, clipText(r, r.tiny, eventWhen(e, v.day)+"  ·  "+v.name(e.Calendar), row.Dx()-r.s(60)),
			textX, row.Min.Y+r.s(56), dim)
		r.calHitAdd(calHit{r: row, kind: calEvent, ev: e})
	}
	// More than fits: a swipe up or down moves through them, and the corners say there are more.
	if first > 0 {
		r.text(r.tiny, "▲ "+strconv.Itoa(first)+" more", r.w-r.margin-r.s(130), top-r.s(8), amber)
	}
	if rest := len(evs) - first - len(shown); rest > 0 {
		label := "▼ " + strconv.Itoa(rest) + " more"
		r.text(r.tiny, label, r.w-r.margin-r.width(r.tiny, label), r.h-r.s(4), amber)
	}
}

// calendarDayRows is how many events the day's list shows at once, for scrolling it.
func (r *renderer) calendarDayRows() int {
	return max((r.h-r.s(10)-(r.margin+r.s(74)))/r.s(66), 1)
}

// eventWhen is an event's time as the day's list says it: "All day", "3:00 PM – 4:30 PM", or, for
// one that runs past the day, when it starts or ends beyond it.
func eventWhen(e hass.Event, day time.Time) string {
	if e.AllDay {
		last := e.End.AddDate(0, 0, -1)
		if sameDay(e.Start, last) || !last.After(e.Start) {
			return "All day"
		}
		return "All day, " + e.Start.Format("Jan 2") + " – " + last.Format("Jan 2")
	}
	if !e.End.After(e.Start) {
		return clockText(e.Start)
	}
	from, to := clockText(e.Start), clockText(e.End)
	if !sameDay(e.Start, day) {
		from = e.Start.Format("Jan 2") + " " + from
	}
	if !sameDay(e.End, day) {
		to = e.End.Format("Jan 2") + " " + to
	}
	return from + " – " + to
}

// eventWhenFull is an event's day and its time, as its window says them on two lines.
func eventWhenFull(e hass.Event) (day, at string) {
	if e.AllDay {
		last := e.End.AddDate(0, 0, -1)
		if !last.After(e.Start) {
			return e.Start.Format("Monday, January 2"), "All day"
		}
		return e.Start.Format("Mon, Jan 2") + " – " + last.Format("Mon, Jan 2"), "All day"
	}
	if !e.End.After(e.Start) {
		return e.Start.Format("Monday, January 2"), clockText(e.Start)
	}
	if sameDay(e.Start, e.End) {
		return e.Start.Format("Monday, January 2"), clockText(e.Start) + " – " + clockText(e.End)
	}
	return fmt.Sprintf("%s %s –", e.Start.Format("Mon, Jan 2"), clockText(e.Start)),
		fmt.Sprintf("%s %s", e.End.Format("Mon, Jan 2"), clockText(e.End))
}

// calendarDetail is one event's window: the title, when, the calendar, and the location and the
// description when it has them.
func (r *renderer) calendarDetail(v calendarView, e hass.Event) {
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(color.NRGBA{0, 0, 0, 150}), image.Point{}, draw.Over)
	r.calHitAdd(calHit{r: r.dst.Rect, kind: calOutside})
	w, h := r.w*76/100, r.h*80/100
	card := image.Rect((r.w-w)/2, (r.h-h)/2, (r.w+w)/2, (r.h+h)/2)
	r.roundRect(card, r.s(18), color.RGBA{walnut.R/2 + 20, walnut.G/2 + 20, walnut.B/2 + 20, 255})
	r.calHitAdd(calHit{r: card, kind: calInside})
	c := v.color(e.Calendar)
	r.fillRect(image.Rect(card.Min.X+r.s(18), card.Min.Y+r.s(22), card.Min.X+r.s(24), card.Min.Y+r.s(74)), c)
	x, textW := card.Min.X+r.s(40), card.Dx()-r.s(64)
	y := card.Min.Y + r.s(58)
	for i, line := range r.wrap(r.title, e.Summary, textW) {
		if i == 2 {
			break
		}
		r.text(r.title, line, x, y, cream)
		y += r.s(54)
	}
	dayLine, atLine := eventWhenFull(e)
	r.text(r.small, clipText(r, r.small, dayLine, textW), x, y, cream)
	y += r.s(40)
	r.text(r.small, clipText(r, r.small, atLine, textW), x, y, cream)
	y += r.s(42)
	r.text(r.tiny, clipText(r, r.tiny, v.name(e.Calendar), textW), x, y, c)
	y += r.s(38)
	if e.Location != "" {
		r.text(r.small, clipText(r, r.small, e.Location, textW), x, y, dim)
		y += r.s(42)
	}
	closeB := image.Rect(card.Max.X-r.s(150), card.Max.Y-r.s(66), card.Max.X-r.s(22), card.Max.Y-r.s(18))
	if e.Description != "" {
		lines := r.wrap(r.tiny, e.Description, textW)
		for _, line := range lines {
			if y+r.s(32) > closeB.Min.Y-r.s(6) {
				break
			}
			r.text(r.tiny, line, x, y, dim)
			y += r.s(32)
		}
	}
	r.roundRect(closeB, closeB.Dy()/2, color.NRGBA{255, 255, 255, 30})
	label := "Close"
	r.text(r.small, label, closeB.Min.X+(closeB.Dx()-r.width(r.small, label))/2, closeB.Min.Y+r.s(35), cream)
	r.calHitAdd(calHit{r: closeB.Inset(-r.s(8)), kind: calClose})
}

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}
