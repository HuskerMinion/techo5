//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"strconv"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The calendar page (docs/calendar-and-night-plan.md): the month as a grid with each day's events on
// it, a day's events on its hours or as a list, a week on its hours, the coming days' events as an
// agenda, and one event's details in a window over any of them. The events are Home Assistant's, from
// the calendars this device shows, each calendar in its own color, less those hidden here with the
// chips over the month. How much of it is drawn, and how, is the switches in calendar_look.go.

// calendarShow is how long the calendar stays up untouched.
const calendarShow = 2 * time.Minute

// agendaDays is how many days the agenda lists, today the first.
const agendaDays = 14

// calendarView is what the page shows.
type calendarView struct {
	month  time.Time    // the first of the month shown
	day    time.Time    // the day opened, on its hours or as a list; zero for the month or the agenda
	week   bool         // the week holding day, on its hours, rather than the day alone
	agenda bool         // the coming days' events, as one list, rather than the month
	detail *hass.Event  // the event opened in its window, over the month, the day or the agenda
	scroll int          // how far down the day's list or the agenda is scrolled, in rows; the day's hours, in hours
	events []hass.Event // the month's; for the agenda, this month's and the next's; for a week, its months'
	loaded bool         // whether those events have been read yet
	names  map[string]string
	order  []string // the calendars shown, in order, which picks their colors
	hidden []string // the calendars hidden here, whose events are left out
	// chipsOpen is the list of every calendar up over the page, from the chips' "+N".
	chipsOpen bool
	now       time.Time
	// forecast is the coming days' weather, for the days' headings; none with the weather switch off.
	forecast []hass.Day
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
	calNone      calHitKind = iota
	calPrev                 // the month before
	calNext                 // the month after
	calToday                // back to this month
	calDone                 // put the calendar away
	calBack                 // from a day back to its month
	calDay                  // a day of the month: its list
	calEvent                // an event: its window
	calClose                // the window's Close
	calOutside              // anywhere outside the window: closes it
	calInside               // the window itself: nothing
	calMonthView            // from the agenda or the week to the month
	calViewDay              // the Day of the Day, Week and Month switch
	calViewWeek             // its Week
	calChip                 // a calendar's chip: hides its events here, or shows them again
	calLater                // the day's "Later" pill: its hours scrolled to the event
	calChipsMore            // the chips' "+N": the list of every calendar
	calChipsDone            // that list's Done, or a tap outside it
)

type calHit struct {
	r    image.Rectangle
	kind calHitKind
	day  time.Time
	ev   hass.Event
	cal  string // a chip's calendar
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
	v.hidden = hiddenAmong(v.hidden, v.order)
	v.events = visibleEvents(v.events, v.hidden)
	l := r.look()
	if !l.weather {
		v.forecast = nil
	}
	switch {
	case !v.day.IsZero() && v.week && l.week:
		r.calendarWeek(v, l)
	case !v.day.IsZero() && l.timeline:
		r.calendarTimeline(v, l)
	case !v.day.IsZero():
		r.calendarDay(v)
	case v.agenda && l.refined:
		r.calendarAgendaRefined(v, l)
	case v.agenda:
		r.calendarAgenda(v)
	default:
		r.calendarMonth(v, l)
	}
	if v.chipsOpen && len(v.order) > 1 {
		r.calChipsList(v)
	}
	if v.detail != nil {
		r.calendarDetail(v, *v.detail)
	}
}

// calArrowIcon is the size the header's chevrons are drawn at. The program's icon buttons draw the
// transport marks at transportIcon's 32 pixels in a 54-pixel button; the header's buttons are 48
// tall, so 28 is the same share of the button.
const calArrowIcon = 28

// calIconButtonAt draws a button in b whose label is an icon-font glyph: the program's button face,
// with the mark centered by the ink of the glyph itself, as the transport controls are.
func (r *renderer) calIconButtonAt(b image.Rectangle, icon string) {
	col := r.buttonFace(b, float64(b.Dy())/2, btnSecondary)
	r.iconIn(b, icon, calArrowIcon, col)
}

// calButtonBox is the box a header button takes: the one the page's small face measured, so the
// header stays where it was. The button face is narrower and fits inside it.
func (r *renderer) calButtonBox(label string, right int) image.Rectangle {
	w := r.width(r.small, label) + r.s(36)
	return image.Rect(right-w, r.margin-r.s(8), right, r.margin+r.s(40))
}

// calButton draws a button with its right edge at right, on the header line, and says where it is.
func (r *renderer) calButton(label string, right int, kind calHitKind) int {
	b := r.calButtonBox(label, right)
	r.labelButton(b, label, btnSecondary)
	r.calHitAdd(calHit{r: b.Inset(-r.s(6)), kind: kind})
	return b.Min.X - r.s(14)
}

// calIconButton draws a button whose label is an icon-font glyph, with its right edge at right, on the
// header line, and says where it is. label is the words the icon replaces: the box is still the one
// they measured, so every button and hit rectangle on the header line stays where it was, on any
// screen font, and only the ink changes.
func (r *renderer) calIconButton(icon, label string, right int, kind calHitKind) int {
	b := r.calButtonBox(label, right)
	r.calIconButtonAt(b, icon)
	r.calHitAdd(calHit{r: b.Inset(-r.s(6)), kind: kind})
	return b.Min.X - r.s(14)
}

// calendarDay is a day's events as a list: the time, the title and the calendar, all-day first.
func (r *renderer) calendarDay(v calendarView) {
	// Done here goes back to the month or the agenda it was opened from; their own Done puts the
	// calendar away.
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

// agendaRow is a line of the agenda: a day's heading, or one of its events.
type agendaRow struct {
	day time.Time
	ev  *hass.Event // nil for the day's heading
}

func (a agendaRow) height(r *renderer) int {
	if a.ev == nil {
		return r.s(48)
	}
	return r.s(66)
}

// agendaRows is the agenda's lines: each of the coming agendaDays that has events, its heading and
// then its events, all-day first, leaving out today's that are over.
func agendaRows(events []hass.Event, now time.Time) []agendaRow {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	var rows []agendaRow
	for i := range agendaDays {
		day := today.AddDate(0, 0, i)
		var evs []hass.Event
		for _, e := range eventsOn(events, day) {
			if i > 0 || e.AllDay || e.End.After(now) || !e.End.After(e.Start) && !e.Start.Before(now) {
				evs = append(evs, e)
			}
		}
		if len(evs) == 0 {
			continue
		}
		rows = append(rows, agendaRow{day: day})
		for _, e := range evs {
			rows = append(rows, agendaRow{day: day, ev: &e})
		}
	}
	return rows
}

// agendaDayName is a day's heading in the agenda: "Today", "Tomorrow", or its weekday and date.
func agendaDayName(day, now time.Time) string {
	switch {
	case sameDay(day, now):
		return "Today"
	case sameDay(day, now.AddDate(0, 0, 1)):
		return "Tomorrow"
	}
	return day.Format("Monday, January 2")
}

// calendarAgenda is the coming days' events as one list, each day under its heading. A tap on an event
// opens its window, a tap on a heading that day's list.
func (r *renderer) calendarAgenda(v calendarView) {
	x := r.calButton("Done", r.w-r.margin, calDone)
	x = r.calButton("Month", x, calMonthView)
	r.text(r.title, clipText(r, r.title, "Coming up", x-r.margin-r.s(10)), r.margin, r.margin+r.s(34), cream)

	rows := agendaRows(v.events, v.now)
	top := r.margin + r.s(74)
	bottom := r.h - r.s(10)
	// The furthest the list can scroll: the first row from which the rest all fit.
	most, used := len(rows), 0
	for most > 0 && used+rows[most-1].height(r) <= bottom-top {
		most--
		used += rows[most].height(r)
	}
	r.calMu.Lock()
	r.calAgendaMax = most
	r.calAgendaStep = max(r.calendarDayRows()-1, 1)
	r.calMu.Unlock()
	if len(rows) == 0 {
		msg := "Nothing in the next two weeks"
		if !v.loaded {
			msg = "Reading the calendar…"
		}
		r.text(r.body, msg, r.margin, top+r.s(60), dim)
		return
	}
	first := min(max(v.scroll, 0), most)
	y, last := top, first
	for i := first; i < len(rows) && y+rows[i].height(r) <= bottom; i++ {
		row := rows[i]
		h := row.height(r)
		if row.ev == nil {
			r.text(r.small, agendaDayName(row.day, v.now), r.margin, y+r.s(34), amber)
			r.fxLine(float64(r.margin), float64(y+h-r.s(4)), float64(r.w-r.margin), float64(y+h-r.s(4)), float64(r.s(2)), ember, 1)
			r.calHitAdd(calHit{r: image.Rect(r.margin, y, r.w-r.margin, y+h), kind: calDay, day: row.day})
		} else {
			e := *row.ev
			box := image.Rect(r.margin, y+r.s(4), r.w-r.margin, y+h-r.s(2))
			c := v.color(e.Calendar)
			r.roundRect(box, r.s(10), color.NRGBA{255, 255, 255, 12})
			r.roundRect(image.Rect(box.Min.X+r.s(10), box.Min.Y+r.s(10), box.Min.X+r.s(18), box.Max.Y-r.s(10)), r.s(4), c)
			textX := box.Min.X + r.s(34)
			r.text(r.small, clipText(r, r.small, e.Summary, box.Dx()-r.s(60)), textX, box.Min.Y+r.s(28), cream)
			r.text(r.tiny, clipText(r, r.tiny, eventWhen(e, row.day)+"  ·  "+v.name(e.Calendar), box.Dx()-r.s(60)),
				textX, box.Min.Y+r.s(54), dim)
			r.calHitAdd(calHit{r: box, kind: calEvent, ev: e})
		}
		y += h
		last = i + 1
	}
	// More than fits: a swipe up or down moves through them, and the corners say there are more.
	if first > 0 {
		r.text(r.tiny, "▲ more", r.w-r.margin-r.s(130), top-r.s(8), amber)
	}
	if last < len(rows) {
		label := "▼ more"
		r.text(r.tiny, label, r.w-r.margin-r.width(r.tiny, label), r.h-r.s(4), amber)
	}
}

// calendarAgendaStep is how many rows a swipe moves the agenda, as last drawn: a page less one.
func (r *renderer) calendarAgendaStep() int {
	r.calMu.Lock()
	defer r.calMu.Unlock()
	return max(r.calAgendaStep, 1)
}

// calendarAgendaMax is how far the agenda could scroll, in rows, as last drawn.
func (r *renderer) calendarAgendaMax() int {
	r.calMu.Lock()
	defer r.calMu.Unlock()
	return r.calAgendaMax
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
	washOver(r.dst, r.dst.Rect, color.NRGBA{0, 0, 0, 150})
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
	r.labelButton(closeB, "Close", btnSecondary)
	r.calHitAdd(calHit{r: closeB.Inset(-r.s(8)), kind: calClose})
}

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}
