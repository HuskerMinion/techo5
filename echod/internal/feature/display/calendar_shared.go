//go:build !dot

package display

import (
	"image/color"
	"slices"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
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

// visibleEvents is events without those of the calendars hidden on this screen. An event on several
// calendars (mergeSame) stays while any of them is shown, and is drawn as that one's: kept under the
// first calendar of them still shown, in the order the calendars were chosen, so it has that one's color
// and not the hidden one's, whose chip says it is not on the screen. The hidden one goes among the others
// it is on, so the event is still on it (home.OnCalendar) for what asks, such as which calendars pop up.
func visibleEvents(events []hass.Event, hidden []string) []hass.Event {
	if len(hidden) == 0 {
		return events
	}
	out := make([]hass.Event, 0, len(events))
	for _, e := range events {
		shown := !slices.Contains(hidden, e.Calendar)
		for _, c := range e.Also {
			shown = shown || !slices.Contains(hidden, c)
		}
		if !shown {
			continue
		}
		if slices.Contains(hidden, e.Calendar) {
			i := slices.IndexFunc(e.Also, func(c string) bool { return !slices.Contains(hidden, c) })
			also := append([]string{e.Calendar}, slices.Delete(slices.Clone(e.Also), i, i+1)...)
			e.Calendar, e.Also = e.Also[i], also
		}
		out = append(out, e)
	}
	return out
}

// hiddenAmong is the calendars of hidden that are still among those shown, in order: one hidden and
// then taken off the device is not left hidden for good, to come back hidden if it is chosen again
// some other day, and with a single calendar shown none is hidden, since there are no chips to show it
// again.
func hiddenAmong(hidden, shown []string) []string {
	if len(shown) <= 1 || len(hidden) == 0 {
		return nil
	}
	var out []string
	for _, h := range hidden {
		if slices.Contains(shown, h) && !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	return out
}

// hiddenHere is the calendars hidden on this screen, as hiddenAmong keeps them.
func hiddenHere(c config.Calendar) []string { return hiddenAmong(c.Hidden, c.Sources) }
