//go:build !dot && !spot

package display

import (
	"image"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

func calTestDisplay(t *testing.T) (*Display, calendarView) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	at := func(d, h int) time.Time { return time.Date(2026, 9, d, h, 0, 0, 0, time.Local) }
	v := calendarView{
		month: time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local), now: at(26, 10), loaded: true,
		order: []string{"calendar.family"}, names: map[string]string{"calendar.family": "Family"},
		events: []hass.Event{
			{Calendar: "calendar.family", Summary: "Dentist", Start: at(26, 15), End: at(26, 16)},
			{Calendar: "calendar.family", Summary: "Birthday", Start: at(26, 0), End: at(27, 0), AllDay: true},
		},
	}
	d := &Display{r: newRenderer(image.NewRGBA(image.Rect(0, 0, 960, 480))), poke: make(chan struct{}, 1),
		view: voice.State{Phase: "idle"}}
	d.calUntil, d.calMonth = time.Now().Add(time.Minute), v.month
	return d, v
}

// hitOf is where the last drawn page put the first thing of a kind, for a finger to land on.
func hitOf(t *testing.T, r *renderer, kind calHitKind) image.Point {
	t.Helper()
	r.calMu.Lock()
	defer r.calMu.Unlock()
	for _, h := range r.calHits {
		if h.kind == kind {
			return image.Pt((h.r.Min.X+h.r.Max.X)/2, (h.r.Min.Y+h.r.Max.Y)/2)
		}
	}
	t.Fatalf("nothing of kind %d drawn", kind)
	return image.Point{}
}

// Around the page by touch: a day opens its list, an event its window, Close and Back return, the
// arrows and swipes move a month, and Done puts it away.
func TestCalendarPageByTouch(t *testing.T) {
	d, v := calTestDisplay(t)
	draw := func() {
		d.mu.Lock()
		v.month, v.day, v.detail, v.scroll = d.calMonth, d.calDay, d.calDetail, d.calScroll
		d.mu.Unlock()
		d.r.draw(scene{now: v.now, phase: "idle", showCalendar: true, cal: v})
	}
	tap := func(p image.Point) { d.calendarGesture(touch.Gesture{Kind: touch.Tap, X: p.X, Y: p.Y}) }

	draw()
	// The birthday's bar on the 26th, drawn first there since it is all day.
	tap(hitOf(t, d.r, calEvent))
	if d.calDetail == nil || d.calDetail.Summary != "Birthday" {
		t.Fatalf("an event's bar opened %+v", d.calDetail)
	}
	draw()
	tap(hitOf(t, d.r, calClose))
	if d.calDetail != nil {
		t.Fatal("Close left the window open")
	}

	draw()
	d.r.calMu.Lock()
	var cell image.Point
	for _, h := range d.r.calHits {
		if h.kind == calDay && h.day.Day() == 26 && h.day.Month() == time.September {
			cell = image.Pt(h.r.Min.X+4, h.r.Min.Y+4) // the corner, clear of the bars
		}
	}
	d.r.calMu.Unlock()
	tap(cell)
	if d.calDay.Day() != 26 {
		t.Fatalf("a tap on the 26th opened %v", d.calDay)
	}
	draw()
	tap(hitOf(t, d.r, calBack))
	if !d.calDay.IsZero() {
		t.Fatal("Back left the day open")
	}

	draw()
	tap(hitOf(t, d.r, calNext))
	if d.calMonth.Month() != time.October {
		t.Fatalf("› went to %v", d.calMonth.Month())
	}
	d.calendarGesture(touch.Gesture{Kind: touch.SwipeRight})
	d.calendarGesture(touch.Gesture{Kind: touch.SwipeRight})
	if d.calMonth.Month() != time.August {
		t.Fatalf("two swipes right went to %v", d.calMonth.Month())
	}

	draw()
	tap(hitOf(t, d.r, calDone))
	if d.calendarUp() {
		t.Error("Done left the calendar up")
	}
}

// With no calendar chosen, the calendar does not open.
func TestNoCalendarNoPage(t *testing.T) {
	d, _ := calTestDisplay(t)
	d.calUntil = time.Time{}
	if d.OpenCalendar() || d.calendarUp() {
		t.Error("the calendar opened with no calendar chosen")
	}
}

// A day's list: all-day first, then by time, whatever order the events came in.
func TestEventsOnSorts(t *testing.T) {
	_, v := calTestDisplay(t)
	got := eventsOn(v.events, time.Date(2026, 9, 26, 0, 0, 0, 0, time.Local))
	if len(got) != 2 || got[0].Summary != "Birthday" || got[1].Summary != "Dentist" {
		t.Errorf("%+v", got)
	}
}
