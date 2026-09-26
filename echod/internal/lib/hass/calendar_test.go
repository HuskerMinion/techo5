package hass

import (
	"testing"
	"time"
)

// Home Assistant's calendar events as its API gives them: a timed event with an offset, an all-day
// one by its dates, and one whose end is missing.
func TestParseEvents(t *testing.T) {
	out := []byte(`[
		{"start":{"dateTime":"2026-09-26T15:00:00-06:00"},"end":{"dateTime":"2026-09-26T16:30:00-06:00"},
		 "summary":"Dentist","location":"Main St","description":"Bring the card"},
		{"start":{"date":"2026-09-26"},"end":{"date":"2026-09-27"},"summary":"Birthday"},
		{"start":{"dateTime":"2026-09-27T09:00:00Z"},"end":{},"summary":"Call"}
	]`)
	ev, err := parseEvents("calendar.family", out)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) != 3 {
		t.Fatalf("%d events", len(ev))
	}
	// Sorted by start: the all-day event starts at local midnight, before the dentist.
	if ev[0].Summary != "Birthday" || !ev[0].AllDay || ev[0].Start.Hour() != 0 || ev[0].End.Sub(ev[0].Start) != 24*time.Hour {
		t.Errorf("all-day: %+v", ev[0])
	}
	if ev[1].Summary != "Dentist" || ev[1].AllDay || ev[1].End.Sub(ev[1].Start) != 90*time.Minute ||
		ev[1].Location != "Main St" || ev[1].Description != "Bring the card" || ev[1].Calendar != "calendar.family" {
		t.Errorf("timed: %+v", ev[1])
	}
	if !ev[2].End.Equal(ev[2].Start) {
		t.Errorf("no end: %+v", ev[2])
	}
	if _, err := parseEvents("calendar.x", []byte(`[{"start":{"date":"soon"}}]`)); err == nil {
		t.Error("an unreadable date was taken")
	}
}
