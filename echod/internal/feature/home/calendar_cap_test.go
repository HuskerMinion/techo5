package home

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// An event's words are kept to a size the screen can draw: the title and the place to 200 runes, the
// description to 2,000, whole runes, and a short one as it is.
func TestEventWordsAreCapped(t *testing.T) {
	long := strings.Repeat("é", 5000)
	e := capEvent(hass.Event{Summary: long, Location: long, Description: long})
	if n := utf8.RuneCountInString(e.Summary); n != titleRunes || !utf8.ValidString(e.Summary) {
		t.Errorf("title kept %d runes", n)
	}
	if n := utf8.RuneCountInString(e.Location); n != titleRunes {
		t.Errorf("place kept %d runes", n)
	}
	if n := utf8.RuneCountInString(e.Description); n != descriptionRunes {
		t.Errorf("description kept %d runes", n)
	}
	if e := capEvent(hass.Event{Summary: "Dentist"}); e.Summary != "Dentist" {
		t.Errorf("a short title became %q", e.Summary)
	}
}

// A month keeps at most monthEventsKept events, the earliest, and one that has fewer keeps them all.
func TestAMonthKeepsSoManyEvents(t *testing.T) {
	first := time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)
	var events []hass.Event
	for i := range monthEventsKept + 500 { // 40 a day, under what a day keeps, for 62 days
		events = append(events, hass.Event{Summary: "Every 36 minutes", Start: first.Add(time.Duration(i) * 36 * time.Minute)})
	}
	got := capMonth("2026-10", events)
	if len(got) != monthEventsKept || cap(got) != monthEventsKept {
		t.Fatalf("kept %d (room for %d); want %d", len(got), cap(got), monthEventsKept)
	}
	if !got[len(got)-1].Start.Equal(events[monthEventsKept-1].Start) {
		t.Error("not the earliest kept")
	}
	if got := capMonth("2026-11", events[:10]); len(got) != 10 {
		t.Errorf("a short month kept %d of 10", len(got))
	}
}

// A flood of events early in a month is cut on its own day, the earliest kept, and every later day
// keeps all it has, where the month's cap alone would have left them blank.
func TestAFloodOnOneDayLeavesTheOthers(t *testing.T) {
	first := time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)
	var events []hass.Event
	for i := range monthEventsKept + 500 { // every minute, from the first into the second
		events = append(events, hass.Event{Summary: "Every minute", Start: first.Add(time.Duration(i) * time.Minute)})
	}
	for d := 3; d <= 31; d++ {
		events = append(events, hass.Event{Summary: "Dentist", Start: first.AddDate(0, 0, d-1).Add(9 * time.Hour)})
	}
	got := capMonth("2026-10-flood", events)
	days := map[int]int{}
	for _, e := range got {
		days[e.Start.Day()]++
	}
	if days[1] != dayEventsKept || days[2] != dayEventsKept {
		t.Errorf("the flooded days kept %d and %d; want %d each", days[1], days[2], dayEventsKept)
	}
	if !got[dayEventsKept-1].Start.Equal(events[dayEventsKept-1].Start) {
		t.Error("not the flooded day's earliest kept")
	}
	for d := 3; d <= 31; d++ {
		if days[d] != 1 {
			t.Errorf("day %d kept %d of its 1", d, days[d])
		}
	}
}

// A month flooded on every one of its days keeps some on each, and no more than a month keeps.
func TestAMonthFloodedEveryDayKeepsSomeOnEach(t *testing.T) {
	first := time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)
	var events []hass.Event
	for d := range 31 {
		for m := range 100 {
			events = append(events, hass.Event{Summary: "Flood", Start: first.AddDate(0, 0, d).Add(time.Duration(m) * time.Minute)})
		}
	}
	got := capMonth("2026-10-every", events)
	if len(got) > monthEventsKept {
		t.Fatalf("kept %d; at most %d", len(got), monthEventsKept)
	}
	days := map[int]int{}
	for _, e := range got {
		days[e.Start.Day()]++
	}
	for d := 1; d <= 31; d++ {
		if days[d] != dayEventsKept {
			t.Errorf("day %d kept %d; want %d", d, days[d], dayEventsKept)
		}
	}
}
