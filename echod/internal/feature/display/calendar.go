//go:build !dot && !spot

package display

import (
	"image"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// OpenCalendar puts the calendar up on this month. It reports false, and does nothing, on a device
// that shows no calendar.
func (d *Display) OpenCalendar() bool { return d.openCalendar(false) }

// OpenAgenda puts the calendar up as the coming days' events in one list, as OpenCalendar does the month.
func (d *Display) OpenAgenda() bool { return d.openCalendar(true) }

// OpenCalendarDay puts the calendar up on one day, as the clock's next-up card does for its event's
// day; Done there goes to that day's month.
func (d *Display) OpenCalendarDay(day time.Time) bool {
	if !d.openCalendar(false) {
		return false
	}
	d.mu.Lock()
	d.calDay = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.Local)
	d.calMonth = firstOfMonth(day)
	d.mu.Unlock()
	return true
}

func (d *Display) openCalendar(agenda bool) bool {
	if len(home.Get().CalendarSources()) == 0 {
		return false
	}
	now := time.Now()
	d.mu.Lock()
	d.calUntil, d.calMonth, d.calDay, d.calDetail = now.Add(calendarShow), firstOfMonth(now), time.Time{}, nil
	d.calAgenda, d.calScroll, d.calKept, d.calWeek, d.calFromWeek, d.calChips = agenda, 0, 0, false, false, false
	d.closeAlert()
	d.weatherUntil, d.sheet, d.dash, d.drawer = time.Time{}, false, false, false
	d.mu.Unlock()
	d.wake()
	return true
}

// agendaEvents is this month's and the next's events together, which hold every day the agenda shows,
// and whether both have been read.
func agendaEvents(now time.Time) ([]hass.Event, bool) {
	first := firstOfMonth(now)
	return monthsEvents(first, first.AddDate(0, 1, 0))
}

// monthsEvents is the events of the months holding from and to, and of those between, together, and
// whether all of them have been read. An event running across the turn of a month is in both: it is
// kept once.
func monthsEvents(from, to time.Time) ([]hass.Event, bool) {
	var months [][]hass.Event
	ok := true
	for m := firstOfMonth(from); !m.After(to); m = m.AddDate(0, 1, 0) {
		more, read := home.Get().MonthEvents(m)
		ok = ok && read
		months = append(months, more)
	}
	return joinMonths(months), ok
}

// joinMonths is the months' events one after another, an event in more than one of them (one running
// across the turn of a month) kept the first time: the same calendar, title, start, end and
// all-day-ness, as mergeSame tells events apart, so two it keeps apart in a month stay apart here. Only
// against the months before: two alike in one month are as that month has them. Looked up in a map,
// where a search of those kept was thousands of comparisons for each of thousands of events. A time
// is its seconds and nanoseconds, which hold any date, where UnixNano holds none past 2262.
func joinMonths(months [][]hass.Event) []hass.Event {
	type same struct {
		calendar, summary  string
		start, end         int64
		startNano, endNano int
		allDay             bool
	}
	key := func(e hass.Event) same {
		return same{e.Calendar, e.Summary, e.Start.Unix(), e.End.Unix(), e.Start.Nanosecond(), e.End.Nanosecond(), e.AllDay}
	}
	var events []hass.Event
	seen := map[same]bool{}
	for _, more := range months {
		var added []same
		for _, e := range more {
			k := key(e)
			if !seen[k] {
				added = append(added, k)
				events = append(events, e)
			}
		}
		for _, k := range added {
			seen[k] = true
		}
	}
	return events
}

func firstOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.Local)
}

// calendarUp is whether the calendar page is on the screen.
func (d *Display) calendarUp() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return time.Now().Before(d.calUntil) && pageIdle(d.view.Phase)
}

// calendarScene fills in what the calendar page shows, when it is up.
func (d *Display) calendarScene(s *scene, now time.Time) {
	d.mu.Lock()
	up := now.Before(d.calUntil) && (s.phase == "idle" || s.phase == "lingering")
	month, day, agenda, detail, scroll, week, chips := d.calMonth, d.calDay, d.calAgenda, d.calDetail, d.calScroll, d.calWeek, d.calChips
	d.mu.Unlock()
	if !up {
		return
	}
	h := home.Get()
	var events []hass.Event
	var loaded bool
	switch {
	case agenda:
		events, loaded = agendaEvents(now)
	case week && !day.IsZero():
		first := weekStart(day)
		events, loaded = monthsEvents(first, first.AddDate(0, 0, 6))
	default:
		events, loaded = h.MonthEvents(month)
	}
	names := map[string]string{}
	for _, c := range h.Calendars() {
		names[c.ID] = c.Name
	}
	c := config.Get().Calendar
	s.showCalendar = true
	s.cal = calendarView{month: month, day: day, week: week, agenda: agenda, detail: detail, scroll: scroll, events: events,
		loaded: loaded, names: names, order: h.CalendarSources(), hidden: hiddenHere(c), chipsOpen: chips, now: now}
	if !c.NoWeather {
		s.cal.forecast = h.Forecast()
	}
}

// calendarViewLocked is which page the calendar is on: a day, a week, the agenda, or (none of them) the
// month.
func (d *Display) calendarViewLocked() (day, week, agenda bool) {
	return !d.calDay.IsZero() && !d.calWeek, !d.calDay.IsZero() && d.calWeek, d.calDay.IsZero() && d.calAgenda
}

// calendarGesture is a touch on the calendar page. Every touch keeps it up a while longer.
func (d *Display) calendarGesture(g touch.Gesture) {
	// A calendar's chip saves the setting once the display's lock is let go, and draws the page again.
	var hide string
	defer func() {
		if hide != "" {
			toggleHidden(hide)
			d.wake()
		}
	}()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calUntil = time.Now().Add(calendarShow)
	day, week, agenda := d.calendarViewLocked()
	timelineOn := d.r != nil && d.r.look().timeline
	switch g.Kind {
	case touch.SwipeLeft, touch.SwipeRight:
		// The month, the week, or the day on its hours: left goes on to the next, right back.
		if d.calDetail != nil {
			return
		}
		by := 1
		if g.Kind == touch.SwipeRight {
			by = -1
		}
		switch {
		case week:
			d.calStepLocked(7 * by)
		case day && timelineOn:
			d.calStepLocked(by)
		case !day && !agenda:
			d.calMonth = d.calMonth.AddDate(0, by, 0)
		}
		return
	case touch.SwipeUp, touch.SwipeDown:
		// A long day's list, the day's hours or the agenda: up shows the later ones, down the earlier.
		if d.calDetail != nil || d.r == nil {
			return
		}
		switch {
		case day && timelineOn:
			d.r.calMu.Lock()
			tl := d.r.calTimeline
			d.r.calMu.Unlock()
			step := max(tl.shown-1, 1)
			if g.Kind == touch.SwipeDown {
				step = -step
			}
			d.calScroll = min(max(d.calScroll+step, tl.low), tl.high)
		case day:
			step := max(d.r.calendarDayRows()-1, 1)
			if g.Kind == touch.SwipeDown {
				step = -step
			}
			events, _ := home.Get().EventsOn(d.calDay)
			events = visibleEvents(events, hiddenHere(config.Get().Calendar))
			d.calScroll = min(max(d.calScroll+step, 0), max(len(events)-d.r.calendarDayRows(), 0))
		case agenda:
			step := d.r.calendarAgendaStep()
			if g.Kind == touch.SwipeDown {
				step = -step
			}
			d.calScroll = min(max(d.calScroll+step, 0), d.r.calendarAgendaMax())
		}
		return
	case touch.Tap:
	default:
		return
	}
	if d.r == nil {
		return
	}
	hit, ok := d.r.calendarHit(image.Pt(g.X, g.Y))
	if !ok {
		return
	}
	now := time.Now()
	switch hit.kind {
	case calPrev, calNext:
		by := 1
		if hit.kind == calPrev {
			by = -1
		}
		switch {
		case week:
			d.calStepLocked(7 * by)
		case day:
			d.calStepLocked(by)
		default:
			d.calMonth = d.calMonth.AddDate(0, by, 0)
		}
	case calToday:
		d.calMonth = firstOfMonth(now)
	case calDone:
		d.calUntil, d.calDetail = time.Time{}, nil
		slog.Info("screen: calendar put away")
	case calBack:
		if d.calFromWeek {
			// A day opened from the week goes back to its week.
			d.calWeek, d.calFromWeek, d.calDay, d.calScroll = true, false, weekStart(d.calDay), 0
			return
		}
		d.calDay, d.calScroll = time.Time{}, 0
		if d.calAgenda {
			d.calScroll = d.calKept // back where the agenda was
		}
	case calMonthView:
		if week {
			d.calMonth = firstOfMonth(d.calDay)
		} else {
			d.calMonth = firstOfMonth(now)
		}
		d.calAgenda, d.calWeek, d.calFromWeek, d.calDay, d.calScroll = false, false, false, time.Time{}, 0
	case calViewWeek:
		// The week holding today when today is in the month shown, else the month's first week.
		anchor := d.calMonth
		if sameMonth(d.calMonth, now) {
			anchor = now
		}
		d.calWeek, d.calDay, d.calScroll = true, weekStart(anchor), 0
	case calViewDay:
		// Today when it is in the week or the month shown, else that week's or month's first day.
		anchor, in := d.calMonth, sameMonth(d.calMonth, now)
		if week {
			anchor, in = d.calDay, daysBetween(d.calDay, now) >= 0 && daysBetween(d.calDay, now) < 7
		}
		if in {
			anchor = now
		}
		d.calFromWeek, d.calWeek = week, false
		d.calDay, d.calScroll = time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, time.Local), 0
		d.calMonth = firstOfMonth(d.calDay)
	case calDay:
		if d.calAgenda {
			d.calKept = d.calScroll
		}
		d.calFromWeek = week
		d.calWeek = false
		d.calDay, d.calScroll = hit.day, 0
		// A day at the edge of the grid belongs to the month before or after: its list reads that one.
		d.calMonth = firstOfMonth(hit.day)
	case calEvent:
		ev := hit.ev
		d.calDetail = &ev
	case calChip:
		hide = hit.cal
	case calChipsMore:
		d.calChips = true
	case calChipsDone:
		d.calChips = false
	case calLater:
		d.r.calMu.Lock()
		tl := d.r.calTimeline
		d.r.calMu.Unlock()
		d.calScroll = tl.laterScroll(hit.ev)
	case calClose, calOutside:
		d.calDetail = nil
	}
}

// calStepLocked moves the day or the week shown by days, its hours back where a new day opens.
func (d *Display) calStepLocked(days int) {
	d.calDay, d.calScroll = d.calDay.AddDate(0, 0, days), 0
	d.calMonth = firstOfMonth(d.calDay)
}

func sameMonth(a, b time.Time) bool { return a.Year() == b.Year() && a.Month() == b.Month() }
