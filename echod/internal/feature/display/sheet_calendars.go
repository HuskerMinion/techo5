//go:build !dot

package display

import (
	"log/slog"
	"slices"
	"strconv"
	"sync"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The Calendars row: which of Home Assistant's calendars this device shows. Its list marks each one
// shown or not: with one calendar a tap turns it on or off and closes the list; with several the list
// stays open for the next, and Done at its foot closes it.

// The marks beside each calendar. The screen's typeface has these circles and no check mark.
const (
	calendarOn    = "●  "
	calendarOff   = "○  "
	calendarsDone = "Done"
)

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

// calendarsListed are the calendars the Calendars list last showed, in its order: a choice in it is one
// of these, whatever Home Assistant's own list has become since.
var calendarsListed struct {
	sync.Mutex
	ids []string
}

func calendarsPicker() pickerView {
	p := pickerView{title: "Calendars", cur: -1}
	cals := home.Get().Calendars()
	calendarsListed.Lock()
	calendarsListed.ids = calendarsListed.ids[:0]
	for _, c := range cals {
		calendarsListed.ids = append(calendarsListed.ids, c.ID)
	}
	calendarsListed.Unlock()
	if len(cals) == 0 {
		p.opts = []string{"None in Home Assistant yet"}
		return p
	}
	src := home.Get().CalendarSources()
	for _, c := range cals {
		mark := calendarOff
		if slices.Contains(src, c.ID) {
			mark = calendarOn
		}
		p.opts = append(p.opts, mark+cmpOr(c.Name, c.ID))
	}
	if len(cals) > 1 {
		p.opts = append(p.opts, calendarsDone)
	}
	return p
}

// toggleCalendar turns the i'th of Home Assistant's calendars on or off, and reports whether the list
// should stay open for another: only when there are several to choose from.
func toggleCalendar(i int) bool {
	calendarsListed.Lock()
	ids := slices.Clone(calendarsListed.ids)
	calendarsListed.Unlock()
	if i < 0 || i >= len(ids) {
		return false
	}
	src := home.Get().CalendarSources()
	id := ids[i]
	if j := slices.Index(src, id); j >= 0 {
		src = slices.Delete(slices.Clone(src), j, j+1)
	} else {
		src = append(slices.Clone(src), id)
	}
	if err := home.Get().SetCalendarSources(src); err != nil {
		slog.Warn("choosing the calendars failed", "err", err)
		return false
	}
	return len(ids) > 1
}

// The pop-up rows, under Calendars once a calendar is shown: on or off, and when they are on, how early,
// the chime, all-day events, and which calendars.

var (
	popupLeadLabels = []string{"As it starts", "5 minutes before", "10 minutes before", "15 minutes before",
		"30 minutes before", "1 hour before"}
	popupLeads      = []int{0, 5, 10, 15, 30, 60}
	popupAllDayOpts = []string{"In the morning", "Never"}
)

const popupAllCalendars = "All calendars shown"

func popupLeadIndex() int {
	lead := config.Get().Calendar.PopupLead()
	for i, m := range popupLeads {
		if m == lead {
			return i
		}
	}
	return slices.Index(popupLeads, config.PopupDefaultBefore)
}

func popupAllDayIndex() int {
	if config.Get().Calendar.PopupAllDayNever {
		return 1
	}
	return 0
}

// popupCalendarsValue is the row's value: all, the one, or how many.
func popupCalendarsValue() string {
	pick := config.Get().Calendar.PopupCalendars
	switch len(pick) {
	case 0:
		return "All"
	case 1:
		return calendarName(pick[0])
	}
	return strconv.Itoa(len(pick)) + " calendars"
}

func calendarName(id string) string {
	for _, c := range home.Get().Calendars() {
		if c.ID == id {
			return cmpOr(c.Name, id)
		}
	}
	return id
}

// calendarRows are the Calendars row and, once a calendar is shown, the pop-up rows under it.
func calendarRows() []settingRow {
	rows := []settingRow{{id: "calendars", label: "Calendars", sub: "Shown on the calendar page, from Home Assistant",
		kind: ctlChoice, value: calendarsValue()}}
	c := config.Get().Calendar
	if !hasCalendarPopups || len(c.Sources) == 0 {
		return rows
	}
	rows = append(rows, settingRow{id: "calpop", label: "Event pop-ups", sub: "An event coming up, over the screen",
		kind: ctlToggle, on: c.Popups})
	if !c.Popups {
		return rows
	}
	return append(rows,
		settingRow{id: "calpopwhen", label: "Pop up", kind: ctlChoice, value: popupLeadLabels[popupLeadIndex()]},
		settingRow{id: "calpopsound", label: "Pop-up chime", sub: "Silent at night and in quiet hours", kind: ctlToggle, on: !c.PopupSilent},
		settingRow{id: "calpopallday", label: "All-day events", kind: ctlChoice, value: popupAllDayOpts[popupAllDayIndex()]},
		settingRow{id: "calpopcals", label: "Pop-up calendars", kind: ctlChoice, value: popupCalendarsValue()},
	)
}

// popupCalendarsPicker lists the calendars shown, each marked whether its events pop up, All at the
// top, and Done at the foot when there are several.
func popupCalendarsPicker() pickerView {
	c := config.Get().Calendar
	p := pickerView{title: "Pop-up calendars", cur: -1, opts: []string{popupAllCalendars}}
	if len(c.PopupCalendars) == 0 {
		p.cur = 0
	}
	for _, id := range c.Sources {
		mark := calendarOff
		if len(c.PopupCalendars) == 0 || slices.Contains(c.PopupCalendars, id) {
			mark = calendarOn
		}
		p.opts = append(p.opts, mark+calendarName(id))
	}
	if len(c.Sources) > 1 {
		p.opts = append(p.opts, calendarsDone)
	}
	return p
}

// choosePopupCalendar is a choice in that list: All, or one calendar turned on or off. It reports
// whether the list should stay open for another. The last calendar on stays on: none would be no
// pop-ups, which is what the switch above is for.
func choosePopupCalendar(i int) bool {
	c := config.Get().Calendar
	if i == 0 {
		_ = config.Set().Calendar().PopupCalendars(nil)
		return false
	}
	if i-1 >= len(c.Sources) {
		return false
	}
	id := c.Sources[i-1]
	pick := slices.Clone(c.PopupCalendars)
	if len(pick) == 0 {
		pick = slices.Clone(c.Sources)
	}
	if j := slices.Index(pick, id); j >= 0 {
		if len(pick) == 1 {
			return len(c.Sources) > 1
		}
		pick = slices.Delete(pick, j, j+1)
	} else {
		pick = append(pick, id)
	}
	if len(pick) == len(c.Sources) {
		pick = nil // every one again: All
	}
	if err := config.Set().Calendar().PopupCalendars(pick); err != nil {
		slog.Warn("choosing the pop-up calendars failed", "err", err)
	}
	return len(c.Sources) > 1
}
