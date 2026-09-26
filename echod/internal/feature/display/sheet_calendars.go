//go:build !dot

package display

import (
	"log/slog"
	"slices"
	"strconv"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The Calendars row: which of Home Assistant's calendars this device shows. Its list ticks each one on
// or off, and stays open so several can be chosen in a row.

const calendarTick = "✓  "

// calendarsValue is the row's value: the one calendar shown, how many, or none.
func calendarsValue() string {
	src := home.Get().CalendarSources()
	switch len(src) {
	case 0:
		return "None"
	case 1:
		for _, c := range home.Get().Calendars() {
			if c.ID == src[0] {
				return c.Name
			}
		}
		return src[0]
	}
	return strconv.Itoa(len(src)) + " calendars"
}

func calendarsPicker() pickerView {
	p := pickerView{title: "Calendars", cur: -1}
	cals := home.Get().Calendars()
	if len(cals) == 0 {
		p.opts = []string{"None in Home Assistant yet"}
		return p
	}
	src := home.Get().CalendarSources()
	for _, c := range cals {
		name := cmpOr(c.Name, c.ID)
		if slices.Contains(src, c.ID) {
			name = calendarTick + name
		}
		p.opts = append(p.opts, name)
	}
	return p
}

// toggleCalendar ticks the i'th of Home Assistant's calendars on or off, and reports whether it did.
func toggleCalendar(i int) bool {
	cals := home.Get().Calendars()
	if i < 0 || i >= len(cals) {
		return false
	}
	src := home.Get().CalendarSources()
	id := cals[i].ID
	if j := slices.Index(src, id); j >= 0 {
		src = slices.Delete(slices.Clone(src), j, j+1)
	} else {
		src = append(slices.Clone(src), id)
	}
	if err := home.Get().SetCalendarSources(src); err != nil {
		slog.Warn("choosing the calendars failed", "err", err)
		return false
	}
	return true
}
