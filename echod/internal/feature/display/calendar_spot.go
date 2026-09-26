//go:build spot

package display

import (
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The Spot's calendar: no month on a screen this small, only what is coming. Today's events still to
// come, or, when today has none left, the next ones after it. Reached from the dial, once the Spot
// shows a calendar (Settings, General, Calendars); a tap puts it away.

// calendarIdle is how long the calendar face stays up untouched.
const calendarIdle = time.Minute

// spotWhen is an event's time as the face says it: All day, or when it starts.
func spotWhen(e hass.Event, now time.Time) string {
	if e.AllDay {
		return "All day"
	}
	if e.Start.Before(now) {
		return "Now, until " + clockText(e.End)
	}
	return clockText(e.Start)
}

// chordAt is how wide the round screen is at baseline y, less a margin either side.
func chordAt(y int) int {
	d := float64(y - center)
	return int(2*math.Sqrt(math.Max(float64(center*center)-d*d, 0))) - 80
}

// calendarFace is Today, or Next: each event's title with its calendar's color beside it, and when.
func (r *roundRenderer) calendarFace(s roundScene) {
	r.clear()
	r.centered(r.label, clockHM(s.now), 62, colDim)
	heading, rows := "Today", s.calToday
	if len(rows) == 0 {
		heading, rows = "Next", s.calNext
	}
	r.centered(r.title, heading, 116, colText)
	if len(rows) == 0 {
		r.centered(r.body, "Nothing coming up", 250, colDim)
		return
	}
	y := 178
	shown := rows[:min(len(rows), 3)]
	for _, e := range shown {
		width := chordAt(y)
		title := clip(r.body, r, e.Summary, width-24)
		tw := r.width(r.body, title)
		r.discAt(float64(center-tw/2-16), float64(y-9), 6, calendarColor(s.calOrder, e.Calendar))
		r.centered(r.body, title, y, colText)
		when := spotWhen(e, s.now)
		if heading == "Next" {
			when = comingDay(e.Start, s.now) + " · " + when
		}
		r.centered(r.small, clip(r.small, r, when, chordAt(y+30)), y+30, colDim)
		y += 76
	}
	if more := len(s.calToday) - len(shown); heading == "Today" && more > 0 {
		r.centered(r.small, "and "+strconv.Itoa(more)+" more today", y-8, colDim)
	}
}

// comingUp is today's events not yet over, all-day ones first, and the next few after today: what the
// Spot's calendar face shows. From the month's events, and the next month's near its end.
func comingUp(now time.Time, next int) (today, later []hass.Event) {
	h := home.Get()
	events, _ := h.MonthEvents(now)
	if first := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.Local); first.Sub(now) < 14*24*time.Hour {
		more, _ := h.MonthEvents(first)
		events = append(events, more...)
	}
	for _, e := range eventsOn(events, now) {
		if e.AllDay || e.End.After(now) || e.Start.After(now) {
			today = append(today, e)
		}
	}
	tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.Local)
	for _, e := range events {
		if !e.Start.Before(tomorrow) && !slices.ContainsFunc(later, func(x hass.Event) bool {
			return x.Summary == e.Summary && x.Start.Equal(e.Start)
		}) {
			later = append(later, e)
		}
	}
	slices.SortStableFunc(later, func(a, b hass.Event) int { return a.Start.Compare(b.Start) })
	return today, later[:min(len(later), next)]
}

// comingDay is a day as the Spot names one coming up: Tomorrow, a weekday in the week ahead, or a date.
func comingDay(day, now time.Time) string {
	d0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	d := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.Local)
	switch days := int(d.Sub(d0).Hours() / 24); {
	case days == 1:
		return "Tomorrow"
	case days > 1 && days < 7:
		return day.Format("Monday")
	}
	return day.Format("Mon, Jan 2")
}
