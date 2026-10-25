//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/palette"
)

// A made-up week for the calendar's look: three calendars, a Sunday in October at half past eight, a
// trip that runs into November, and a forecast for the fortnight.

var lookNow = time.Date(2026, 10, 18, 8, 30, 0, 0, time.Local)

const (
	calFamily = "calendar.family"
	calWork   = "calendar.work"
	calSchool = "calendar.school"
)

var lookOrder = []string{calFamily, calWork, calSchool}

var lookNames = map[string]string{calFamily: "Family", calWork: "Work", calSchool: "School"}

func oct(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, time.Local) }

func octDay(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.Local) }

func novDay(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.Local) }

func lookEvents() []hass.Event {
	ev := func(title, cal string, start, end time.Time, loc string) hass.Event {
		return hass.Event{Summary: title, Calendar: cal, Start: start, End: end, Location: loc}
	}
	day := func(title, cal string, from, to time.Time) hass.Event {
		return hass.Event{Summary: title, Calendar: cal, Start: from, End: to, AllDay: true}
	}
	return []hass.Event{
		ev("Pumpkin patch", calFamily, oct(18, 10, 0), oct(18, 12, 0), ""),
		ev("Soccer practice", calSchool, oct(18, 17, 30), oct(18, 19, 0), ""),
		ev("Soccer practice", calSchool, oct(25, 17, 30), oct(25, 19, 0), ""),
		ev("Team standup", calWork, oct(19, 9, 0), oct(19, 9, 30), ""),
		ev("Team standup", calWork, oct(20, 9, 0), oct(20, 9, 30), ""),
		ev("Team standup", calWork, oct(21, 9, 0), oct(21, 9, 30), ""),
		ev("Dentist", calFamily, oct(20, 15, 0), oct(20, 16, 0), "Main St Dental"),
		ev("Quarterly review", calWork, oct(21, 13, 0), oct(21, 15, 0), ""),
		day("Trash day", calFamily, octDay(21), octDay(22)),
		day("Trash day", calFamily, octDay(28), octDay(29)),
		day("Alex's birthday", calFamily, octDay(22), octDay(23)),
		ev("Parent-teacher night", calSchool, oct(23, 18, 0), oct(23, 20, 0), ""),
		day("Vacation", calFamily, octDay(29), novDay(4)),
		ev("Flight to Denver", calWork, oct(6, 7, 15), oct(6, 9, 40), ""),
		ev("Book club", calFamily, oct(9, 19, 0), oct(9, 21, 0), ""),
		day("Picture day", calSchool, octDay(14), octDay(15)),
	}
}

func lookForecast() []hass.Day {
	w := map[int]struct {
		cond     string
		high, lo float64
	}{
		18: {"sunny", 68, 45}, 19: {"partlycloudy", 64, 43}, 20: {"rainy", 55, 40}, 21: {"cloudy", 58, 41},
		22: {"sunny", 62, 42}, 23: {"partlycloudy", 60, 44}, 24: {"sunny", 63, 46}, 25: {"partlycloudy", 61, 44},
		26: {"rainy", 54, 39}, 27: {"cloudy", 56, 40}, 28: {"sunny", 59, 41}, 29: {"sunny", 61, 43},
	}
	var out []hass.Day
	for d := 18; d <= 29; d++ {
		out = append(out, hass.Day{When: octDay(d), Condition: w[d].cond, High: w[d].high, Low: w[d].lo, Rain: -1})
	}
	return out
}

// lookView is the calendar page on the sample week, as calendarScene would fill it in.
func lookView() calendarView {
	return calendarView{month: octDay(1), now: lookNow, loaded: true, order: lookOrder, names: lookNames,
		events: lookEvents(), forecast: lookForecast()}
}

func lookCard() *nextUpCard {
	c, _ := pickNextUp(lookEvents(), lookNow)
	c.color = calendarColor(lookOrder, c.ev.Calendar)
	if c.big != nil {
		c.bigColor = calendarColor(lookOrder, c.big.Calendar)
	}
	return &c
}

// calendarScenes are the calendar's views on the sample week, for the stock previews (TestShowScenesDraw).
func calendarScenes() map[string]scene {
	page := func(f func(v *calendarView)) scene {
		v := lookView()
		f(&v)
		return scene{now: lookNow, phase: "idle", showCalendar: true, cal: v}
	}
	return map[string]scene{
		"calendar-month":        page(func(*calendarView) {}),
		"calendar-month-filter": page(func(v *calendarView) { v.hidden = []string{calWork} }),
		"calendar-day":          page(func(v *calendarView) { v.day = octDay(21) }),
		"calendar-day-today":    page(func(v *calendarView) { v.day = octDay(18) }),
		"calendar-week":         page(func(v *calendarView) { v.day, v.week = octDay(18), true }),
		"calendar-agenda":       page(func(v *calendarView) { v.agenda = true }),
		"clock-next-up":         {now: lookNow, phase: "idle", weather: hassSunny(), nextUp: lookCard()},
	}
}

func hassSunny() home.Weather { return home.Weather{Condition: "sunny", Temp: "68°"} }

// With SHOW_PREVIEW set, the calendar's views on the sample week are written there on every Show panel
// and the Skylight's, in the default theme and in Paper, with each switch's other way: to look at
// beside the mockups.
func TestCalendarLookPreviews(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	if dir == "" {
		t.Skip("SHOW_PREVIEW is not set")
	}
	keepPalette(t)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Calendar().Sources(lookOrder); err != nil {
		t.Fatal(err)
	}
	variants := []struct {
		name string
		set  func()
	}{
		{"", func() {}},
		{"-icons-off", func() { _ = config.Set().Calendar().NoIcons(true) }},
		{"-full", func() { _ = config.Set().Calendar().MonthDetails(true) }},
		{"-simple", func() { _ = config.Set().Calendar().MonthDetails(false) }},
		{"-week-on", func() { _ = config.Set().Calendar().Week(true) }},
		{"-plain", func() {
			_ = config.Set().Calendar().DayList(true)
			_ = config.Set().Calendar().AgendaPlain(true)
		}},
	}
	for _, theme := range []string{"", "Paper"} {
		applyTheme(themes[themeIndex(cmpOr(theme, palette.Presets[0].Name))])
		for _, vr := range variants {
			config.Use(filepath.Join(t.TempDir(), "state.json"))
			_ = config.Set().Calendar().Sources(lookOrder)
			vr.set()
			for _, panel := range showPanels() {
				scenes := calendarScenes()
				if vr.name == "-week-on" {
					scenes["calendar-week-edges"] = reviewScenes()["calendar-week-edges"]
				}
				if vr.name == "" {
					for name, s := range reviewScenes() {
						scenes[name] = s
					}
					// The switches, under Settings, Display, Calendar. The scroll is in the panel's pixels, so it is
					// the Show 5's scaled to the panel, to land on the same switches on each.
					for _, at := range []int{1200, 1500} {
						scenes[fmt.Sprintf("settings-display-calendar-%d", at)] = scene{now: lookNow, phase: "idle", showSheet: true,
							sheet: settings{cat: catDisplay, brightness: 70, cardScroll: at * panel.wide / showWide}}
					}
				}
				for name, s := range scenes {
					if vr.name != "" && name == "clock-next-up" {
						continue
					}
					img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
					newRenderer(img).draw(s)
					file := name + vr.name
					if theme != "" {
						file += "-" + theme
					}
					f, err := os.Create(filepath.Join(dir, file+"-"+panel.name+".png"))
					if err != nil {
						t.Fatal(err)
					}
					if err := png.Encode(f, img); err != nil {
						t.Fatal(err)
					}
					_ = f.Close()
				}
			}
		}
	}
}

// reviewScenes are the edges the calendar's look had to be fixed for: a dozen calendars' chips and their
// list, a week with an event past midnight and one too short to see, and the card on the night light.
func reviewScenes() map[string]scene {
	many := lookView()
	many.order, many.names = nil, map[string]string{}
	for i, n := range []string{"Family", "Work", "School", "Soccer", "Piano", "Book club", "Grandma",
		"Neighborhood", "Holidays", "Trash pickup", "Birthdays", "Garden", "Swim team", "Church choir",
		"Volunteer shifts"} {
		id := fmt.Sprintf("calendar.c%d", i)
		many.order, many.names[id] = append(many.order, id), n
	}
	many.hidden = []string{"calendar.c11"}
	list := many
	list.chipsOpen = true
	week := lookView()
	week.day, week.week = octDay(18), true
	week.events = append(week.events,
		hass.Event{Summary: "Night shift", Calendar: calWork, Start: oct(20, 20, 0), End: oct(21, 4, 0)},
		hass.Event{Summary: "Call", Calendar: calWork, Start: oct(22, 12, 0), End: oct(22, 12, 5)},
		hass.Event{Summary: "Meteor shower", Calendar: calFamily, Start: oct(24, 23, 30), End: oct(24, 23, 59)},
		hass.Event{Summary: "Flight to Denver", Calendar: calWork, Start: oct(19, 4, 30), End: oct(19, 5, 0)},
		hass.Event{Summary: "Gym", Calendar: calFamily, Start: oct(19, 6, 0), End: oct(19, 7, 0)},
		hass.Event{Summary: "Sync", Calendar: calWork, Start: oct(22, 9, 0), End: oct(22, 9, 30)},
		hass.Event{Summary: "Review", Calendar: calWork, Start: oct(22, 9, 30), End: oct(22, 10, 0)},
		hass.Event{Summary: "Plan", Calendar: calWork, Start: oct(22, 10, 0), End: oct(22, 10, 30)})
	page := func(v calendarView) scene { return scene{now: lookNow, phase: "idle", showCalendar: true, cal: v} }
	return map[string]scene{
		"calendar-month-many":      page(many),
		"calendar-month-many-list": page(list),
		"calendar-week-edges":      page(week),
		"clock-next-up-night":      {now: lookNow, phase: "idle", weather: hassSunny(), nextUp: lookCard(), nightDim: true},
	}
}
