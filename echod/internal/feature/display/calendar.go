//go:build !dot && !spot

package display

import (
	"image"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// OpenCalendar puts the calendar up on this month. It reports false, and does nothing, on a device
// that shows no calendar.
func (d *Display) OpenCalendar() bool {
	if len(home.Get().CalendarSources()) == 0 {
		return false
	}
	now := time.Now()
	d.mu.Lock()
	d.calUntil, d.calMonth, d.calDay, d.calDetail = now.Add(calendarShow), firstOfMonth(now), time.Time{}, nil
	d.closeAlert()
	d.weatherUntil, d.sheet, d.dash, d.drawer = time.Time{}, false, false, false
	d.mu.Unlock()
	d.wake()
	return true
}

func firstOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.Local)
}

// calendarUp is whether the calendar page is on the screen.
func (d *Display) calendarUp() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return time.Now().Before(d.calUntil) && d.view.Phase == "idle"
}

// calendarScene fills in what the calendar page shows, when it is up.
func (d *Display) calendarScene(s *scene, now time.Time) {
	d.mu.Lock()
	up := now.Before(d.calUntil) && (s.phase == "idle" || s.phase == "lingering")
	month, day, detail, scroll := d.calMonth, d.calDay, d.calDetail, d.calScroll
	d.mu.Unlock()
	if !up {
		return
	}
	h := home.Get()
	events, loaded := h.MonthEvents(month)
	names := map[string]string{}
	for _, c := range h.Calendars() {
		names[c.ID] = c.Name
	}
	s.showCalendar = true
	s.cal = calendarView{month: month, day: day, detail: detail, scroll: scroll, events: events, loaded: loaded,
		names: names, order: h.CalendarSources(), now: now}
}

// calendarGesture is a touch on the calendar page. Every touch keeps it up a while longer.
func (d *Display) calendarGesture(g touch.Gesture) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calUntil = time.Now().Add(calendarShow)
	month := d.calDay.IsZero() && d.calDetail == nil
	switch g.Kind {
	case touch.SwipeLeft:
		if month {
			d.calMonth = d.calMonth.AddDate(0, 1, 0)
		}
		return
	case touch.SwipeRight:
		if month {
			d.calMonth = d.calMonth.AddDate(0, -1, 0)
		}
		return
	case touch.SwipeUp, touch.SwipeDown:
		// A long day's list: up shows the later events, down the earlier.
		if !d.calDay.IsZero() && d.calDetail == nil && d.r != nil {
			step := max(d.r.calendarDayRows()-1, 1)
			if g.Kind == touch.SwipeDown {
				step = -step
			}
			events, _ := home.Get().EventsOn(d.calDay)
			d.calScroll = min(max(d.calScroll+step, 0), max(len(events)-d.r.calendarDayRows(), 0))
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
	switch hit.kind {
	case calPrev:
		d.calMonth = d.calMonth.AddDate(0, -1, 0)
	case calNext:
		d.calMonth = d.calMonth.AddDate(0, 1, 0)
	case calToday:
		d.calMonth = firstOfMonth(time.Now())
	case calDone:
		d.calUntil, d.calDetail = time.Time{}, nil
		slog.Info("screen: calendar put away")
	case calBack:
		d.calDay, d.calScroll = time.Time{}, 0
	case calDay:
		d.calDay, d.calScroll = hit.day, 0
		// A day at the edge of the grid belongs to the month before or after: its list reads that one.
		d.calMonth = firstOfMonth(hit.day)
	case calEvent:
		ev := hit.ev
		d.calDetail = &ev
	case calClose, calOutside:
		d.calDetail = nil
	}
}
