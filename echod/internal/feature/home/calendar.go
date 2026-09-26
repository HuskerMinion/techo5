package home

import (
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The calendar: the events on the calendars this device shows, read from Home Assistant
// (docs/calendar-and-night-plan.md). Kept a month at a time and read again in the background, so a
// screen asking while it draws never waits on the network: it gets what is kept, and Changed fires when
// something newer arrives.

const (
	// calendarsEvery is how often Home Assistant's list of calendars is read again, and eventsEvery a
	// month's events.
	calendarsEvery = 10 * time.Minute
	eventsEvery    = 15 * time.Minute

	// monthsKept is how many months' events are kept at once: this one, the next, and a few looked at.
	monthsKept = 6
)

var calendar struct {
	sync.Mutex
	list     []hass.Calendar
	listAt   time.Time
	listBusy bool
	months   map[string]*calendarMonth
}

// calendarMonth is one month's events, from the sources chosen when it was read.
type calendarMonth struct {
	events  []hass.Event
	sources string // the chosen calendars they were read for, joined
	at      time.Time
	busy    bool
}

// Calendars is Home Assistant's calendars as last read, starting a read in the background when that
// is stale; it never waits.
func (f *Feature) Calendars() []hass.Calendar {
	calendar.Lock()
	defer calendar.Unlock()
	if time.Since(calendar.listAt) > calendarsEvery && !calendar.listBusy && hass.Get().Ready() {
		calendar.listBusy = true
		go func() {
			list, err := hass.Get().Calendars()
			calendar.Lock()
			calendar.listBusy = false
			if err != nil {
				slog.Warn("calendar: reading Home Assistant's calendars failed", "err", err)
				calendar.listAt = time.Now().Add(time.Minute - calendarsEvery) // try again in a minute
			} else {
				calendar.list, calendar.listAt = list, time.Now()
			}
			calendar.Unlock()
			if err == nil {
				f.Changed.Emit(struct{}{})
			}
		}()
	}
	return slices.Clone(calendar.list)
}

// CalendarSources is the calendars this device shows, in the order chosen; none is no calendar.
func (f *Feature) CalendarSources() []string { return config.Get().Calendar.Sources }

// SetCalendarSources chooses the calendars this device shows.
func (f *Feature) SetCalendarSources(ids []string) error {
	var clean []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || slices.Contains(clean, id) {
			continue
		}
		if !strings.HasPrefix(id, "calendar.") {
			return errors.New("calendar: " + id + " is not a calendar entity")
		}
		clean = append(clean, id)
	}
	if err := config.Set().Calendar().Sources(clean); err != nil {
		return err
	}
	// The calendars that pop up are among those shown: one no longer shown is no longer one of them,
	// and none left is every one again.
	if pick := config.Get().Calendar.PopupCalendars; len(pick) > 0 {
		kept := slices.DeleteFunc(slices.Clone(pick), func(id string) bool { return !slices.Contains(clean, id) })
		if len(kept) == 0 {
			kept = nil
		}
		if err := config.Set().Calendar().PopupCalendars(kept); err != nil {
			return err
		}
	}
	calendar.Lock()
	calendar.months = nil // read for the old choice
	calendar.Unlock()
	slog.Info("calendar: sources set", "count", len(clean))
	f.Changed.Emit(struct{}{})
	return nil
}

// MonthEvents is the events of the month holding month, on this device's calendars, and whether they
// have been read for the calendars chosen now. It starts a read when the month is missing or stale.
func (f *Feature) MonthEvents(month time.Time) ([]hass.Event, bool) {
	sources := f.CalendarSources()
	if len(sources) == 0 {
		return nil, true
	}
	first := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.Local)
	key, joined := first.Format("2006-01"), strings.Join(sources, ",")

	calendar.Lock()
	defer calendar.Unlock()
	if calendar.months == nil {
		calendar.months = map[string]*calendarMonth{}
	}
	m := calendar.months[key]
	if m == nil {
		m = &calendarMonth{}
		calendar.months[key] = m
		forgetOldMonths(key)
	}
	if !m.busy && hass.Get().Ready() && (m.sources != joined || time.Since(m.at) > eventsEvery) {
		m.busy = true
		go f.readMonth(key, first, sources, joined)
	}
	if m.sources != joined {
		return nil, false
	}
	return slices.Clone(m.events), true
}

// readMonth reads a month's events from each chosen calendar. One calendar failing - a login that ran
// out - leaves the others' events shown rather than none.
func (f *Feature) readMonth(key string, first time.Time, sources []string, joined string) {
	var events []hass.Event
	ok := false
	for _, src := range sources {
		ev, err := hass.Get().CalendarEvents(src, first, first.AddDate(0, 1, 0))
		if err != nil {
			slog.Warn("calendar: reading events failed", "calendar", src, "month", key, "err", err)
			continue
		}
		events, ok = append(events, ev...), true
	}
	slices.SortStableFunc(events, func(a, b hass.Event) int { return a.Start.Compare(b.Start) })

	calendar.Lock()
	m := calendar.months[key]
	if m == nil { // forgotten or cleared while it was read
		calendar.Unlock()
		return
	}
	m.busy = false
	if !ok {
		m.at = time.Now().Add(time.Minute - eventsEvery) // try again in a minute
		calendar.Unlock()
		return
	}
	m.events, m.sources, m.at = events, joined, time.Now()
	calendar.Unlock()
	f.Changed.Emit(struct{}{})
}

// forgetOldMonths keeps no more than monthsKept months, dropping those furthest from keep. Wants the lock.
func forgetOldMonths(keep string) {
	for len(calendar.months) > monthsKept {
		far, farBy := "", -1
		for k := range calendar.months {
			if d := monthDistance(k, keep); d > farBy {
				far, farBy = k, d
			}
		}
		delete(calendar.months, far)
	}
}

func monthDistance(a, b string) int {
	ta, _ := time.Parse("2006-01", a)
	tb, _ := time.Parse("2006-01", b)
	d := (ta.Year()-tb.Year())*12 + int(ta.Month()) - int(tb.Month())
	if d < 0 {
		d = -d
	}
	return d
}

// EventsOn is one day's events, from the kept month: those that touch the day at all, an event that
// runs across midnight on both days it touches.
func (f *Feature) EventsOn(day time.Time) ([]hass.Event, bool) {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 0, 1)
	month, ok := f.MonthEvents(start)
	var out []hass.Event
	for _, e := range month {
		if e.Start.Before(end) && (e.End.After(start) || e.Start.Equal(start)) {
			out = append(out, e)
		}
	}
	return out, ok
}

// calendarAction is calendar_sources: the calendars this device shows, as entity ids separated by
// commas; empty shows none.
func (f *Feature) calendarAction() *esphome.Action {
	return &esphome.Action{
		Name: "calendar_sources",
		Args: []esphome.Arg{{Name: "calendars", Type: esphome.ArgString}},
		Run: func(c esphome.Call) (any, error) {
			return nil, f.SetCalendarSources(strings.Split(c.String("calendars"), ","))
		},
	}
}
