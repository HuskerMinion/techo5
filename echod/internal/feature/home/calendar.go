package home

import (
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

	// titleRunes and descriptionRunes are the most of an event's title (and its place) and of its
	// description that are kept. The words come from whoever can write to the calendar, and a title of
	// thousands of letters is measured and cut on every frame that draws it.
	titleRunes       = 200
	descriptionRunes = 2000

	// monthEventsKept is the most events a month keeps, from all its calendars together. Whoever can
	// write to a calendar can fill a month with thousands (a repeat every minute), and every one of them
	// is sorted, merged and laid out again each time the screen works out a view; a month a person keeps
	// is a few hundred at the most.
	monthEventsKept = 2000

	// dayEventsKept is the most events a day keeps, the earliest, before the month is counted: a flood
	// of them early in a month would otherwise fill all it keeps and leave the later days blank. A
	// month's share for each of its days, so even a month flooded on every day keeps some on each.
	dayEventsKept = monthEventsKept / 31
)

// calendarVersion counts the changes to the events kept, for a screen that works something out from
// them to know when to work it out again (CalendarVersion).
var calendarVersion atomic.Uint64

// CalendarVersion is a number that changes whenever the events kept change.
func (f *Feature) CalendarVersion() uint64 { return calendarVersion.Load() }

// capRunes is s cut to at most n runes.
func capRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// capEvent keeps an event's words to a size the screen can draw at once.
func capEvent(e hass.Event) hass.Event {
	e.Summary = capRunes(e.Summary, titleRunes)
	e.Location = capRunes(e.Location, titleRunes)
	e.Description = capRunes(e.Description, descriptionRunes)
	return e
}

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
	// The calendars read from their iCal address come after Home Assistant's.
	return append(slices.Clone(calendar.list), linkCalendars()...)
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
		if !strings.HasPrefix(id, "calendar.") && !isLink(id) {
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
	calendarVersion.Add(1)
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
	readable := hass.Get().Ready() || slices.ContainsFunc(sources, isLink)
	if !m.busy && readable && (m.sources != joined || time.Since(m.at) > eventsEvery) {
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
		var ev []hass.Event
		var err error
		switch {
		case isLink(src):
			ev, err = linkEvents(src, first, first.AddDate(0, 1, 0))
		case hass.Get().Ready():
			ev, err = hass.Get().CalendarEvents(src, first, first.AddDate(0, 1, 0))
		default:
			continue
		}
		if err != nil {
			slog.Warn("calendar: reading events failed", "calendar", src, "month", key, "err", err)
			continue
		}
		for _, e := range ev {
			events = append(events, capEvent(e))
		}
		ok = true
	}
	slices.SortStableFunc(events, func(a, b hass.Event) int { return a.Start.Compare(b.Start) })
	events = capMonth(key, mergeSame(events))

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
	calendarVersion.Add(1)
	f.Changed.Emit(struct{}{})
}

// cappedMonths is the months already logged as having more events than are kept, so a month read
// again every eventsEvery says so once.
var cappedMonths sync.Map

// capMonth keeps a day's events to dayEventsKept and then the month's to monthEventsKept, the earliest
// of each, since events is sorted by start: a flood on one day is cut there, and every other day keeps
// what it has. A day is the one an event starts on, here. The log says how many, never what they are,
// once a month.
func capMonth(key string, events []hass.Event) []hass.Event {
	if len(events) <= dayEventsKept {
		return events
	}
	type date struct {
		year  int
		month time.Month
		day   int
	}
	perDay := map[date]int{}
	over := map[date]bool{}
	out := make([]hass.Event, 0, min(len(events), monthEventsKept))
	for _, e := range events {
		s := e.Start.Local()
		d := date{s.Year(), s.Month(), s.Day()}
		if perDay[d] >= dayEventsKept {
			over[d] = true
			continue
		}
		if len(out) == monthEventsKept {
			break
		}
		perDay[d]++
		out = append(out, e)
	}
	if len(out) == len(events) {
		return events
	}
	if _, told := cappedMonths.LoadOrStore(key, true); !told {
		slog.Warn("calendar: more events than are kept, a day's or the month's latest left out", "month", key,
			"events", len(events), "kept", len(out), "days over", len(over))
	}
	return slices.Clip(out)
}

// mergeSame shows an event that is on more than one of the calendars once: the same name, start, end
// and all-day-ness, on another calendar, is the same event (a family dinner on both parents'
// calendars). The first calendar's copy is kept, in the order the calendars were chosen, with the
// others named in Also. An event differing in any of those, even a little, is a different one and is
// left as it is, and so are two copies on one calendar. events is sorted by start.
func mergeSame(events []hass.Event) []hass.Event {
	out := make([]hass.Event, 0, len(events))
	kept := map[string]int{} // same() key -> index in out
	for _, e := range events {
		k := strings.TrimSpace(e.Summary) + "\x00" + e.Start.UTC().Format(time.RFC3339Nano) + "\x00" +
			e.End.UTC().Format(time.RFC3339Nano) + "\x00" + strconv.FormatBool(e.AllDay)
		if i, ok := kept[k]; ok && out[i].Calendar != e.Calendar && !slices.Contains(out[i].Also, e.Calendar) {
			out[i].Also = append(out[i].Also, e.Calendar)
			continue
		}
		if _, ok := kept[k]; !ok {
			kept[k] = len(out)
		}
		out = append(out, e)
	}
	return out
}

// OnCalendar is whether an event is on calendar, as its own or one it was merged with (mergeSame).
func OnCalendar(e hass.Event, calendar string) bool {
	return e.Calendar == calendar || slices.Contains(e.Also, calendar)
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

// AddCalendarLink keeps a calendar read from its iCal address and shows it on this device.
func (f *Feature) AddCalendarLink(name, addr string) error {
	c := config.Get().Calendar
	id := strconv.FormatInt(time.Now().UnixNano(), 36)
	links := append(slices.Clone(c.Links), config.CalendarLink{ID: id, Name: name, URL: addr})
	if err := config.Set().Calendar().Links(links); err != nil {
		return err
	}
	return f.SetCalendarSources(append(slices.Clone(c.Sources), config.CalendarLinkPrefix+id))
}

// RemoveCalendarLink forgets one, and stops showing it.
func (f *Feature) RemoveCalendarLink(id string) error {
	c := config.Get().Calendar
	var kept []config.CalendarLink
	for _, l := range c.Links {
		if l.ID == id {
			forgetICalFile(l.URL)
			continue
		}
		kept = append(kept, l)
	}
	if err := config.Set().Calendar().Links(kept); err != nil {
		return err
	}
	src := config.CalendarLinkPrefix + id
	return f.SetCalendarSources(slices.DeleteFunc(slices.Clone(c.Sources), func(s string) bool { return s == src }))
}
