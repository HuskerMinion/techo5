//go:build !dot

package display

import (
	"image/color"
	"slices"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// What the Show's calendar page and the Spot's calendar face share.

// calendarColors are the calendars' colors, in the order they were chosen.
var calendarColors = []color.RGBA{
	{224, 164, 72, 255},  // amber
	{88, 160, 214, 255},  // sky
	{118, 186, 112, 255}, // green
	{214, 108, 112, 255}, // rose
	{168, 128, 210, 255}, // violet
	{92, 190, 178, 255},  // teal
}

// calendarColor is a calendar's color, by its place among the calendars shown.
func calendarColor(order []string, cal string) color.RGBA {
	for i, c := range order {
		if c == cal {
			return calendarColors[i%len(calendarColors)]
		}
	}
	return calendarColors[len(calendarColors)-1]
}

// eventsOn is the events of a list that touch day.
func eventsOn(events []hass.Event, day time.Time) []hass.Event {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 0, 1)
	var out []hass.Event
	for _, e := range events {
		if e.Start.Before(end) && (e.End.After(start) || e.Start.Equal(start)) {
			out = append(out, e)
		}
	}
	// All-day events first, then by when they start.
	slices.SortStableFunc(out, func(a, b hass.Event) int {
		if a.AllDay != b.AllDay {
			if a.AllDay {
				return -1
			}
			return 1
		}
		return a.Start.Compare(b.Start)
	})
	return out
}
