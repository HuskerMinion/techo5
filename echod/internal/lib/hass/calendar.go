package hass

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"time"
)

// Calendars and their events, from Home Assistant's calendar API. Home Assistant holds the accounts -
// Google, iCloud over CalDAV, Microsoft 365, a published calendar link, its own Local Calendar - and
// every one of them is a calendar.* entity here, read the same way.

// Calendar is one of Home Assistant's calendars.
type Calendar struct {
	ID, Name string
}

// Event is one event on a calendar. An all-day event starts at midnight on its first day and ends at
// midnight after its last, in the device's time zone.
type Event struct {
	Calendar    string // the calendar's entity
	Summary     string
	Description string
	Location    string
	Start, End  time.Time
	AllDay      bool
}

// Calendars lists Home Assistant's calendars.
func (c *Client) Calendars() ([]Calendar, error) {
	out, err := c.do("GET", "/api/calendars", nil)
	if err != nil {
		return nil, err
	}
	var list []struct {
		EntityID string `json:"entity_id"`
		Name     string `json:"name"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, err
	}
	cals := make([]Calendar, 0, len(list))
	for _, l := range list {
		cals = append(cals, Calendar{ID: l.EntityID, Name: l.Name})
	}
	return cals, nil
}

// calendarTime is an event's start or end as Home Assistant gives it: a dateTime for a timed event, a
// date for an all-day one.
type calendarTime struct {
	DateTime string `json:"dateTime"`
	Date     string `json:"date"`
}

func (t calendarTime) at() (time.Time, bool, error) {
	if t.DateTime != "" {
		at, err := time.Parse(time.RFC3339, t.DateTime)
		return at.In(time.Local), false, err
	}
	at, err := time.ParseInLocation("2006-01-02", t.Date, time.Local)
	return at, true, err
}

// CalendarEvents is a calendar's events that overlap from to to, in start order.
func (c *Client) CalendarEvents(entity string, from, to time.Time) ([]Event, error) {
	q := url.Values{"start": {from.Format(time.RFC3339)}, "end": {to.Format(time.RFC3339)}}
	out, err := c.do("GET", "/api/calendars/"+url.PathEscape(entity)+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	return parseEvents(entity, out)
}

func parseEvents(entity string, out []byte) ([]Event, error) {
	var raw []struct {
		Summary     string       `json:"summary"`
		Description string       `json:"description"`
		Location    string       `json:"location"`
		Start       calendarTime `json:"start"`
		End         calendarTime `json:"end"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	events := make([]Event, 0, len(raw))
	for _, r := range raw {
		start, allDay, err := r.Start.at()
		if err != nil {
			return nil, fmt.Errorf("hass: an event on %s starts %q: %w", entity, r.Start, err)
		}
		end, _, err := r.End.at()
		if err != nil || !end.After(start) {
			end = start // an event with no end, or one that ends before it starts, is a moment
		}
		events = append(events, Event{Calendar: entity, Summary: r.Summary, Description: r.Description,
			Location: r.Location, Start: start, End: end, AllDay: allDay})
	}
	slices.SortStableFunc(events, func(a, b Event) int { return a.Start.Compare(b.Start) })
	return events, nil
}
