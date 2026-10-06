//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The agenda leaves out today's events that are over, takes in the next month's, and puts each day
// under its heading, all-day first.
func TestAgendaRows(t *testing.T) {
	_, v := calTestDisplay(t)
	at := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, time.Local) }
	v.events = append(v.events,
		hass.Event{Calendar: "calendar.family", Summary: "Breakfast", Start: at(9, 26, 8), End: at(9, 26, 9)},
		hass.Event{Calendar: "calendar.family", Summary: "Trip", Start: at(10, 2, 9), End: at(10, 2, 17)},
		hass.Event{Calendar: "calendar.family", Summary: "Too far", Start: at(10, 30, 9), End: at(10, 30, 10)})
	var got []string
	for _, row := range agendaRows(v.events, v.now) {
		if row.ev == nil {
			got = append(got, agendaDayName(row.day, v.now))
		} else {
			got = append(got, row.ev.Summary)
		}
	}
	want := []string{"Today", "Birthday", "Dentist", "Friday, October 2", "Trip"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("rows %q, want %q", got, want)
	}
}

// Around the agenda by touch: an event opens its window, a day's heading that day's list and Done
// there comes back, Month goes to the month, and a long agenda scrolls.
func TestAgendaByTouch(t *testing.T) {
	d, v := calTestDisplay(t)
	d.calAgenda = true
	draw := func() {
		d.mu.Lock()
		v.day, v.agenda, v.detail, v.scroll = d.calDay, d.calAgenda, d.calDetail, d.calScroll
		d.mu.Unlock()
		d.r.draw(scene{now: v.now, phase: "idle", showCalendar: true, cal: v})
	}
	tap := func(p image.Point) { d.calendarGesture(touch.Gesture{Kind: touch.Tap, X: p.X, Y: p.Y}) }

	draw()
	tap(hitOf(t, d.r, calEvent))
	if d.calDetail == nil || d.calDetail.Summary != "Birthday" {
		t.Fatalf("an event opened %+v", d.calDetail)
	}
	draw()
	tap(hitOf(t, d.r, calClose))

	draw()
	tap(hitOf(t, d.r, calDay))
	if d.calDay.Day() != 26 {
		t.Fatalf("today's heading opened %v", d.calDay)
	}
	draw()
	tap(hitOf(t, d.r, calBack))
	if !d.calDay.IsZero() || !d.calAgenda {
		t.Fatal("Done on the day did not come back to the agenda")
	}

	// A swipe sideways does not move the month behind it.
	d.calendarGesture(touch.Gesture{Kind: touch.SwipeLeft})
	if d.calMonth.Month() != time.September {
		t.Errorf("a swipe on the agenda moved the month to %v", d.calMonth.Month())
	}

	for i := range 12 {
		v.events = append(v.events, hass.Event{Calendar: "calendar.family", Summary: fmt.Sprint("Event ", i),
			Start: v.now.AddDate(0, 0, i+1), End: v.now.AddDate(0, 0, i+1).Add(time.Hour)})
	}
	draw()
	if d.r.calendarAgendaMax() == 0 {
		t.Fatal("a long agenda cannot scroll")
	}
	d.calendarGesture(touch.Gesture{Kind: touch.SwipeUp})
	if d.calScroll == 0 {
		t.Error("a swipe up did not scroll the agenda")
	}
	for range 20 {
		d.calendarGesture(touch.Gesture{Kind: touch.SwipeUp})
	}
	if d.calScroll != d.r.calendarAgendaMax() {
		t.Errorf("scrolled to %d, past the end at %d", d.calScroll, d.r.calendarAgendaMax())
	}

	// A day opened from the scrolled agenda, and Done there, comes back to the same place.
	scrolled := d.calScroll
	draw()
	tap(hitOf(t, d.r, calDay))
	if d.calDay.IsZero() || d.calScroll != 0 {
		t.Fatalf("a heading in the scrolled agenda opened %v at %d", d.calDay, d.calScroll)
	}
	draw()
	tap(hitOf(t, d.r, calBack))
	if d.calScroll != scrolled {
		t.Errorf("Done came back to the agenda at %d, not where it was at %d", d.calScroll, scrolled)
	}

	draw()
	tap(hitOf(t, d.r, calMonthView))
	if d.calAgenda || d.calScroll != 0 {
		t.Error("Month left the agenda up")
	}
}

// The Dashboard's next events are a tap target of their own, apart from the date.
func TestDashboardNextTap(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	facts := styleFactsFor("", at)
	facts.kind, facts.chosen = styleDashboard, true
	facts.next = []hass.Event{{Summary: "Dentist", Start: at.Add(time.Hour), End: at.Add(2 * time.Hour)}}
	for _, size := range []image.Point{{showWide, showHigh}, {show8Wide, show8High}} {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, size.X, size.Y)))
		r.draw(scene{now: at, phase: "idle", style: facts})
		r.weatherMu.Lock()
		next, date := r.nextAt, r.dateAt
		r.weatherMu.Unlock()
		if next.Empty() {
			t.Fatalf("%v: the next events were not made a tap target", size)
		}
		if next.Overlaps(date) {
			t.Errorf("%v: the next events %v overlap the date %v", size, next, date)
		}
		if !r.nextTapped(image.Pt((next.Min.X+next.Max.X)/2, (next.Min.Y+next.Max.Y)/2)) {
			t.Errorf("%v: a tap on the next events missed", size)
		}

		// Another clock style has no next events to tap.
		other := facts
		other.kind = styleSun
		r.draw(scene{now: at, phase: "idle", style: other})
		if r.nextTapped(image.Pt((next.Min.X+next.Max.X)/2, (next.Min.Y+next.Max.Y)/2)) {
			t.Errorf("%v: the next events stayed a tap target in another style", size)
		}
	}
}

// The Dashboard's next events say when: the time today, "Tmrw" and the time tomorrow, and the weekday
// after that, the time beside it when there is one.
func TestDashWhen(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	now := time.Date(2026, 10, 6, 10, 0, 0, 0, time.Local) // a Tuesday
	at := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.Local) }
	day := func(d int) hass.Event { return hass.Event{Start: at(d, 0), End: at(d+1, 0), AllDay: true} }
	timed := func(d, h int) hass.Event { return hass.Event{Start: at(d, h), End: at(d, h+1)} }
	for _, c := range []struct {
		e    hass.Event
		want string
	}{
		{timed(6, 9), "Now"},
		{timed(6, 15), clockText(at(6, 15))},
		{day(6), "Today"},
		{timed(7, 9), "Tmrw " + clockText(at(7, 9))},
		{day(7), "Tomorrow"},
		{timed(8, 15), "Thu " + clockText(at(8, 15))},
		{day(10), "Sat"},
	} {
		if got := dashWhen(c.e, now); got != c.want {
			t.Errorf("%v: %q, want %q", c.e.Start, got, c.want)
		}
	}
}
