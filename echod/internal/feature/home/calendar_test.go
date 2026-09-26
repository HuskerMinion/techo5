package home

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// fakeCalendars is Home Assistant's calendar API: two calendars, one of which fails.
func fakeCalendars(t *testing.T) *atomic.Int32 {
	t.Helper()
	var reads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/calendars":
			_, _ = w.Write([]byte(`[{"entity_id":"calendar.family","name":"Family"},{"entity_id":"calendar.work","name":"Work"}]`))
		case r.URL.Path == "/api/calendars/calendar.family":
			reads.Add(1)
			if r.URL.Query().Get("start") == "" || r.URL.Query().Get("end") == "" {
				http.Error(w, "no range", http.StatusBadRequest)
				return
			}
			// The dentist in the device's own zone, as Home Assistant gives times in its own.
			at := func(h int) string { return time.Date(2026, 9, 26, h, 0, 0, 0, time.Local).Format(time.RFC3339) }
			_, _ = w.Write([]byte(`[
				{"start":{"dateTime":"` + at(15) + `"},"end":{"dateTime":"` + at(16) + `"},"summary":"Dentist"},
				{"start":{"date":"2026-09-26"},"end":{"date":"2026-09-27"},"summary":"Birthday"},
				{"start":{"date":"2026-09-29"},"end":{"date":"2026-09-30"},"summary":"Trash day"}]`))
		case r.URL.Path == "/api/calendars/calendar.work":
			http.Error(w, "the login ran out", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	config.Use(filepath.Join(dir, "state.json"))
	was := hass.Path
	hass.Path = filepath.Join(dir, "hass.json")
	t.Cleanup(func() { hass.Path = was })
	if err := hass.Get().Set(srv.URL, "token"); err != nil {
		t.Fatal(err)
	}
	calendar.Lock()
	calendar.list, calendar.listAt, calendar.months = nil, time.Time{}, nil
	calendar.Unlock()
	return &reads
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("waited for %s", what)
}

// Home Assistant's calendars are listed, a month's events are read in the background and kept, one
// calendar failing leaves the other's events, and a day's events are the ones that touch it.
func TestCalendarMonths(t *testing.T) {
	reads := fakeCalendars(t)
	f := Get()
	f.Calendars()
	eventually(t, "the calendar list", func() bool { return len(f.Calendars()) == 2 })

	if ev, ok := f.MonthEvents(time.Date(2026, 9, 15, 0, 0, 0, 0, time.Local)); !ok || ev != nil {
		t.Fatalf("with none chosen: %v, %v", ev, ok)
	}
	if err := f.SetCalendarSources([]string{"calendar.family", " calendar.work ", "calendar.family"}); err != nil {
		t.Fatal(err)
	}
	if got := config.Get().Calendar.Sources; strings.Join(got, ",") != "calendar.family,calendar.work" {
		t.Fatalf("sources %v", got)
	}
	sept := time.Date(2026, 9, 15, 0, 0, 0, 0, time.Local)
	if _, ok := f.MonthEvents(sept); ok {
		t.Fatal("a month not read yet counted as read")
	}
	eventually(t, "September's events", func() bool { ev, ok := f.MonthEvents(sept); return ok && len(ev) == 3 })

	day, ok := f.EventsOn(time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local))
	if !ok || len(day) != 2 || day[0].Summary != "Birthday" || day[1].Summary != "Dentist" {
		t.Errorf("the 26th: %+v", day)
	}
	if day, _ := f.EventsOn(time.Date(2026, 9, 27, 12, 0, 0, 0, time.Local)); len(day) != 0 {
		t.Errorf("the 27th has %+v: an all-day event ends at midnight", day)
	}

	// Kept: asking again does not read again.
	n := reads.Load()
	f.MonthEvents(sept)
	time.Sleep(50 * time.Millisecond)
	if reads.Load() != n {
		t.Error("a month kept a moment ago was read again")
	}

	if err := f.SetCalendarSources([]string{"sensor.nope"}); err == nil {
		t.Error("a non-calendar entity was taken")
	}
}
